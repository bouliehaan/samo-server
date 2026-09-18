package channels

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// The mixer is the audio stage between the items and the listeners.
//
// Each item used to be its own ffmpeg, encoding straight to the output codec,
// and the listener stream was those encoded streams laid end to end. Nothing
// about that shape can put a fade between two items: by the time the audio is
// MP3 there is no level to move, only frames to copy. So a booked station cut
// in dead on the second, the tail of the item before it was chopped mid-word,
// and the join carried whatever the two encoders left at their edges — a Xing
// header, a half frame, an encoder warming up — which is the "hard cut with a
// burst" that made every cut-in jarring.
//
// Now every item is DECODED, to raw PCM, into a bounded ring. The mixer holds
// the clock: every twenty milliseconds it takes one frame from whatever is on
// air, shapes it through a gain envelope, sums it with whatever is on its way
// out, and hands the result to one long-lived encoder. Two things fall out of
// that which no amount of care at the encoded level could give:
//
//   - A fade is a multiplication. An item opens under a fade-in, closes under
//     a fade-out when its end is known in advance, and a booked show taking
//     over CROSSFADES — the outgoing item goes down as the incoming station
//     comes up, over the same three seconds, landing at full level exactly on
//     the second the schedule promised.
//
//   - The mixer paces the output at real time whatever the input does. A live
//     station bursts several seconds of audio at connect; that now fills the
//     source's ring and goes out at real time (or, past the ring's depth, is
//     dropped), instead of arriving at the listener all at once.
//
// One encoder for the life of the loop also means one continuous encoded
// stream: no header at every join, no frame torn in half, and silence — real,
// encoded silence — when there is nothing to play, so a client never sees the
// bytes simply stop.

const (
	// mixFrame is the mixer's tick: one frame of output per tick. Twenty
	// milliseconds is short enough that a fade is smooth (a three-second
	// crossfade is a hundred and fifty steps) and long enough that the Go
	// scheduler keeps up without effort.
	mixFrame = 20 * time.Millisecond
	// mixChannels and mixBytesPerSample describe the PCM the decoders produce
	// and the encoder consumes: 16-bit little-endian stereo.
	mixChannels       = 2
	mixBytesPerSample = 2

	// maxCatchUpFrames bounds how many frames the clock emits in one tick to
	// make up for a late one. A stall longer than this is absorbed as a small
	// slip rather than repaid as a burst — which would be the very thing the
	// mixer exists to prevent.
	maxCatchUpFrames = 10

	// fadeInAfterEnd is how an item opens after the previous one finished on
	// its own. Long enough to be heard as an opening rather than a click.
	fadeInAfterEnd = 1200 * time.Millisecond
	// fadeInAfterCut is how an item opens after a skip or a preemption. The
	// cut was deliberate; what follows should arrive promptly.
	fadeInAfterCut = 600 * time.Millisecond
	// crossfadeCutIn is how long a booked show takes to take over from what is
	// playing: the outgoing item is fully down and the station fully up on the
	// second the slot begins.
	crossfadeCutIn = 3 * time.Second
	// fadeOutSkip is the fade under a skip. The cut is meant to be heard — that
	// is what skip means — this only keeps it from being a click.
	fadeOutSkip = 300 * time.Millisecond
	// fadeOutDefault is the fade under any other cut: a play window closing,
	// the stall watchdog, a preemption the ticker caught.
	fadeOutDefault = 2 * time.Second

	// ringDepthFile is how much decoded audio a file-backed source may hold
	// ahead of the clock. The decoder is paced at real time (-re) and blocks
	// on a full ring, so this is only ever a small jitter buffer.
	ringDepthFile = 2 * time.Second
	// ringDepthLive is the same for a continuous source, which is NOT paced:
	// a station bursts on connect, and past this depth the oldest audio is
	// dropped so the relay stays close to live rather than accumulating the
	// burst as permanent delay.
	ringDepthLive = 4 * time.Second
)

