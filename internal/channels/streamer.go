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

	"github.com/bouliehaan/samo-server/internal/loudness"
	"github.com/bouliehaan/samo-server/internal/safego"
)

// LoudnessPlanner is the slice of internal/loudness the streamer uses, kept as
// an interface so streamer tests do not need a database or an ffmpeg.
type LoudnessPlanner interface {
	// PlanFor returns the gain for an item that is about to play, from cache
	// only — it never blocks on analysis, because nothing in a live radio
	// pipeline may wait on a subprocess.
	PlanFor(ctx context.Context, req loudness.Request) (loudness.Plan, loudness.Measurement, bool)
	// Warm measures an item ahead of time.
	Warm(ctx context.Context, req loudness.Request)
}

// warmDelay is how long into an item the streamer waits before asking what is
// coming next. The scheduler's answer depends on the play-log row written when
// this item started, so peeking immediately can return the item already
// playing; a few seconds is enough for that write to land and costs nothing,
// since the whole item is available to measure in.
const warmDelay = 5 * time.Second

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
	// notices the silence. 20s is well past any legitimate rebuffering gap
	// once audio is actually flowing.
	stallTimeout = 20 * time.Second

	// startupTimeout is the budget for the FIRST byte, which is a completely
	// different problem from a mid-stream gap and needs far longer.
	//
	// Before ffmpeg emits anything from a live stream it has to resolve DNS,
	// complete a TCP and TLS handshake, get an HTTP response, probe enough of
	// the input to identify the codec, start the encoder and fill an output
	// buffer — and a live stream delivers its probe data at real-time bitrate,
	// so that alone is seconds. Holding first-byte to the same 20s as a
	// mid-stream stall kills slow-but-healthy stations before they ever speak,
	// which presents as dead air with no error: the watchdog cancels, the item
	// reports zero bytes, and the loop tries again forever.
	startupTimeout = 60 * time.Second

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

	// Loudness levels items against each other. Nil means no normalisation,
	// which is what the channel did before this existed: every item aired at
	// whatever level it was mastered at.
	Loudness LoudnessPlanner
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
	loudness  LoudnessPlanner

	mu        sync.Mutex
	listeners map[*listener]struct{}
	cancel    context.CancelFunc
	// done is non-nil exactly while a loop is meant to be running, and is
	// closed by that loop on exit. lastDone keeps the most recent one after a
	// stop so the next start can serialise behind it — see startLocked.
	done     chan struct{}
	lastDone chan struct{}
	// idleStop is the pending "nobody is listening any more" teardown.
	//
	// Tearing the loop down the instant the last socket closes makes every
	// reconnect a fresh start: the programming decision is re-run and whatever
	// was mid-air is abandoned. The daemon reconnects for ordinary reasons — a
	// skip, a device settings change, a momentary blip on the loopback — so a
	// four-hour episode could be dropped forty seconds in, having already been
	// written to the play log as aired, which then rests it for hours.
	//
	// A short linger makes a reconnection re-attach to the loop that is already
	// running, which is what it always meant.
	idleStop *time.Timer
	// idleLinger is how long that grace period lasts. A field so a test does
	// not have to wait out the real one.
	idleLinger time.Duration

	// skipCancel ends the item playing right now. Held separately from the
	// loop's own cancel so a skip drops one item instead of tearing the
	// channel down and re-spinning ffmpeg from scratch.
	skipMu     sync.Mutex
	skipCancel context.CancelFunc

	// Mirror of the last item handed to the streamer, for now-playing.
	currentMu     sync.RWMutex
	lastError     string
	lastErrorItem string
	lastErrorAt   time.Time
	current       *PlaybackItem
	currentLog    string
	currentAt     time.Time
}

