package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bouliehaan/samo-server/internal/safego"
)

// preemptTick is how often the streamer re-asks the scheduler whether
// the currently-playing item should yield to a higher-priority rule.
// 15s is short enough that "ATC at 4pm" feels live (worst-case 15s
// late) without thrashing the database for channels with no rules.
const preemptTick = 15 * time.Second

const (
	// stallTimeout is how long an item may produce no audio before the
	// streamer abandons it and asks the scheduler for something else.
	//
	// This is the difference between a channel that recovers and one that
	// dies. A network source that completes its TCP handshake and then goes
	// quiet leaves ffmpeg blocked on read and stdout.Read blocked behind it,
	// forever — no error, no EOF, no log line, and no recovery until a human
	// notices the silence. 20s is well past any legitimate rebuffering gap on
	// a `-re`-paced input.
	stallTimeout = 20 * time.Second

	// stallCheck is how often the watchdog samples for that condition.
	stallCheck = 5 * time.Second

	// networkIOTimeoutMicros is ffmpeg's own read/write timeout for network
	// inputs, in microseconds (the unit -rw_timeout takes). Belt to the
	// watchdog's braces: ffmpeg aborting on its own is cleaner than us killing
	// it, and it covers the window before the watchdog's first sample.
	networkIOTimeoutMicros = 30_000_000

	// listenerBuffer is how many chunks a slow listener may fall behind before
	// it is dropped. At 16KiB chunks and 192kbps that is roughly 40 seconds of
	// slack, which absorbs mobile network stalls without letting one stuck
	// client backpressure the whole channel.
	listenerBuffer = 64
)

// StreamerOptions configures a per-channel streamer.
type StreamerOptions struct {
	// FFmpegPath is the absolute path to ffmpeg. Required.
	FFmpegPath string

	// Logger is optional; nil silences subprocess stderr.
	Logger *log.Logger

	// BaseContext roots every ffmpeg subprocess this streamer spawns. It must
	// be the process-lifetime context, because Go does not kill child
	// processes when it exits: a streamer rooted at context.Background() keeps
	// its transcoder running past shutdown, reparented to init and still
	// holding its input. install.sh restarts on every deploy, so that leaked
	// one ffmpeg per active channel per deploy, permanently. Defaults to
	// context.Background() only so tests can omit it.
	BaseContext context.Context
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
	baseCtx   context.Context

	mu        sync.Mutex
	listeners map[*listener]struct{}
	cancel    context.CancelFunc
	// done is non-nil exactly while a loop is meant to be running, and is
	// closed by that loop on exit. lastDone keeps the most recent one after a
	// stop so the next start can serialise behind it — see startLocked.
	done     chan struct{}
	lastDone chan struct{}

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
	baseCtx := opts.BaseContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return &channelStreamer{
		channel:   channel,
		deps:      deps,
		scheduler: scheduler,
		ffmpeg:    opts.FFmpegPath,
		logger:    logger,
		recorder:  recorder,
		baseCtx:   baseCtx,
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
	lis := &listener{ch: make(chan []byte, listenerBuffer)}

	// Registering the listener and deciding whether to start must happen under
	// one acquisition of s.mu, because detach makes the mirror-image decision
	// under the same lock. Splitting check from act let this interleaving
	// through: the last listener computed "I'm the last, stop the loop" and
	// released the lock; a new listener then saw the loop still marked running
	// and declined to start it; the first listener's stop then killed it. The
	// new listener stayed attached to a dead streamer — no bytes, no error, no
	// log, silent until it gave up.
	s.mu.Lock()
	s.listeners[lis] = struct{}{}
	s.startLocked()
	s.mu.Unlock()

	// detach is handed to an HTTP handler's defer, and a handler can panic
	// after attaching; sync.Once keeps a double call from double-counting the
	// listener set.
	var once sync.Once
	detach := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.listeners, lis)
			if len(s.listeners) == 0 {
				s.stopLocked()
			}
			s.mu.Unlock()
			lis.close()
		})
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

// startLocked launches the streaming loop unless one is already meant to be
// running. The caller must hold s.mu.
func (s *channelStreamer) startLocked() {
	if s.done != nil {
		return
	}

	// Serialise behind whichever loop we are replacing. A stop only *signals*
	// the previous loop; it still has to kill ffmpeg and drain its last read.
	// Starting a new loop before that finishes would leave two transcoders
	// writing into the same listener set, interleaving their output into
	// audible garbage.
	previous := s.lastDone

	ctx, cancel := context.WithCancel(s.baseCtx)
	done := make(chan struct{})
	s.cancel = cancel
	s.done = done
	s.lastDone = done

	safego.Go(fmt.Sprintf("channel %s streamer", s.channel.ID), func() {
		defer close(done)
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.loop(ctx)
	})
}

