package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/bouliehaan/samo-server/internal/safego"
)

// The audio stage of a streamer: the decoders that turn items into PCM, the
// mixer that shapes and paces them, and the one encoder that turns the result
// back into the channel's output format. See mixer.go for why the stream is
// built this way.

// mixerRun is one life of the mixer and its encoder, from the first item of a
// loop to the loop's end.
type mixerRun struct {
	mixer  *mixer
	cancel context.CancelFunc
	done   chan struct{}

	encMu      sync.Mutex
	enc        *encoderProc
	encRetryAt time.Time
}

// encoderRetry is how long the mixer waits before respawning an encoder that
// died. Frames in the meantime are dropped, which is the honest outcome: the
// listener hears a gap where the encoder was not, rather than a burst when it
// comes back.
const encoderRetry = time.Second

// encoderProc is the long-lived ffmpeg encoding the mixed PCM.
type encoderProc struct {
	stdin io.WriteCloser
	done  chan struct{}
	stop  func()
}

// ensureMixer returns the running mixer, starting one when none is.
//
// Rooted at the streamer's base context rather than the loop's, so an item
// played directly (a test, a preview) has a stage to play on; the loop stops
// it on its way out and stopAndWait stops it for everybody else.
func (s *channelStreamer) ensureMixer() *mixerRun {
	s.mixMu.Lock()
	defer s.mixMu.Unlock()
	if s.mix != nil {
		select {
		case <-s.mix.done:
		default:
			return s.mix
		}
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	run := &mixerRun{cancel: cancel, done: make(chan struct{})}
	run.mixer = newMixer(s.sampleRate(), func(frame []byte) error {
		return s.encodeFrame(ctx, run, frame)
	}, func(format string, args ...any) {
		s.logger.Printf("channel %s: "+format, append([]any{s.channel.ID}, args...)...)
	})
	safego.Go(fmt.Sprintf("channel %s mixer", s.channel.ID), func() {
		defer close(run.done)
		run.mixer.run(ctx)
		run.mixer.stop()
		run.stopEncoder()
	})
	s.mix = run
	return run
}

// currentMixer is the running mixer, or nil.
func (s *channelStreamer) currentMixer() *mixerRun {
	s.mixMu.Lock()
	defer s.mixMu.Unlock()
	if s.mix == nil {
		return nil
	}
	select {
	case <-s.mix.done:
		return nil
	default:
		return s.mix
	}
}

// stopMixer ends the mixer and its encoder and waits for both.
func (s *channelStreamer) stopMixer() {
	s.mixMu.Lock()
	run := s.mix
	s.mix = nil
	s.mixMu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	<-run.done
}

// sampleRate is the PCM rate the whole stage runs at.
func (s *channelStreamer) sampleRate() int {
	if s.channel.SampleRateHz > 0 {
		return s.channel.SampleRateHz
	}
	return 44100
}

// encodeFrame hands one mixed frame to the encoder, spawning or respawning it
// as needed.
func (s *channelStreamer) encodeFrame(ctx context.Context, run *mixerRun, frame []byte) error {
	if s.ffmpeg == "" {
		// Nothing to encode with: the mixer keeps time and the audio goes
		// nowhere, which is what a streamer with no transcoder has always
		// meant.
		return nil
	}
	run.encMu.Lock()
	defer run.encMu.Unlock()
	if run.enc != nil {
		select {
		case <-run.enc.done:
			run.enc = nil
		default:
		}
	}
	if run.enc == nil {
		if time.Now().Before(run.encRetryAt) {
			return errEncoderGone
		}
		enc, err := s.startEncoder(ctx)
		if err != nil {
			run.encRetryAt = time.Now().Add(encoderRetry)
			return err
		}
		run.enc = enc
	}
	if _, err := run.enc.stdin.Write(frame); err != nil {
		run.enc.stop()
		run.enc = nil
		run.encRetryAt = time.Now().Add(encoderRetry)
		return fmt.Errorf("encoder: %w", err)
	}
	return nil
}

// stopEncoder ends the encoder, if one is running.
func (run *mixerRun) stopEncoder() {
	run.encMu.Lock()
	enc := run.enc
	run.enc = nil
	run.encMu.Unlock()
	if enc != nil {
		enc.stop()
	}
}

// startEncoder spawns the encoder and pumps its output to the listeners.
func (s *channelStreamer) startEncoder(ctx context.Context) (*encoderProc, error) {
	codec, ext := codecArgs(s.channel.Codec)
	bitrate := s.channel.BitrateKbps
	if bitrate <= 0 {
		bitrate = defaultBitrateKbps
	}
	cmd := exec.CommandContext(ctx, s.ffmpeg, encodeArgs(codec, ext, bitrate, s.sampleRate())...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("encoder stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("encoder stdout: %w", err)
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg (encoder)", s.channel.ID))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start encoder: %w", err)
	}
	done := make(chan struct{})
	var once sync.Once
	wait := func() { once.Do(func() { _ = cmd.Wait() }) }
	safego.Go(fmt.Sprintf("channel %s encoder pump", s.channel.ID), func() {
		defer close(done)
		buf := make([]byte, streamChunk)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				s.broadcast(buf[:n])
			}
			if err != nil {
				break
			}
		}
		wait()
	})
	return &encoderProc{
		stdin: stdin,
		done:  done,
		stop: func() {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			<-done
		},
	}, nil
}