// frameBytes is the size of one mixer frame at a sample rate.
func frameBytes(sampleRate int) int {
	samples := sampleRate * int(mixFrame) / int(time.Second)
	return samples * mixChannels * mixBytesPerSample
}

// pcmRing is a bounded FIFO of decoded audio between one decoder and the
// mixer.
//
// The decoder side pushes; the mixer side pulls without ever blocking, because
// the clock does not wait for anybody. What happens when the ring is full is
// the one policy decision here: a paced decoder (a file under -re) is made to
// wait, which is the backpressure that keeps it at real time; a live source is
// never made to wait — the station on the far end would drop a client that
// stopped reading — so its oldest audio is discarded instead.
type pcmRing struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	r, w int
	n    int

	// closed means no more audio is coming: the decoder has finished.
	closed bool
	// discard means audio pushed now is thrown away rather than kept — a
	// source connected ahead of its slot, whose audio before the boundary is
	// deliberately not what should go out AT the boundary.
	discard bool
	// dropOldest is the full-ring policy for a live source.
	dropOldest bool
}

func newPCMRing(capacity int, dropOldest bool) *pcmRing {
	ring := &pcmRing{buf: make([]byte, capacity), dropOldest: dropOldest}
	ring.cond = sync.NewCond(&ring.mu)
	return ring
}

// push appends audio, blocking while the ring is full unless the ring drops
// its oldest audio instead. Returns false once the ring is closed or the
// context is done.
func (r *pcmRing) push(ctx context.Context, data []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(data) > 0 {
		if r.closed || ctx.Err() != nil {
			return false
		}
		if r.discard {
			return true
		}
		free := len(r.buf) - r.n
		if free == 0 {
			if r.dropOldest {
				drop := len(data)
				if drop > len(r.buf) {
					drop = len(r.buf)
				}
				r.r = (r.r + drop) % len(r.buf)
				r.n -= drop
				continue
			}
			// Woken by a pull, a close, or the periodic nudge below, so a
			// cancelled context is noticed within a tick.
			r.cond.Wait()
			continue
		}
		chunk := len(data)
		if chunk > free {
			chunk = free
		}
		if chunk > len(r.buf) {
			chunk = len(r.buf)
		}
		first := copy(r.buf[r.w:], data[:chunk])
		if first < chunk {
			copy(r.buf, data[first:chunk])
		}
		r.w = (r.w + chunk) % len(r.buf)
		r.n += chunk
		data = data[chunk:]
	}
	return true
}

// pull copies up to len(dst) bytes out, never blocking, and reports how many.
func (r *pcmRing) pull(dst []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	take := len(dst)
	if take > r.n {
		take = r.n
	}
	if take == 0 {
		return 0
	}
	first := copy(dst[:take], r.buf[r.r:])
	if first < take {
		copy(dst[first:take], r.buf)
	}
	r.r = (r.r + take) % len(r.buf)
	r.n -= take
	r.cond.Broadcast()
	return take
}

// len is how much audio is waiting.
func (r *pcmRing) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// keep switches a discarding ring to keeping what is pushed from now on.
func (r *pcmRing) keep() {
	r.mu.Lock()
	r.discard = false
	r.mu.Unlock()
}

// close marks the end of the audio and wakes any blocked push.
func (r *pcmRing) close() {
	r.mu.Lock()
	r.closed = true
	r.cond.Broadcast()
	r.mu.Unlock()
}

// nudge wakes a push blocked on a full ring so it can re-check its context.
func (r *pcmRing) nudge() {
	r.mu.Lock()
	r.cond.Broadcast()
	r.mu.Unlock()
}

