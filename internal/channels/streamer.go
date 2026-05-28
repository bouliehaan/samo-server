package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// preemptTick is how often the streamer re-asks the scheduler whether
// the currently-playing item should yield to a higher-priority rule.
// 15s is short enough that "ATC at 4pm" feels live (worst-case 15s
// late) without thrashing the database for channels with no rules.
const preemptTick = 15 * time.Second

// StreamerOptions configures a per-channel streamer.
type StreamerOptions struct {
	// FFmpegPath is the absolute path to ffmpeg. Required.
	FFmpegPath string

	// Logger is optional; nil silences subprocess stderr.
	Logger *log.Logger
}

// channelStreamer owns one channel's playback pipeline:
//
//	scheduler → playback item → ffmpeg subprocess → in-memory
//	broadcaster → connected HTTP listeners.
//
// The streamer is lazy: it spins up only when the first listener
// connects and tears down when the last one leaves. While running,
// each item is transcoded to the channel's configured output format
// so podcast (mp3), commercial (m4a), live HTTP stream, etc. all mux
// into one continuous output the listeners experience as radio.
type channelStreamer struct {
	channel   Channel
	deps      Dependencies
	scheduler *Scheduler
	ffmpeg    string
	logger    *log.Logger
	recorder  PlayRecorder

	mu        sync.Mutex
	listeners map[*listener]struct{}
	running   bool
	cancel    context.CancelFunc

	// Mirror of the last item handed to the streamer, for now-playing.
	currentMu  sync.RWMutex
	current    *PlaybackItem
	currentLog string
	currentAt  time.Time
}

// PlayRecorder is the slice of the service the streamer uses to write
// play log entries. Decoupled so tests can stub it.
type PlayRecorder interface {
	OnPlayStart(channelID string, item PlaybackItem) (string, error)
	OnPlayEnd(playLogID string)
}

func newChannelStreamer(channel Channel, deps Dependencies, scheduler *Scheduler, opts StreamerOptions, recorder PlayRecorder) *channelStreamer {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &channelStreamer{
		channel:   channel,
		deps:      deps,
		scheduler: scheduler,
		ffmpeg:    opts.FFmpegPath,
		logger:    logger,
		recorder:  recorder,
		listeners: map[*listener]struct{}{},
	}
}

// listener is one connected HTTP client. The streamer fans bytes out
// by writing to each listener's chan; if a listener can't keep up its
// channel fills, we drop it (slow listeners shouldn't backpressure the
// whole stream).
type listener struct {
	ch     chan []byte
	closed bool
	mu     sync.Mutex
}

func (l *listener) send(buf []byte) bool {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	l.mu.Unlock()
	clone := make([]byte, len(buf))
	copy(clone, buf)
	select {
	case l.ch <- clone:
		return true
	default:
		// Listener is slow / disconnected. Caller will drop us.
		return false
	}
}

func (l *listener) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	close(l.ch)
}

// Attach hooks a listener into the broadcast and ensures the streamer
// is running. Returns a function the caller defers to detach.
func (s *channelStreamer) Attach() (*listener, func()) {
	lis := &listener{ch: make(chan []byte, 64)}
	s.mu.Lock()
	s.listeners[lis] = struct{}{}
	shouldStart := !s.running
	s.mu.Unlock()
	if shouldStart {
		s.startLoop()
	}
	detach := func() {
		s.mu.Lock()
		delete(s.listeners, lis)
		empty := len(s.listeners) == 0
		s.mu.Unlock()
		lis.close()
		if empty {
			s.stopLoop()
		}
	}
	return lis, detach
}

// Now returns a snapshot of what's currently playing on this channel,
// suitable for the now-playing API. Returns nil when nothing is loaded.
func (s *channelStreamer) Now() (PlaybackItem, time.Time, string, bool) {
	s.currentMu.RLock()
	defer s.currentMu.RUnlock()
	if s.current == nil {
		return PlaybackItem{}, time.Time{}, "", false
	}
	return *s.current, s.currentAt, s.currentLog, true
}

// ListenerCount returns the number of currently attached listeners.
// Used by the now-playing endpoint so the UI can show "3 listeners" and
// confirm a stream is reaching real ears.
func (s *channelStreamer) ListenerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.listeners)
}

func (s *channelStreamer) startLoop() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	go s.loop(ctx)
}

func (s *channelStreamer) stopLoop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.running = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.currentMu.Lock()
	s.current = nil
	s.currentLog = ""
	s.currentAt = time.Time{}
	s.currentMu.Unlock()
}

// loop pulls the next item from the scheduler, transcodes it via
// ffmpeg, and writes the encoded bytes to every attached listener
// until the item ends, the context is cancelled, or the item's
// MaxDuration elapses.
func (s *channelStreamer) loop(ctx context.Context) {
	defer s.logger.Printf("channel %s: streamer stopped", s.channel.ID)
	s.logger.Printf("channel %s: streamer started", s.channel.ID)

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		item, err := s.scheduler.NextItem(ctx, s.channel.ID)
		if err != nil {
			s.logger.Printf("channel %s: scheduler error: %v", s.channel.ID, err)
			// Brief sleep to avoid a tight error loop while user
			// fixes their configuration.
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		logID := ""
		if s.recorder != nil {
			if id, err := s.recorder.OnPlayStart(s.channel.ID, item); err == nil {
				logID = id
			}
		}
		s.currentMu.Lock()
		copyItem := item
		s.current = &copyItem
		s.currentLog = logID
		s.currentAt = time.Now().UTC()
		s.currentMu.Unlock()

		if err := s.playItem(ctx, item); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("channel %s: play error (%s): %v", s.channel.ID, item.Title, err)
		}

		if s.recorder != nil {
			s.recorder.OnPlayEnd(logID)
		}
	}
}