// encodeArgs is the encoder's command line: raw PCM in, the channel's format
// out, flushed packet by packet so nothing sits in a buffer between the mixer
// and the listener.
func encodeArgs(codec, ext string, bitrate, sampleRate int) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "error",
		"-nostdin",
		"-analyzeduration", "0",
		"-f", "s16le",
		"-ar", strconv.Itoa(sampleRate),
		"-ac", strconv.Itoa(mixChannels),
		"-i", "pipe:0",
		"-ac", strconv.Itoa(mixChannels),
		"-ar", strconv.Itoa(sampleRate),
		"-b:a", strconv.Itoa(bitrate) + "k",
		"-c:a", codec,
		"-f", ext,
		"-flush_packets", "1",
		"pipe:1",
	}
}

// decodeArgs is a decoder's command line: the item in, levelled, raw PCM out
// at the stage's rate.
//
// Argument order is not cosmetic — ffmpeg reads options positionally relative
// to -i — which is why this is a function a test can look at.
func decodeArgs(item PlaybackItem, sampleRate int, loudnessFilter string) []string {
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
		// seconds of silence before the first byte reaches a listener.
		args = append(args,
			"-analyzeduration", "3000000",
			"-probesize", "1000000",
		)
	}
	// `-re` paces a file at real time, which with the ring's backpressure is
	// what keeps the decoder a small step ahead of the clock rather than a
	// whole episode ahead. A live stream already arrives at real time; pacing
	// it again would only add the burst it sends on connect as permanent lag.
	if !item.Live {
		args = append(args, "-re")
	}
	args = append(args, "-i", item.URL, "-vn")
	if loudnessFilter != "" {
		args = append(args, "-af", loudnessFilter)
	}
	return append(args,
		"-ac", strconv.Itoa(mixChannels),
		"-ar", strconv.Itoa(sampleRate),
		"-f", "s16le",
		"pipe:1",
	)
}

// ringBytes is the ring depth, in bytes, for a duration at the stage's rate.
func ringBytes(sampleRate int, depth time.Duration) int {
	return int(float64(sampleRate)*depth.Seconds()) * mixChannels * mixBytesPerSample
}

// spawnDecoder starts an item's decoder and the pump that feeds its ring.
//
// The decoder is rooted at the streamer, not at the item: an item that has
// been cut still needs its decoder for as long as its fade-out runs, and the
// mixer releases it once the source is off air.
func (s *channelStreamer) spawnDecoder(item PlaybackItem, discard bool) (*pcmSource, error) {
	if s.ffmpeg == "" {
		return nil, errors.New("ffmpeg path not configured")
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	args := decodeArgs(item, s.sampleRate(), s.loudnessFilter(ctx, item))
	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg stdout: %w", err)
	}
	cmd.Stderr = newPrefixWriter(s.logger, fmt.Sprintf("channel %s ffmpeg", s.channel.ID))
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	depth := ringDepthFile
	if item.Live {
		depth = ringDepthLive
	}
	ring := newPCMRing(ringBytes(s.sampleRate(), depth), item.Live)
	ring.discard = discard

	// Reaped from more than one direction — the pump on its way out, the mixer
	// releasing the source, the streamer discarding a warmed one — and os/exec
	// answers a second Wait with an error about the first. Asked once, answered
	// the same way to everyone.
	var once sync.Once
	var waitErr error
	wait := func() error {
		once.Do(func() { waitErr = cmd.Wait() })
		return waitErr
	}
	src := newPCMSource(item.Title, ring, func() {
		cancel()
		// A pump blocked on a full ring nobody is draining has to be let go
		// too, or the decoder is dead and the goroutine is not.
		ring.close()
		_ = wait()
	})
	safego.Go(fmt.Sprintf("channel %s decoder pump", s.channel.ID), func() {
		src.pump(ctx, stdout, wait)
	})
	return src, nil
}

// openSource returns the audio for an item, adopting a source warmed for this
// appointment when there is one.
func (s *channelStreamer) openSource(item PlaybackItem) (*pcmSource, error) {
	if src := s.takeWarm(item); src != nil {
		s.logger.Printf("channel %s: %q was already connected when its slot came round",
			s.channel.ID, item.Title)
		return src, nil
	}
	return s.spawnDecoder(item, false)
}

// fadeOutFor is the fade an item runs into its planned end, when it has one.
func fadeOutFor(item PlaybackItem) time.Duration {
	if item.FadeOut > 0 {
		return item.FadeOut
	}
	return fadeOutDefault
}