// pcmSource is one decoded item as the mixer sees it.
type pcmSource struct {
	title string
	ring  *pcmRing

	// decoded is how much audio the decoder has produced, in bytes. Zero at
	// the end means the item never made a sound.
	decoded atomic.Int64
	// lastByteAt is when the decoder last produced audio, for the stall
	// watchdog. Unix nanoseconds.
	lastByteAt atomic.Int64
	// decoderErr is how the decoder ended, once it has.
	decoderErr atomic.Pointer[error]
	// decoderDone closes when the decoder has finished and its output has
	// been read to the end.
	decoderDone chan struct{}

	// release kills the decoder. Called by the mixer once the source is off
	// air, not by the item that started it: an item fading out still needs
	// its decoder for as long as the fade runs.
	release func()

	// Mixer-owned state below; touched only under the mixer's lock.
	gain      fadeEnvelope
	fadingOut bool
	// framesLeft is the planned end: how many more frames may go out before
	// the source is over, or -1 for "until the audio ends".
	framesLeft int
	// fadeOutFrames is the fade to run into a planned end.
	fadeOutFrames int
	underruns     int

	finishOnce sync.Once
	finished   chan struct{}
	reason     error
}

func newPCMSource(title string, ring *pcmRing, release func()) *pcmSource {
	src := &pcmSource{
		title:       title,
		ring:        ring,
		decoderDone: make(chan struct{}),
		release:     release,
		framesLeft:  -1,
		finished:    make(chan struct{}),
	}
	src.lastByteAt.Store(time.Now().UnixNano())
	return src
}

// finish settles how the source ended, once.
func (s *pcmSource) finish(reason error) {
	s.finishOnce.Do(func() {
		s.reason = reason
		close(s.finished)
	})
}

// pump copies the decoder's output into the ring for as long as it produces
// any, then closes the ring.
func (s *pcmSource) pump(ctx context.Context, stdout io.Reader, wait func() error) {
	defer close(s.decoderDone)
	defer s.ring.close()
	buf := make([]byte, streamChunk)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			s.decoded.Add(int64(n))
			s.lastByteAt.Store(time.Now().UnixNano())
			if !s.ring.push(ctx, buf[:n]) {
				break
			}
		}
		if err != nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	// The exit status says whether the decoder finished or failed. A killed
	// decoder is the station's doing and carries the context's own error, so
	// the distinction the loop relies on — finished, cut, or broken — is
	// preserved. See playItem for how each is read.
	var reason error
	if ctxErr := ctx.Err(); ctxErr != nil {
		reason = ctxErr
	} else if wait != nil {
		reason = wait()
	}
	if reason != nil {
		s.decoderErr.Store(&reason)
	}
}

// fadeEnvelope is a gain moving from one level to another over a number of
// frames.
//
// Equal-power rather than linear: two sources crossfading under linear ramps
// dip audibly in the middle, where both are at half amplitude, and the sine
// and cosine halves keep the summed power constant. For a fade on its own the
// difference is a slightly more natural opening.
type fadeEnvelope struct {
	from, to float64
	frames   int
	index    int
}

func (f *fadeEnvelope) set(from, to float64, frames int) {
	f.from, f.to, f.frames, f.index = from, to, frames, 0
	if frames <= 0 {
		f.frames, f.index = 0, 0
	}
}

// step returns the gain for the frame about to go out and advances.
func (f *fadeEnvelope) step() float64 {
	if f.frames <= 0 || f.index >= f.frames {
		return f.to
	}
	t := float64(f.index+1) / float64(f.frames)
	f.index++
	var curve float64
	if f.to > f.from {
		curve = math.Sin(t * math.Pi / 2)
	} else {
		curve = 1 - math.Cos(t*math.Pi/2)
	}
	return f.from + (f.to-f.from)*curve
}

// current is the gain the envelope is at without advancing.
func (f *fadeEnvelope) current() float64 {
	if f.frames <= 0 || f.index >= f.frames {
		return f.to
	}
	if f.index == 0 {
		return f.from
	}
	t := float64(f.index) / float64(f.frames)
	var curve float64
	if f.to > f.from {
		curve = math.Sin(t * math.Pi / 2)
	} else {
		curve = 1 - math.Cos(t*math.Pi/2)
	}
	return f.from + (f.to-f.from)*curve
}