// stopLocked signals the running loop to unwind. The caller must hold s.mu.
// It does not wait — see stopAndWait for the shutdown path that does.
func (s *channelStreamer) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	// lastDone deliberately survives so the next start serialises behind this
	// loop's actual exit rather than merely its cancellation.
	s.done = nil
}

func (s *channelStreamer) stopLoop() {
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
}

// stopAndWait stops the loop and blocks until it has fully unwound, including
// reaping its ffmpeg subprocess. Used on the shutdown path, where returning
// before the transcoder is dead is exactly the orphan we are avoiding.
func (s *channelStreamer) stopAndWait(ctx context.Context) {
	s.mu.Lock()
	s.stopLocked()
	wait := s.lastDone
	s.mu.Unlock()
	if wait == nil {
		return
	}
	select {
	case <-wait:
	case <-ctx.Done():
		s.logger.Printf("channel %s: streamer did not stop before shutdown deadline", s.channel.ID)
	}
}

// loop pulls the next item from the scheduler, transcodes it via
// ffmpeg, and writes the encoded bytes to every attached listener
// until the item ends, the context is cancelled, or the item's
// MaxDuration elapses.
func (s *channelStreamer) loop(ctx context.Context) {
	// The loop owns `current`, so the loop clears it — that keeps now-playing
	// honest whether we exit via stop, shutdown, or a panic caught upstream,
	// and avoids taking currentMu while s.mu is held.
	defer func() {
		s.currentMu.Lock()
		s.current = nil
		s.currentLog = ""
		s.currentAt = time.Time{}
		s.currentMu.Unlock()
		s.logger.Printf("channel %s: streamer stopped", s.channel.ID)
	}()
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
	// Network inputs get ffmpeg's own I/O timeout plus automatic reconnect.
	// Without the timeout a source that stops sending mid-item wedges ffmpeg
	// indefinitely; without reconnect, an ordinary blip on a live stream costs
	// the listener the whole item. Local files take neither (the file protocol
	// ignores them and reconnect is meaningless).
	if isNetworkSource(item.URL) {
		args = append(args,
			"-rw_timeout", strconv.Itoa(networkIOTimeoutMicros),
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "5",
		)
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
		safego.Go(fmt.Sprintf("channel %s preempt watchdog", s.channel.ID), func() {
			ticker := time.NewTicker(preemptTick)
			defer ticker.Stop()
			for {
				select {
				case <-itemCtx.Done():
					return
				case <-ticker.C:
					if s.shouldPreempt(itemCtx, item) {
						s.logger.Printf("channel %s: preempting %q for scheduled rule", s.channel.ID, item.Title)
						itemCancel()
						return
					}
				}
			}
		})
	}

	// Stall watchdog. stdout.Read below blocks with no deadline of its own, so
	// a source that connects and then goes quiet would hold this item — and
	// therefore the channel — forever. Cancelling itemCtx kills ffmpeg, which
	// unblocks the read and sends the outer loop back to the scheduler for a
	// different pick. This is what makes a dead upstream a skipped track
	// instead of a dead station.
	lastByteAt := &atomic.Int64{}
	lastByteAt.Store(time.Now().UnixNano())
	safego.Go(fmt.Sprintf("channel %s stall watchdog", s.channel.ID), func() {
		ticker := time.NewTicker(stallCheck)
		defer ticker.Stop()
		for {
			select {
			case <-itemCtx.Done():
				return
			case <-ticker.C:
				idle := time.Since(time.Unix(0, lastByteAt.Load()))
				if idle >= stallTimeout {
					s.logger.Printf("channel %s: %q produced no audio for %s, skipping to next item",
						s.channel.ID, item.Title, idle.Truncate(time.Second))
					itemCancel()
					return
				}
			}
		}
	})

	// Pump stdout → listeners until EOF / cancel.
	buf := make([]byte, 16*1024)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			lastByteAt.Store(time.Now().UnixNano())
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
func (s *channelStreamer) shouldPreempt(parent context.Context, current PlaybackItem) bool {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
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

// isNetworkSource reports whether ffmpeg will reach this item over the network,
// which is what makes the I/O timeout and reconnect flags meaningful. Bare
// paths and file:// URLs are local.
func isNetworkSource(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "", "file":
		return false
	default:
		return true
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