// cutInWarmLead is how long before an appointment its source is connected.
//
// A live station measured on the real server takes about two and a half seconds
// from spawning ffmpeg to producing its first audio: DNS, TLS, the HTTP
// response, and then a probe whose data arrives at real-time speed. Spent after
// the boundary that is two and a half seconds of the news nobody hears, so it is
// spent before instead, and the audio it produces in the meantime is thrown
// away. Eight seconds leaves room for a slow station without warming things so
// early that the item playing is likely to end first — and comfortably covers
// the crossfade, which begins three seconds out.
const cutInWarmLead = 8 * time.Second

// warmSource is a station connected ahead of its appointment.
//
// Everything it produces before it goes to air is DISCARDED, which is the whole
// point: a live stream has no beginning to preserve, so what should go out at
// 16:00:00 is what the station is broadcasting at 16:00:00 — not the eight
// seconds we spent getting ready to listen.
type warmSource struct {
	item PlaybackItem
	at   time.Time
	src  *pcmSource
}

// alive reports whether the warmed decoder is still running.
func (w *warmSource) alive() bool {
	select {
	case <-w.src.decoderDone:
		return false
	default:
		return true
	}
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
	src, err := s.spawnDecoder(next, true)
	if err != nil {
		s.logger.Printf("channel %s: could not warm %q for its slot: %v", s.channel.ID, next.Title, err)
		return
	}
	s.setWarm(&warmSource{item: next, at: at, src: src})
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
	// A source whose decoder has already exited is not warm, it is a corpse
	// holding the slot open.
	return warm.alive()
}

// warmReadyFor is the warmed source for an appointment, if it is alive and has
// audio to give — the two things a crossfade needs from it.
func (s *channelStreamer) warmReadyFor(at time.Time) *warmSource {
	s.warmMu.Lock()
	warm := s.warm
	s.warmMu.Unlock()
	if warm == nil || !warm.at.Equal(at) || !warm.alive() || warm.src.decoded.Load() == 0 {
		return nil
	}
	return warm
}

// setWarm stores a warmed source, discarding any it replaces.
func (s *channelStreamer) setWarm(warm *warmSource) {
	s.warmMu.Lock()
	previous := s.warm
	s.warm = warm
	s.warmMu.Unlock()
	s.discardWarm(previous)
}

// takeWarm hands over the warmed source if it is the one now going to air.
//
// A mismatch is not a fault — something short can still come and go in the
// seconds before a boundary, and the station is entitled to change its mind —
// so a connection whose own moment has not arrived yet is left where it is.
// Only one that has been overtaken is killed, rather than left holding a socket
// open on somebody's station.
func (s *channelStreamer) takeWarm(item PlaybackItem) *pcmSource {
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
		s.discardWarm(warm)
		return nil
	}
	// A connection that died during the warm-up window would hand the mixer
	// an immediate end, and an item that produces no audio is treated as a
	// dead source: the ref suppressed, the source stepped off. A station that
	// blinked while we were waiting for its hour would take itself off the
	// air. Better to find out by dialling again — that costs the two and a
	// half seconds this was avoiding, and only when something has actually
	// gone wrong.
	if !warm.alive() {
		s.logger.Printf("channel %s: the connection warmed for %q did not survive to its slot; dialling again",
			s.channel.ID, item.Title)
		s.discardWarm(warm)
		return nil
	}
	return warm.src
}

// dropWarm reaps a connection nothing is going to use.
func (s *channelStreamer) dropWarm() {
	s.warmMu.Lock()
	warm := s.warm
	s.warm = nil
	s.warmMu.Unlock()
	s.discardWarm(warm)
}

// discardWarm lets a warmed source go: faded out if it had already been put on
// air for its crossfade, released outright otherwise.
func (s *channelStreamer) discardWarm(warm *warmSource) {
	if warm == nil {
		return
	}
	if run := s.currentMixer(); run != nil && run.mixer.onAir(warm.src) {
		run.mixer.retire(warm.src, fadeOutDefault)
		return
	}
	warm.src.release()
}

// beginCutIn starts the handover to a booked slot, `crossfadeCutIn` before the
// slot begins, so the item on air is fully down and the slot fully up on the
// second.
//
// With the slot's station already warmed and producing, the two overlap: the
// station comes up as the item goes down. Without one — a file-backed show,
// or a station that did not answer in time — the item on air is faded to
// silence by the boundary and whatever the decision picks at the boundary
// fades in after it.
func (s *channelStreamer) beginCutIn(run *mixerRun, playing *pcmSource, cutInAt time.Time) {
	if warm := s.warmReadyFor(cutInAt); warm != nil {
		s.logger.Printf("channel %s: crossfading to %q for %s",
			s.channel.ID, warm.item.Title, cutInAt.Format("15:04:05"))
		run.mixer.crossfade(warm.src, crossfadeCutIn)
		return
	}
	run.mixer.retire(playing, crossfadeCutIn)
}
