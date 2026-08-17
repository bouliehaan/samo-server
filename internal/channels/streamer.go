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
	"strings"
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

	// streamChunk is the read size the pump fans out to listeners.
	streamChunk = 16 * 1024

	// defaultBitrateKbps is what a channel encodes at when it says nothing.
	defaultBitrateKbps = 192

	// listenerJitter is the depth a listener is allowed to sit at once it is
	// keeping up again — the difference between a jitter buffer and a reservoir.
	//
	// listenerBuffer alone is a ceiling, not a target, and a queue between two
	// real-time endpoints never comes back down on its own. The encoder produces
	// at 1x (see -re) and the far end consumes at 1x, because it is feeding a
	// sound card at a fixed rate; nothing in that loop ever reads FASTER than
	// real time. So every transient — a network blip, a busy disk, a slow
	// reconnect — permanently adds its own length to the queue, and the listener
	// stays that far behind for as long as it remains connected. The depth only
	// ever ratchets up, toward forty seconds.
	//
	// That is a station that is punctual on its own clock and late at the
	// speaker: the scheduler cuts to the news at exactly 08:00:00 and the
	// listener hears it half a minute later, because half a minute of the
	// previous programme is still queued in front of it.
	//
	// So past this depth the OLDEST audio is dropped rather than kept. Falling
	// behind on live radio is resolved by catching up, never by playing stale
	// audio late — which is the same reason a skip drains the queue instead of
	// letting it play out. 2.5s is comfortably more than the encoder's 16KiB
	// chunking and fan-out jitter, and small enough that a boundary lands when
	// the schedule says it does.
	//
	// A DURATION converted per channel, not a chunk count, because the depth
	// that matters is seconds of audio: the same 4 chunks is 2.7s at 192kbps and
	// 5.5s at 96kbps, so a fixed count would quietly make a low-bitrate channel
	// less punctual than a high-bitrate one.
	listenerJitterTarget = 2500 * time.Millisecond

	// listenerJitterFloor is the minimum depth, in chunks, and it WINS over the
	// target when the two disagree.
	//
	// A trim drops a whole chunk, so a queue allowed to sit at one chunk trims
	// on ordinary handler jitter and drops a chunk of audio every time it does.
	// Two is the smallest depth where normal jitter has somewhere to go. Below
	// ~110kbps that is worth slightly more than the target (2.7s at 96kbps
	// against a 2.5s target), which is the right way round: a fraction of a
	// second of extra delay on a low-bitrate channel beats regular dropouts.
	listenerJitterFloor = 2
)

// jitterChunks converts the jitter target into a queue depth for one channel.
//
// Chunks are at most streamChunk bytes (a short read gives less), so this is a
// conservative ceiling: the real queued duration is never longer than the
// target, only shorter.
func jitterChunks(bitrateKbps int) int {
	if bitrateKbps <= 0 {
		bitrateKbps = defaultBitrateKbps
	}
	bytesPerSecond := float64(bitrateKbps) * 1000 / 8
	chunks := int(listenerJitterTarget.Seconds() * bytesPerSecond / streamChunk)
	if chunks < listenerJitterFloor {
		chunks = listenerJitterFloor
	}
	return chunks
}

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

	// warm is the source of the next booked show, connected early so the
	// boundary costs nothing. Owned by the streamer rather than by the item
	// that started it, because it is meant to outlive that item.
	warmMu sync.Mutex
	warm   *warmSource

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
	ch chan []byte
	// jitter is the depth this listener is allowed to sit at, in chunks. See
	// listenerJitterTarget; it is per-listener because it is derived from the
	// channel's bitrate.
	jitter int
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
		// Keep the queue a jitter buffer. Without this it only ever grows —
		// see listenerJitter for why that turns into permanent lateness.
		l.catchUp()
		return true
	default:
		// Listener is slow / disconnected. Caller will drop us.
		return false
	}
}