// PlayRecorder is the slice of the service the streamer uses to write
// play log entries. Decoupled so tests can stub it.
type PlayRecorder interface {
	OnPlayStart(channelID string, item PlaybackItem) (string, error)
	// OnPlayEnd closes out a play. It is given what aired and how much of it
	// actually went out, because "was this surfaced to the listener" is a
	// question about the part that played, not about the row existing: an
	// episode cut off after five minutes by a booked show has not reached
	// anybody, and treating that as a full airing is what used to burn it.
	OnPlayEnd(channelID string, item PlaybackItem, played time.Duration, completed bool, playLogID string)
	// OnPlayDiscard forgets a play entirely, for something skipped before it
	// meaningfully aired.
	OnPlayDiscard(playLogID string)
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
		channel:    channel,
		deps:       deps,
		scheduler:  scheduler,
		ffmpeg:     opts.FFmpegPath,
		logger:     logger,
		recorder:   recorder,
		baseCtx:    baseCtx,
		loudness:   opts.Loudness,
		listeners:  map[*listener]struct{}{},
		idleLinger: defaultIdleLinger,
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

// drain discards audio already queued for this listener, without closing it.
//
// Non-blocking so a listener that has gone away cannot wedge the drain, and
// bounded by what is in the buffer so it always terminates even while the
// broadcaster is still writing.
func (l *listener) drain() {
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return
	}
	for {
		select {
		case _, ok := <-l.ch:
			if !ok {
				return
			}
		default:
			return
		}
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

// flushListeners drops audio already queued for every listener.
//
// Without this a skip is not a skip. Each listener holds up to listenerBuffer
// chunks — around forty seconds of encoded audio at 192kbps, and more at lower
// bitrates — and downstream (samo-radio's own decoder and ring, a browser's
// media buffer) holds more again. Killing ffmpeg only stops NEW audio; every
// byte already queued still plays first, so pressing skip appears to do
// nothing for a minute or more. Dropping the queue is what makes it immediate.
//
// The cut is deliberate and audible. That is what skip means.
func (s *channelStreamer) flushListeners() {
	s.mu.Lock()
	listeners := make([]*listener, 0, len(s.listeners))
	for l := range s.listeners {
		listeners = append(listeners, l)
	}
	s.mu.Unlock()
	for _, l := range listeners {
		l.drain()
	}
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
	s.cancelIdleStopLocked()
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
				s.armIdleStopLocked()
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
// defaultIdleLinger is how long the programming loop keeps running with nobody
// listening. Long enough to cover a reconnect, short enough that a genuinely
// abandoned channel stops transcoding promptly.
const defaultIdleLinger = 20 * time.Second

// armIdleStopLocked schedules the teardown for a channel nobody is listening to.
func (s *channelStreamer) armIdleStopLocked() {
	s.cancelIdleStopLocked()
	linger := s.idleLinger
	if linger <= 0 {
		s.stopLocked()
		return
	}
	s.idleStop = time.AfterFunc(linger, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.idleStop = nil
		// Somebody came back while we were waiting, which is the whole point.
		if len(s.listeners) == 0 {
			s.stopLocked()
		}
	})
}

func (s *channelStreamer) cancelIdleStopLocked() {
	if s.idleStop != nil {
		s.idleStop.Stop()
		s.idleStop = nil
	}
}

func (s *channelStreamer) stopLocked() {
	s.cancelIdleStopLocked()
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

	failures := 0
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

		// Measure what comes after this while this one plays, so a first
		// airing is levelled like every other.
		safego.Go(fmt.Sprintf("channel %s loudness warm", s.channel.ID), func() {
			s.warmNext(ctx, item)
		})

		startedAt := time.Now()
		written, err := s.playItem(ctx, item)
		played := time.Since(startedAt)
		// A clean end-of-input is the only thing that counts as the whole item
		// having gone out. Everything else — a skip, a booked show cutting in,
		// the play window closing — left some of it unheard.
		completed := err == nil
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("channel %s: play error (%s): %v", s.channel.ID, item.Title, err)
			s.setLastError(item, err)
		} else if written > 0 {
			s.setLastError(PlaybackItem{}, nil)
		}

		// An item that delivered no audio never aired. Two consequences, both
		// of which must be handled or a dead source poisons the channel: its
		// play-log row would tell the rotation it got its share (and would mark
		// a podcast episode as heard), and a source that fails instantly would
		// be re-picked instantly, spinning as fast as ffmpeg can exit. That
		// spin is what dead air sounds like.
		if written == 0 {
			if s.recorder != nil && logID != "" {
				s.recorder.OnPlayDiscard(logID)
			}
			failures++
			delay := failureBackoff(failures)
			s.logger.Printf("channel %s: %q produced no audio; retrying in %s",
				s.channel.ID, item.Title, delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}

		failures = 0
		if s.recorder != nil {
			s.recorder.OnPlayEnd(s.channel.ID, item, played, completed, logID)
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
func (s *channelStreamer) playItem(ctx context.Context, item PlaybackItem) (int64, error) {
	if s.ffmpeg == "" {
		return 0, errors.New("ffmpeg path not configured")
	}
	var written int64
	itemCtx, itemCancel := context.WithCancel(ctx)
	defer itemCancel()
	s.skipMu.Lock()
	s.skipCancel = itemCancel
	s.skipMu.Unlock()
	defer func() {
		s.skipMu.Lock()
		s.skipCancel = nil
		s.skipMu.Unlock()
	}()
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

	// Level this item against everything else the channel plays. A constant
	// gain, computed from a measurement taken earlier — see internal/loudness
	// for why it is a constant and not a compressor.
	args := transcodeArgs(item, codec, ext, bitrate, sampleRate, s.loudnessFilter(itemCtx, item))

	cmd := exec.CommandContext(itemCtx, s.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg stdout: %w", err)
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg", s.channel.ID))
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start ffmpeg: %w", err)
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
						// A scheduled slot that starts a minute late because
						// the old item was still draining is not "on time".
						s.flushListeners()
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
	// started separates "has not begun yet" from "was playing and stopped",
	// which need very different patience.
	started := &atomic.Bool{}
	lastByteAt.Store(time.Now().UnixNano())
	safego.Go(fmt.Sprintf("channel %s stall watchdog", s.channel.ID), func() {
		ticker := time.NewTicker(stallCheck)
		defer ticker.Stop()
		for {
			select {
			case <-itemCtx.Done():
				return
			case <-ticker.C:
				// Two different questions, two different budgets.
				idle := time.Since(time.Unix(0, lastByteAt.Load()))
				if started.Load() {
					if idle >= stallTimeout {
						s.logger.Printf("channel %s: %q went quiet for %s mid-item, skipping to next",
							s.channel.ID, item.Title, idle.Truncate(time.Second))
						itemCancel()
						return
					}
					continue
				}
				if idle >= startupTimeout {
					s.logger.Printf("channel %s: %q never produced a first byte in %s — the source is unreachable or too slow to start",
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
			written += int64(n)
			started.Store(true)
			lastByteAt.Store(time.Now().UnixNano())
			s.broadcast(buf[:n])
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				readErr = nil
			}
			waitErr := cmd.Wait()
			if readErr != nil {
				return written, readErr
			}
			if waitErr != nil && !errors.Is(itemCtx.Err(), context.DeadlineExceeded) && !errors.Is(itemCtx.Err(), context.Canceled) {
				return written, waitErr
			}
			return written, nil
		}
		if itemCtx.Err() != nil {
			_ = cmd.Process.Kill()
			cmd.Wait()
			return written, itemCtx.Err()
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
	next, err := s.scheduler.PeekItem(ctx, s.channel.ID)
	if err != nil {
		return false
	}
	if !next.IsRuleDriven {
		return false
	}
	// Only an appointment that asked to cut in gets to. The default is
	// makeNext: let the item finish and start the block after it. With every
	// candidate already filtered to what fits before the anchor, that almost
	// never means a late start — and it means nothing is ever cut off
	// mid-sentence, which is what used to happen and what burned the episode
	// it happened to.
	if next.AnchorPolicy != "" && next.AnchorPolicy != StartImmediately {
		return false
	}
	// Same appointment wins again? Don't preempt — would just restart the
	// same source mid-stream.
	if next.AnchorBlockID != "" && next.AnchorBlockID == current.AnchorBlockID {
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

// transcodeArgs builds the ffmpeg command line for one item.
//
// Split out of playItem so the argument order — which is not cosmetic, since
// ffmpeg reads options positionally relative to -i — can be tested without
// spawning anything.
func transcodeArgs(item PlaybackItem, codec, ext string, bitrate, sampleRate int, loudnessFilter string) []string {
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
		// Stop ffmpeg spending its default five seconds of audio and 5MB
		// probing a stream whose codec is obvious. On a live source that probe
		// data arrives at real-time bitrate, so the default is literally
		// seconds of silence before the first byte reaches a listener — the
		// single biggest contributor to "it takes forever to start, then the
		// watchdog kills it".
		args = append(args,
			"-analyzeduration", "3000000",
			"-probesize", "1000000",
		)
	}
	// `-re` pace input at real-time for local files / static remote
	// files. Live streams already arrive in real-time, don't double-
	// pace them.
	if !item.Live {
		args = append(args, "-re")
	}
	args = append(args, "-i", item.URL, "-vn")
	if loudnessFilter != "" {
		args = append(args, "-af", loudnessFilter)
	}
	return append(args,
		"-ac", "2",
		"-ar", strconv.Itoa(sampleRate),
		"-b:a", strconv.Itoa(bitrate)+"k",
		"-c:a", codec,
		"-f", ext,
		"pipe:1",
	)
}

// loudnessFilter returns the -af filtergraph that levels this item, or "" to
// leave it alone.
//
// Cache-only by design. A channel is a live pipeline: making the transcoder
// wait on an analysis subprocess would put dead air between every pair of
// items, which is a far worse fault than one item airing at its native level.
// An unmeasured item plays as-is and gets measured for next time — and warmNext
// means "next time" is usually the same item's first airing, not its second.
func (s *channelStreamer) loudnessFilter(ctx context.Context, item PlaybackItem) string {
	if s.loudness == nil || item.URL == "" {
		return ""
	}
	plan, measurement, ok := s.loudness.PlanFor(ctx,
		loudness.RequestFor(item.URL, item.DurationSeconds, item.Live))
	if !ok {
		return ""
	}
	if spec := plan.FilterSpec(); spec != "" {
		s.logger.Printf("channel %s: %q %s", s.channel.ID, item.Title, plan.Describe(measurement))
		return spec
	}
	return ""
}

// warmNext measures whatever the scheduler would pick next, using the time the
// current item takes to play.
//
// This is what makes the first airing of a new episode as level as the tenth.
// Analysis is much faster than real time, so a whole item's duration is a
// generous budget even for a long one, and a measurement that does not finish
// in time simply lands in the cache a bit later.
func (s *channelStreamer) warmNext(ctx context.Context, current PlaybackItem) {
	if s.loudness == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(warmDelay):
	}
	next, err := s.scheduler.PeekItem(ctx, s.channel.ID)
	if err != nil || next.URL == "" || next.URL == current.URL {
		return
	}
	s.loudness.Warm(ctx, loudness.RequestFor(next.URL, next.DurationSeconds, next.Live))
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

// skipCurrent ends whatever is playing right now, if anything is.
//
// It cancels the item's context, which is the same mechanism the preemption
// watchdog and the MaxDuration timeout already use — so the loop's normal
// "item finished, ask the scheduler again" path runs, rather than a second
// way of moving on that could drift from the first.
func (s *channelStreamer) skipCurrent() bool {
	s.skipMu.Lock()
	cancel := s.skipCancel
	s.skipMu.Unlock()
	if cancel == nil {
		return false
	}

	// Forget the play if barely any of it went out. Otherwise the log says the
	// channel aired it, and the scheduler believes the log: the episode stops
	// being fresh and sinks to the back of the rerun queue, permanently, over a
	// few seconds of audio.
	s.currentMu.RLock()
	logID, startedAt := s.currentLog, s.currentAt
	s.currentMu.RUnlock()
	if logID != "" && time.Since(startedAt) < countsAsAired {
		s.recorder.OnPlayDiscard(logID)
		s.currentMu.Lock()
		s.currentLog = ""
		s.currentMu.Unlock()
	}

	s.flushListeners()
	cancel()
	return true
}

// countsAsAired is how much of an item has to go out before the channel is
// considered to have played it. Below this it was skipped past, not aired.
const countsAsAired = 60 * time.Second

// currentSourceID is the source behind what is playing, for source-level skips.
func (s *channelStreamer) currentSourceID() string {
	s.currentMu.RLock()
	defer s.currentMu.RUnlock()
	if s.current == nil {
		return ""
	}
	return s.current.SourceID
}

// failureBackoff spaces out retries when nothing will play: 2s, 4s, 8s… to 30s.
// Without it a source that fails on connect is retried as fast as ffmpeg can
// exit, which is thousands of attempts an hour and sounds like dead air.
func failureBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := time.Duration(1<<failures) * time.Second
	if delay > 30*time.Second || delay <= 0 {
		delay = 30 * time.Second
	}
	return delay
}

// setLastError records why the channel has gone quiet, so the status panel can
// say so instead of leaving silence to be interpreted.
func (s *channelStreamer) setLastError(item PlaybackItem, err error) {
	s.currentMu.Lock()
	defer s.currentMu.Unlock()
	if err == nil {
		s.lastError, s.lastErrorItem, s.lastErrorAt = "", "", time.Time{}
		return
	}
	s.lastError = err.Error()
	s.lastErrorItem = firstNonEmpty(item.SourceLabel, item.Title)
	s.lastErrorAt = time.Now().UTC()
}

// LastError reports the most recent playback failure on this channel.
func (s *channelStreamer) LastError() (string, string, time.Time) {
	s.currentMu.RLock()
	defer s.currentMu.RUnlock()
	return s.lastError, s.lastErrorItem, s.lastErrorAt
}