// done reports whether the envelope has reached its target.
func (f *fadeEnvelope) done() bool { return f.frames <= 0 || f.index >= f.frames }

// mixer holds the clock and mixes the sources on air into one PCM stream.
type mixer struct {
	sampleRate int
	frame      int
	// write delivers one frame of mixed PCM to the encoder.
	write func([]byte) error
	logf  func(format string, args ...any)

	mu       sync.Mutex
	current  *pcmSource
	outgoing *pcmSource

	// scratch buffers, reused per frame.
	acc    []int32
	take   []byte
	out    []byte
	frames atomic.Int64
}

func newMixer(sampleRate int, write func([]byte) error, logf func(string, ...any)) *mixer {
	frame := frameBytes(sampleRate)
	return &mixer{
		sampleRate: sampleRate,
		frame:      frame,
		write:      write,
		logf:       logf,
		acc:        make([]int32, frame/mixBytesPerSample),
		take:       make([]byte, frame),
		out:        make([]byte, frame),
	}
}

// framesFor is how many mixer frames a duration is, at least one for anything
// positive.
func framesFor(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	frames := int(d / mixFrame)
	if frames < 1 {
		frames = 1
	}
	return frames
}

// play puts a source on air under a fade-in, bounded by a planned end when it
// has one. Whatever was on air and not yet retired goes out under the default
// fade.
func (m *mixer) play(src *pcmSource, fadeIn, maxDuration, fadeOut time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == src {
		// Already on air — adopted while crossfading in for its slot.
		m.planEndLocked(src, maxDuration, fadeOut)
		return
	}
	if m.current != nil {
		m.retireLocked(m.current, fadeOutDefault)
	}
	src.ring.keep()
	src.gain.set(0, 1, framesFor(fadeIn))
	m.planEndLocked(src, maxDuration, fadeOut)
	m.current = src
}

// planEndLocked records how long a source may run and the fade into its end.
func (m *mixer) planEndLocked(src *pcmSource, maxDuration, fadeOut time.Duration) {
	if maxDuration <= 0 {
		src.framesLeft = -1
		src.fadeOutFrames = 0
		return
	}
	src.framesLeft = framesFor(maxDuration)
	src.fadeOutFrames = framesFor(fadeOut)
	if src.fadeOutFrames > src.framesLeft {
		src.fadeOutFrames = src.framesLeft
	}
}

// crossfade brings a source on air over `fade` while what is playing goes out
// over the same span, so the two overlap and the incoming one is at full
// level when the fade ends.
func (m *mixer) crossfade(src *pcmSource, fade time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == src {
		return
	}
	if m.current != nil {
		m.retireLocked(m.current, fade)
	}
	src.ring.keep()
	src.gain.set(0, 1, framesFor(fade))
	src.framesLeft = -1
	m.current = src
}

// retire takes a source off air under a fade. A source already on its way out
// keeps the fade it has.
func (m *mixer) retire(src *pcmSource, fade time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if src == m.current {
		m.retireLocked(src, fade)
	}
}

func (m *mixer) retireLocked(src *pcmSource, fade time.Duration) {
	if m.outgoing != nil && m.outgoing != src {
		m.dropLocked(m.outgoing, context.Canceled)
	}
	src.fadingOut = true
	src.gain.set(src.gain.current(), 0, framesFor(fade))
	m.outgoing = src
	if m.current == src {
		m.current = nil
	}
}

// dropLocked takes a source out of the mixer entirely and lets its decoder go.
func (m *mixer) dropLocked(src *pcmSource, reason error) {
	if m.current == src {
		m.current = nil
	}
	if m.outgoing == src {
		m.outgoing = nil
	}
	if src.underruns > 0 {
		// The decoder could not keep the ring ahead of the clock: a slow
		// upstream, a busy disk, a box short of CPU. Said once per item.
		m.logf("%q underran %d frame(s) (%s of silence)", src.title, src.underruns,
			(time.Duration(src.underruns) * mixFrame).Round(mixFrame))
	}
	src.finish(reason)
	if src.release != nil {
		go src.release()
	}
}