// playItem runs ffmpeg on the item's URL and copies its stdout into
// the broadcaster. Returns nil on normal end-of-input, an error on
// subprocess failure.
//
// Two things can end an item early:
//   - MaxDuration timeout (live cut-in window ended, channel deleted, …)
//   - Preemption: a higher-priority schedule rule just became active
//     while we were mid-track. We poll the scheduler every preemptTick
//     and bail when the next pick differs from what we're playing.
//
// Preemption is what makes "NPR at 16:00" feel like real radio
// instead of "NPR at whenever the previous track happened to finish."
func (s *channelStreamer) playItem(ctx context.Context, item PlaybackItem) error {
	if s.ffmpeg == "" {
		return errors.New("ffmpeg path not configured")
	}
	itemCtx, itemCancel := context.WithCancel(ctx)
	defer itemCancel()
	if item.MaxDuration > 0 {
		timed, cancel := context.WithTimeout(itemCtx, item.MaxDuration)
		defer cancel()
		itemCtx = timed
	}

	codec, ext := codecArgs(s.channel.Codec)
	bitrate := s.channel.BitrateKbps
	if bitrate <= 0 {
		bitrate = 192
	}
	sampleRate := s.channel.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = 44100
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
	}
	// `-re` pace input at real-time for local files / static remote
	// files. Live streams already arrive in real-time, don't double-
	// pace them.
	if !item.Live {
		args = append(args, "-re")
	}
	args = append(args,
		"-i", item.URL,
		"-vn",
		"-ac", "2",
		"-ar", strconv.Itoa(sampleRate),
		"-b:a", strconv.Itoa(bitrate)+"k",
		"-c:a", codec,
		"-f", ext,
		"pipe:1",
	)

	cmd := exec.CommandContext(itemCtx, s.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout: %w", err)
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg", s.channel.ID))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Preemption watchdog. Every preemptTick we re-ask the scheduler
	// what should be playing right now. If it returns a different
	// source than what we started this item with — only happens when
	// a higher-priority schedule rule has just become active — we
	// cancel itemCtx to kill the ffmpeg subprocess, the read loop
	// breaks on EOF, and the outer loop calls NextItem which picks
	// up the new rule. Items launched FROM a rule are exempt to avoid
	// infinitely re-preempting themselves.
	if !item.IsRuleDriven {
		go func() {
			ticker := time.NewTicker(preemptTick)
			defer ticker.Stop()
			for {
				select {
				case <-itemCtx.Done():
					return
				case <-ticker.C:
					if s.shouldPreempt(item) {
						s.logger.Printf("channel %s: preempting %q for scheduled rule", s.channel.ID, item.Title)
						itemCancel()
						return
					}
				}
			}
		}()
	}

	// Pump stdout → listeners until EOF / cancel.
	buf := make([]byte, 16*1024)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			s.broadcast(buf[:n])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = nil
			}
			waitErr := cmd.Wait()
			if readErr != nil {
				return readErr
			}
			if waitErr != nil && !errors.Is(itemCtx.Err(), context.DeadlineExceeded) && !errors.Is(itemCtx.Err(), context.Canceled) {
				return waitErr
			}
			return nil
		}
		if itemCtx.Err() != nil {
			_ = cmd.Process.Kill()
			cmd.Wait()
			return itemCtx.Err()
		}
	}
}

// shouldPreempt returns true when the scheduler now wants to play
// something rule-driven and that something is NOT the current item.
// Rule-vs-rule and rotation-vs-rotation transitions are ignored
// (the natural end-of-item transition handles those) so we only
// interrupt for the case that actually matters: a live cut-in or
// scheduled block claiming the airwaves.
func (s *channelStreamer) shouldPreempt(current PlaybackItem) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	next, err := s.scheduler.NextItem(ctx, s.channel.ID)
	if err != nil {
		return false
	}
	if !next.IsRuleDriven {
		return false
	}
	// Same rule wins again? Don't preempt — would just restart the
	// same source mid-stream.
	if next.RuleID != "" && next.RuleID == current.RuleID {
		return false
	}
	// Same source picked by a different code path (e.g., we were on
	// rotation and the rule now points at the same source). Avoid the
	// pop.
	if next.SourceID != "" && next.SourceID == current.SourceID {
		return false
	}
	return true
}

func (s *channelStreamer) broadcast(buf []byte) {
	s.mu.Lock()
	listeners := make([]*listener, 0, len(s.listeners))
	for l := range s.listeners {
		listeners = append(listeners, l)
	}
	s.mu.Unlock()
	for _, l := range listeners {
		if !l.send(buf) {
			s.mu.Lock()
			delete(s.listeners, l)
			s.mu.Unlock()
			l.close()
		}
	}
}

func codecArgs(codec string) (string, string) {
	switch codec {
	case "aac":
		return "aac", "adts"
	case "ogg":
		return "libvorbis", "ogg"
	case "opus":
		return "libopus", "ogg"
	case "mp3", "":
		fallthrough
	default:
		return "libmp3lame", "mp3"
	}
}

// prefixWriter wraps a logger so ffmpeg stderr lands in the same log
// stream the rest of the server uses, tagged with the channel id.
type prefixWriter struct {
	logger *log.Logger
	prefix string
}

func newPrefixWriter(logger *log.Logger, prefix string) io.Writer {
	return &prefixWriter{logger: logger, prefix: prefix}
}

func (p *prefixWriter) Write(buf []byte) (int, error) {
	p.logger.Printf("%s: %s", p.prefix, string(buf))
	return len(buf), nil
}