// catchUp drops the oldest queued audio until the listener is no more than
// listenerJitter chunks behind.
//
// The newest audio is what a live station owes its listener, so the front of
// the queue goes and the back stays. A listener that has been stalled hears one
// discontinuity as it rejoins, which is the honest outcome: the audio it missed
// played while it was not listening, and replaying it late would put it behind
// by that much for the rest of the connection.
//
// Non-blocking and bounded by the queue's own capacity, for the same reasons
// drain is: the broadcaster must never wedge here, and the HTTP handler is
// concurrently receiving from the same channel — a chunk taken by the handler
// instead of by this loop is a chunk delivered, which is the outcome we want
// anyway.
func (l *listener) catchUp() {
	target := l.jitter
	if target < listenerJitterFloor {
		target = listenerJitterFloor
	}
	for len(l.ch) > target {
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
	lis := &listener{
		ch:     make(chan []byte, listenerBuffer),
		jitter: jitterChunks(s.channel.BitrateKbps),
	}

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
		// A connection warmed for a boundary this channel will not reach is a
		// held socket and a running ffmpeg with nobody to hand them to.
		s.dropWarm()
		s.logger.Printf("channel %s: streamer stopped", s.channel.ID)
	}()
	s.logger.Printf("channel %s: streamer started", s.channel.ID)

	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		// Where the cycle stood BEFORE this decision.
		//
		// The programme state is committed when the item is chosen, not when it
		// airs, so an item that never produces a sound still spends its turn:
		// the obligation position is used up by something nobody heard and the
		// cycle moves on to a break. One unplayable episode therefore costs a
		// podcast slot AND replaces it with music — and since the next
		// obligation position comes round after that break, a show that fails
		// every time turns the whole day into music. Keeping the previous state
		// is what lets a failure be undone.
		priorState := s.priorProgramState(ctx)

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
			// And stop the scheduler handing back the very same corpse.
			//
			// Discarding the play-log row keeps a dead item from being counted
			// as heard, and the backoff keeps the retry from spinning — but
			// neither tells the SELECTION anything, so the next decision picks
			// the same unplayable episode again. On a cycle that alternates
			// break and obligation, what the listener gets is music, a dead
			// pick nobody hears, music, a dead pick — which sounds exactly like
			// the station has quietly given up on podcasts.
			//
			// Passing over the item is the same thing a skip does, and for the
			// same reason: not this one, ask again.
			if skips := s.skipRegistry(); skips != nil {
				if item.ItemRef != "" {
					skips.SuppressRef(item.ItemRef)
				}
				// A whole feed can be down, not just one episode. Once several
				// in a row have failed, step off the source too rather than
				// working through its back catalogue one corpse at a time.
				// An item that produced no audio did not happen, so the cycle must
				// not have moved past it. Put the position back and let the next
				// decision fill it properly — with the dead item now passed over,
				// that is the next thing you are owed rather than a song.
				s.rewindProgramState(ctx, priorState)

				if failures >= deadSourceAfter && item.SourceID != "" {
					skips.Suppress(item.SourceID, DefaultSkipSuppression)
					s.logger.Printf("channel %s: %d failures in a row from %q; stepping off it for %s",
						s.channel.ID, failures, firstNonEmpty(item.SourceLabel, item.SourceID),
						DefaultSkipSuppression)
				}
			}
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
		bitrate = defaultBitrateKbps
	}
	sampleRate := s.channel.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = 44100
	}

	// Level this item against everything else the channel plays. A constant
	// gain, computed from a measurement taken earlier — see internal/loudness
	// for why it is a constant and not a compressor.
	args := transcodeArgs(item, codec, ext, bitrate, sampleRate, s.loudnessFilter(itemCtx, item))

	// Already connected, if this is the appointment the last item was warming
	// up for. Adopting it is the difference between a booked show that opens on
	// its second and one that opens two and a half seconds later, which is how
	// long a live station takes to answer.
	source, err := s.openSource(itemCtx, item, args)
	if err != nil {
		return 0, err
	}
	stdout := source.read

	// When the next appointment is due. Read once, here, so the two things that
	// care about it cannot disagree about when the hour turns.
	cutInAt, booked := s.scheduler.NextCutIn(itemCtx, s.channel.ID)

	// Get the incoming station connected BEFORE the boundary, whatever is
	// playing now.
	//
	// Every item needs this, not just the ones that will be interrupted: a
	// booked block that runs to its own end at 08:00 hands over to the next
	// one at 08:00, and if ffmpeg is only asked to dial at that moment the new
	// station's first two and a half seconds are silence.
	if booked {
		safego.Go(fmt.Sprintf("channel %s cut-in warm-up", s.channel.ID), func() {
			warm := timerUntil(cutInAt.Add(-cutInWarmLead), true)
			defer warm.Stop()
			select {
			case <-itemCtx.Done():
			case <-warm.C:
				s.warmCutIn(itemCtx, cutInAt)
			}
		})
	}

	// Preemption watchdog. Every preemptTick we re-ask the scheduler
	// what should be playing right now. If it returns a different
	// source than what we started this item with — only happens when
	// a higher-priority schedule rule has just become active — we
	// cancel itemCtx to kill the ffmpeg subprocess, the read loop
	// breaks on EOF, and the outer loop calls NextItem which picks
	// up the new rule. Items launched FROM a rule are exempt to avoid
	// infinitely re-preempting themselves — and they do not need it, since a
	// booked item is already capped at the end of its own slot.
	if !item.IsRuleDriven {
		safego.Go(fmt.Sprintf("channel %s preempt watchdog", s.channel.ID), func() {
			// Fire ON the boundary, not every fifteen seconds.
			//
			// A booked show is an appointment with a known time, so waiting to
			// notice it has arrived costs up to a full tick before ffmpeg is
			// even asked to start — and then the listener misses the top of the
			// programme. The slow ticker stays as a backstop for a plan that
			// changes mid-item; the timer is what makes the switch land.
			ticker := time.NewTicker(preemptTick)
			defer ticker.Stop()
			cutIn := timerUntil(cutInAt, booked)
			defer cutIn.Stop()
			for {
				select {
				case <-itemCtx.Done():
					return
				case <-cutIn.C:
					s.logger.Printf("channel %s: %q gives way at %s, its booked slot is due",
						s.channel.ID, item.Title, cutInAt.Format("15:04:05"))
					// Nothing is asked of the scheduler at this instant. The
					// boundary was known when the item started and the incoming
					// source was checked while warming; a question here would
					// only push the appointment past its own start time.
					s.flushListeners()
					itemCancel()
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
	buf := make([]byte, streamChunk)
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
			waitErr := source.wait()
			if readErr != nil {
				return written, readErr
			}
			if waitErr != nil && !errors.Is(itemCtx.Err(), context.DeadlineExceeded) && !errors.Is(itemCtx.Err(), context.Canceled) {
				return written, waitErr
			}
			return written, nil
		}
		if itemCtx.Err() != nil {
			source.stop()
			return written, itemCtx.Err()
		}
	}
}

// itemSource is one item's encoded audio, however it was started.
//
// A struct rather than a bare reader because the two ways an item can begin —
// spawned here, or adopted from a connection made before the boundary — have to
// be reaped differently, and the pump must not know which it got.
type itemSource struct {
	read io.Reader
	// wait collects the exit status once the audio has ended.
	wait func() error
	// stop kills the transcoder without waiting for it to be polite.
	stop func()
}

// openSource returns the audio for an item, adopting a connection warmed for
// this appointment when there is one.
func (s *channelStreamer) openSource(ctx context.Context, item PlaybackItem, args []string) (itemSource, error) {
	if warm := s.takeWarm(item); warm != nil {
		s.logger.Printf("channel %s: %q was already connected when its slot came round",
			s.channel.ID, item.Title)
		return warm.adopt(ctx), nil
	}
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return itemSource{}, fmt.Errorf("ffmpeg stdout: %w", err)
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg", s.channel.ID))
	if err := cmd.Start(); err != nil {
		return itemSource{}, fmt.Errorf("start ffmpeg: %w", err)
	}
	return itemSource{
		read: stdout,
		wait: cmd.Wait,
		stop: func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		},
	}, nil
}

// cutInWarmLead is how long before an appointment its source is connected.
//
// A live station measured on the real server takes about two and a half seconds
// from spawning ffmpeg to producing its first audio: DNS, TLS, the HTTP
// response, and then a probe whose data arrives at real-time speed. Spent after
// the boundary that is two and a half seconds of the news nobody hears, so it is
// spent before instead, and the audio it produces in the meantime is thrown
// away. Eight seconds leaves room for a slow station without warming things so
// early that the item playing is likely to end first.
const cutInWarmLead = 8 * time.Second

// warmSource is a station connected ahead of its appointment.
//
// Everything it produces before the boundary is DISCARDED, which is the whole
// point: a live stream has no beginning to preserve, so what should go out at
// 16:00:00 is what the station is broadcasting at 16:00:00 — not the eight
// seconds we spent getting ready to listen.
type warmSource struct {
	item PlaybackItem
	at   time.Time
	// out carries audio once the boundary has passed and this source is on air.
	out    chan []byte
	cancel context.CancelFunc
	// done closes when the pump goroutine has finished with the pipe.
	done chan struct{}
	wait func() error

	mu    sync.Mutex
	taken bool
}

// pump reads the warmed transcoder for as long as it lives: dropping what it
// produces before the boundary, delivering everything after it.
//
// Dropping is not waste, it is the point. The listener should hear the station
// as it is at the appointed second, not a recording of it starting from
// whenever we happened to connect.
func (w *warmSource) pump(ctx context.Context, stdout io.Reader) {
	defer close(w.done)
	defer close(w.out)
	buf := make([]byte, streamChunk)
	for {
		n, err := stdout.Read(buf)
		if n > 0 && w.forwarding() {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case w.out <- chunk:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// forwarding reports whether this source is on air yet, under the lock that
// makes the handover atomic: audio is either dropped or delivered, never both
// and never neither.
func (w *warmSource) forwarding() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.taken
}

// adopt puts the warmed source on air and returns its audio from this moment.
func (w *warmSource) adopt(ctx context.Context) itemSource {
	w.mu.Lock()
	w.taken = true
	w.mu.Unlock()

	// The item's own context now owns this transcoder: a skip, a MaxDuration or
	// a shutdown has to kill it, and it was started on a context of its own so
	// that it could outlive the item that warmed it.
	go func() {
		select {
		case <-ctx.Done():
			w.cancel()
		case <-w.done:
		}
	}()
	return itemSource{
		read: &channelReader{ch: w.out},
		wait: w.wait,
		stop: func() {
			w.cancel()
			<-w.done
			_ = w.wait()
		},
	}
}

// channelReader turns the warm pump's chunks into the io.Reader the item pump
// already knows how to drain.
type channelReader struct {
	ch   <-chan []byte
	rest []byte
}

func (r *channelReader) Read(p []byte) (int, error) {
	if len(r.rest) == 0 {
		chunk, ok := <-r.ch
		if !ok {
			return 0, io.EOF
		}
		r.rest = chunk
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// warmCutIn connects the source of an upcoming appointment so that when the
// boundary arrives there is nothing left to do but switch.
//
// Only for a live source. A file opens in milliseconds, and warming one would
// mean either holding its opening seconds (which then all go out at once, and
// every listener is that much further behind) or throwing away its first words.
func (s *channelStreamer) warmCutIn(ctx context.Context, at time.Time) {
	// An item that ends inside the warm-up window hands over to another, and
	// that one arms its own timers — which fire immediately, since the boundary
	// is already close. Re-connecting would throw away the connection that is
	// already open and pay for it a second time, this time with no room left to
	// pay in.
	if s.warmedFor(at) {
		return
	}
	next, err := s.scheduler.PeekItemAt(ctx, s.channel.ID, at)
	if err != nil || next.URL == "" || !next.Live {
		return
	}
	if !next.IsRuleDriven {
		return
	}

	codec, ext := codecArgs(s.channel.Codec)
	bitrate := s.channel.BitrateKbps
	if bitrate <= 0 {
		bitrate = defaultBitrateKbps
	}
	sampleRate := s.channel.SampleRateHz
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	// Rooted at the streamer, not at the item being warmed against: this
	// connection is meant to outlive the item playing now — that is the whole
	// idea — and dies with the channel or when it is reaped unused.
	warmCtx, cancel := context.WithCancel(s.baseCtx)
	args := transcodeArgs(next, codec, ext, bitrate, sampleRate, s.loudnessFilter(warmCtx, next))
	cmd := exec.CommandContext(warmCtx, s.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg (warming)", s.channel.ID))
	if err := cmd.Start(); err != nil {
		cancel()
		s.logger.Printf("channel %s: could not warm %q for its slot: %v", s.channel.ID, next.Title, err)
		return
	}

	// Reaped from more than one direction — the item that adopts it, the
	// streamer replacing it, a shutdown — and os/exec answers a second Wait
	// with an error about the first, which would be reported as the item having
	// failed. Asked once, answered the same way to everyone.
	var once sync.Once
	var waitErr error
	warm := &warmSource{
		item:   next,
		at:     at,
		out:    make(chan []byte, listenerBuffer),
		cancel: cancel,
		done:   make(chan struct{}),
		wait: func() error {
			once.Do(func() { waitErr = cmd.Wait() })
			return waitErr
		},
	}
	safego.Go(fmt.Sprintf("channel %s warm pump", s.channel.ID), func() {
		warm.pump(warmCtx, stdout)
	})

	s.setWarm(warm)
	s.logger.Printf("channel %s: connecting %q now, on air at %s",
		s.channel.ID, next.Title, at.Format("15:04:05"))
}

// warmedFor reports whether a live connection is already open and healthy for
// the appointment at this moment.
func (s *channelStreamer) warmedFor(at time.Time) bool {
	s.warmMu.Lock()
	warm := s.warm
	s.warmMu.Unlock()
	if warm == nil || !warm.at.Equal(at) {
		return false
	}
	// A source whose ffmpeg has already exited is not warm, it is a corpse
	// holding the slot open.
	select {
	case <-warm.done:
		return false
	default:
		return true
	}
}

// setWarm stores a warmed source, discarding any it replaces.
func (s *channelStreamer) setWarm(warm *warmSource) {
	s.warmMu.Lock()
	previous := s.warm
	s.warm = warm
	s.warmMu.Unlock()
	discardWarm(previous)
}

// takeWarm hands over the warmed source if it is the one now going to air.
//
// A mismatch is not a fault — something short can still come and go in the
// seconds before a boundary, and the station is entitled to change its mind —
// so a connection whose own moment has not arrived yet is left where it is.
// Only one that has been overtaken is killed, rather than left holding a socket
// open on somebody's station.
func (s *channelStreamer) takeWarm(item PlaybackItem) *warmSource {
	s.warmMu.Lock()
	warm := s.warm
	if warm != nil && warm.item.URL != item.URL && time.Now().Before(warm.at) {
		s.warmMu.Unlock()
		return nil
	}
	s.warm = nil
	s.warmMu.Unlock()
	if warm == nil {
		return nil
	}
	if warm.item.URL != item.URL {
		discardWarm(warm)
		return nil
	}
	// A connection that died during the warm-up window would hand the item pump
	// an immediate EOF, and an item that produces no audio is treated as a dead
	// source: the ref suppressed, the source stepped off. A station that blinked
	// while we were waiting for its hour would take itself off the air. Better
	// to find out by dialling again — that costs the two and a half seconds this
	// was avoiding, and only when something has actually gone wrong.
	select {
	case <-warm.done:
		s.logger.Printf("channel %s: the connection warmed for %q did not survive to its slot; dialling again",
			s.channel.ID, item.Title)
		discardWarm(warm)
		return nil
	default:
	}
	return warm
}

// dropWarm reaps a connection nothing is going to use.
func (s *channelStreamer) dropWarm() {
	s.warmMu.Lock()
	warm := s.warm
	s.warm = nil
	s.warmMu.Unlock()
	discardWarm(warm)
}

func discardWarm(warm *warmSource) {
	if warm == nil {
		return
	}
	warm.cancel()
	<-warm.done
	_ = warm.wait()
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
	// Levelling and the boundary fade are one filtergraph — ffmpeg takes a
	// single -af, and a second one silently replaces the first, which would
	// leave a faded item unlevelled or a levelled item cut dead.
	if filters := audioFilters(item, loudnessFilter); filters != "" {
		args = append(args, "-af", filters)
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

// audioFilters combines everything that has an opinion about this item's audio
// into the one filtergraph ffmpeg accepts.
//
// The fade comes last: it is a statement about the end of the item, and running
// it before a limiter would let the limiter pull the tail back up.
func audioFilters(item PlaybackItem, loudnessFilter string) string {
	filters := []string{}
	if loudnessFilter != "" {
		filters = append(filters, loudnessFilter)
	}
	if fade := fadeFilter(item); fade != "" {
		filters = append(filters, fade)
	}
	return strings.Join(filters, ",")
}

// fadeFilter is the fade that runs into an item's boundary, or "" for the
// ordinary case of an item allowed to finish.
//
// Anchored on MaxDuration rather than on the item's own length, because the
// whole reason this item is playing is that its own length does not fit: the
// clock decides when it ends. An item that turns out to be shorter than the gap
// simply finishes before the fade is reached, which is what should happen.
func fadeFilter(item PlaybackItem) string {
	if item.FadeOut <= 0 || item.MaxDuration <= 0 {
		return ""
	}
	fade := item.FadeOut
	if fade > item.MaxDuration {
		fade = item.MaxDuration
	}
	start := (item.MaxDuration - fade).Seconds()
	if start < 0 {
		start = 0
	}
	return fmt.Sprintf("afade=t=out:st=%.2f:d=%.2f", start, fade.Seconds())
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
	//
	// Never for something out of a bag, where forgetting is the far worse
	// error. A shuffled source has no freshness to burn and no rerun queue to
	// sink to the back of: the play log IS its queue, the one record that this
	// song has had its turn. Delete the row and the song is not merely fresh
	// again, it is UNPLAYED — indistinguishable from the tracks that have never
	// come round, top of the eligible pile, every pick, for ever. All that
	// holds it back is the 45-minute skip window, so it returns three times an
	// hour; and each return is a slot the rest of the playlist does not get.
	//
	// Which makes skipping a song the one action guaranteed to make you hear it
	// more. Measured on a 300-song playlist over two days: the two tracks the
	// listener skipped were served thirteen and twelve times while everything
	// else managed two or three. "I keep hearing the same songs and never the
	// rest of my playlist" is this, and it gets worse every time the button is
	// pressed.
	//
	// A skip is not evidence that nothing aired. It is the one piece of
	// evidence that somebody was listening — which is exactly how creditSkip
	// already reads it when settling an obligation.
	s.currentMu.RLock()
	logID, startedAt, item := s.currentLog, s.currentAt, s.current
	s.currentMu.RUnlock()
	shuffled := item != nil && item.Shuffled
	if logID != "" && !shuffled && time.Since(startedAt) < countsAsAired {
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

// timerUntil fires at a moment, or never when there is no moment to fire at.
//
// "Never" is a day away rather than a nil timer so the select that reads it
// needs no special case: a channel with nothing booked simply has a branch that
// does not come up, and the ticker remains its backstop.
func timerUntil(at time.Time, booked bool) *time.Timer {
	if !booked {
		return time.NewTimer(24 * time.Hour)
	}
	wait := time.Until(at)
	if wait < 0 {
		wait = 0
	}
	return time.NewTimer(wait)
}

// priorProgramState reads where the cycle stands, for undoing a failed pick.
func (s *channelStreamer) priorProgramState(ctx context.Context) ProgramState {
	if s.deps.DB == nil {
		return ProgramState{}
	}
	state, err := LoadProgramState(ctx, s.deps.DB, s.channel.ID)
	if err != nil {
		return ProgramState{}
	}
	return state
}

// rewindProgramState puts the cycle back after an item that never aired.
func (s *channelStreamer) rewindProgramState(ctx context.Context, state ProgramState) {
	if s.deps.DB == nil || state.BlockID == "" {
		return
	}
	if err := SaveProgramState(ctx, s.deps.DB, s.channel.ID, state); err != nil {
		s.logger.Printf("channel %s: could not rewind the programme state: %v", s.channel.ID, err)
	}
}

// deadSourceAfter is how many consecutive unplayable items from a source it
// takes before the station stops asking that source for anything.
const deadSourceAfter = 3

// skipRegistry is the station's suppression list, if this streamer has one.
func (s *channelStreamer) skipRegistry() *SkipRegistry {
	if s.scheduler == nil {
		return nil
	}
	return s.scheduler.deps.Skips
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