// stop takes everything off air at once, for shutdown.
func (m *mixer) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, src := range []*pcmSource{m.current, m.outgoing} {
		if src != nil {
			m.dropLocked(src, context.Canceled)
		}
	}
}

// onAir reports whether a source is currently being mixed, in or out.
func (m *mixer) onAir(src *pcmSource) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return src == m.current || src == m.outgoing
}

// run is the clock. It emits one frame per tick until the context ends,
// catching up a bounded number of frames when a tick lands late.
func (m *mixer) run(ctx context.Context) {
	ticker := time.NewTicker(mixFrame)
	defer ticker.Stop()
	start := time.Now()
	emitted := int64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		due := int64(time.Since(start) / mixFrame)
		if due-emitted > maxCatchUpFrames {
			emitted = due - maxCatchUpFrames
		}
		for emitted < due {
			if err := m.emit(); err != nil {
				// The encoder is gone; whoever owns it restarts it. Nothing
				// to do with the frame but drop it and keep time.
				m.logf("mixer: encoder write failed: %v", err)
			}
			emitted++
		}
	}
}

// emit mixes and writes one frame.
func (m *mixer) emit() error {
	for i := range m.acc {
		m.acc[i] = 0
	}
	m.mu.Lock()
	if out := m.outgoing; out != nil {
		m.mixInto(out, out.gain.step())
		if out.gain.done() {
			m.dropLocked(out, context.Canceled)
		}
	}
	if cur := m.current; cur != nil {
		gain := cur.gain.step()
		if cur.framesLeft >= 0 {
			// Into the planned end: start the fade so it completes as the
			// last frame goes out.
			if cur.framesLeft == cur.fadeOutFrames && cur.fadeOutFrames > 0 && !cur.fadingOut {
				cur.fadingOut = true
				cur.gain.set(cur.gain.current(), 0, cur.fadeOutFrames)
				gain = cur.gain.step()
			}
			cur.framesLeft--
		}
		got := m.mixInto(cur, gain)
		switch {
		case cur.framesLeft == 0:
			m.dropLocked(cur, context.DeadlineExceeded)
		case got == 0 && cur.ring.len() == 0 && m.decoderFinished(cur):
			// The audio has ended and every byte of it has gone out.
			reason := error(nil)
			if stored := cur.decoderErr.Load(); stored != nil {
				reason = *stored
			}
			m.dropLocked(cur, reason)
		}
	}
	m.mu.Unlock()
	m.frames.Add(1)

	// Clip and pack.
	for i, sample := range m.acc {
		if sample > math.MaxInt16 {
			sample = math.MaxInt16
		} else if sample < math.MinInt16 {
			sample = math.MinInt16
		}
		m.out[2*i] = byte(sample)
		m.out[2*i+1] = byte(sample >> 8)
	}
	return m.write(m.out)
}

// decoderFinished reports whether a source's decoder has ended.
func (m *mixer) decoderFinished(src *pcmSource) bool {
	select {
	case <-src.decoderDone:
		return true
	default:
		return false
	}
}

// mixInto adds one frame of a source, under a gain, into the accumulator.
// Short reads are padded with silence and counted as underruns.
func (m *mixer) mixInto(src *pcmSource, gain float64) int {
	got := src.ring.pull(m.take)
	if got < len(m.take) && !src.fadingOut && src.decoded.Load() > 0 && !m.decoderFinished(src) {
		src.underruns++
	}
	if gain <= 0 || got == 0 {
		return got
	}
	samples := got / mixBytesPerSample
	for i := 0; i < samples; i++ {
		sample := int16(uint16(m.take[2*i]) | uint16(m.take[2*i+1])<<8)
		m.acc[i] += int32(float64(sample) * gain)
	}
	return got
}

// errEncoderGone is what a write returns once the encoder has exited.
var errEncoderGone = errors.New("encoder is not running")
