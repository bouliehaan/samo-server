package channels

import (
	"context"
	"errors"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// Radio runs to the second, and his station did not.
//
// Every one of these reproduces a moment off the real server's decision record
// or its play log, where a booked slot went to air at a time nobody asked for.

// 18:29:12, from the record, verbatim:
//
//	"nothing fitted the gap in front of it, so it starts early"
//	"started the booked slot early — nothing the station owns fits in the 48s
//	 before it"
//
// KRCC is booked at 18:30 and went out at 18:29:12 — and on the two days
// before, at 18:29:06. Same story at 16:00 (15:59:07, 15:59:03, 15:59:04) and
// at 22:00 (21:59:04, 21:59:07). The gap in front of an appointment closes to
// less than the shortest thing the station owns, every single time, because the
// item before it was chosen to fit — so the appointment was dragged forward to
// meet the silence rather than the silence being filled.
func TestABookedBlockDoesNotStartEarlyWhenNothingFitsTheGap(t *testing.T) {
	// 48 seconds to the 18:30 KRCC slot, exactly as it happened.
	now := time.Date(2026, 8, 13, 18, 29, 12, 0, time.UTC)

	talk := podcastSource("pod1", "A Show", "p1")
	episodes := []catalog.PodcastEpisode{}
	for index := 0; index < 6; index++ {
		episodes = append(episodes, episode("e"+strconv.Itoa(index),
			"An episode "+strconv.Itoa(index), now.AddDate(0, 0, -30-index), 45))
	}
	// Nothing anywhere near 48 seconds long — his shortest music is minutes.
	songs := []catalog.MusicTrack{}
	for index := 0; index < 30; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index%15), 150+index))
	}

	plan := boundaryPlan()
	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p1": episodes},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, plan, []Source{talk, musicSource("mus1", "House", "pl1")}, cat, now)

	item, decision := s.decide()
	if decision.BlockID == "krcc" {
		t.Fatalf("KRCC went on air at 18:29:12 instead of 18:30:00:\n%s", decision.Explain())
	}
	if item.Category != "music" {
		t.Fatalf("expected the 48 seconds to be held by the underrun pool, got %q (%s)",
			item.Title, item.Category)
	}
	// Held, not overrun: the filler ends ON the boundary, and is faded rather
	// than cut dead, because it was picked knowing the clock would take it.
	if item.MaxDuration != 48*time.Second {
		t.Fatalf("the filler runs %s; the gap is 48s and the slot starts at 18:30", item.MaxDuration)
	}
	if item.FadeOut <= 0 || item.FadeOut > item.MaxDuration {
		t.Fatalf("a filler cut by the clock must be faded out, got %s", item.FadeOut)
	}
}

// The other half of the same rule: a gap too small to hold anything.
//
// Filling four seconds means four seconds of a song, most of it fade — a fault
// you can hear, in exchange for a start time nobody can. Below the threshold
// the appointment still comes forward, and that is the right answer.
func TestAGapTooSmallToFillStillStartsTheBookedBlock(t *testing.T) {
	now := time.Date(2026, 8, 13, 18, 29, 56, 0, time.UTC)

	talk := podcastSource("pod1", "A Show", "p1")
	episodes := []catalog.PodcastEpisode{
		episode("e1", "An episode", now.AddDate(0, 0, -30), 45),
		episode("e2", "Another episode", now.AddDate(0, 0, -33), 52),
	}
	songs := []catalog.MusicTrack{}
	for index := 0; index < 20; index++ {
		songs = append(songs, track("t"+strconv.Itoa(index), "Song "+strconv.Itoa(index),
			"Artist "+strconv.Itoa(index), 200))
	}

	cat := &stubCatalog{
		episodes:  map[string][]catalog.PodcastEpisode{"p1": episodes},
		playlists: map[string][]catalog.MusicTrack{"pl1": songs},
	}
	s := newStation(t, boundaryPlan(), []Source{talk, musicSource("mus1", "House", "pl1")}, cat, now)

	_, decision := s.decide()
	if decision.BlockID != "krcc" {
		t.Fatalf("four seconds is not worth filling; expected the slot to open, got %q:\n%s",
			decision.BlockID, decision.Explain())
	}
}

// boundaryPlan is his station's shape at the point where it goes wrong: talk in
// rotation, music as the underrun pool, and a booked slot at 18:30.
func boundaryPlan() Plan {
	return Plan{
		Version:      PlanVersion,
		Categories:   []CategoryDef{{ID: "talk", Target: 1}, {ID: "music", Target: 0}},
		UnderrunPool: "music",
		Pools: []Pool{
			{ID: "talk", Match: &PoolMatch{Category: "talk"}},
			{ID: "music", Match: &PoolMatch{Category: "music"}},
		},
		Blocks: []Block{
			{ID: "general", Label: "General rotation", Default: true, Pools: []PoolRef{{Pool: "talk"}}},
			{ID: "krcc", Label: "KRCC",
				Enter: BlockEntry{At: "18:30", Days: "*", Hard: true, Start: StartImmediately},
				Exit:  BlockExit{At: "20:00"},
				Next:  "general",
				Pools: []PoolRef{{Pool: "music"}}},
		},
	}
}

// An item the clock will take fades into its boundary, and nothing else is
// ever faded out.
//
// The fade is anchored on MaxDuration, not on the item's own length: the reason
// this item is playing is that its length does not fit, so the clock decides
// where it ends. And it is the MIXER's fade, not an ffmpeg filter's — the
// levelling filter is the only thing the decoder is handed.
func TestOnlyAnItemTheClockWillTakeIsFaded(t *testing.T) {
	filler := PlaybackItem{URL: "/music/x.flac", MaxDuration: 48 * time.Second, FadeOut: 3 * time.Second}
	m := newMixer(44100, func([]byte) error { return nil }, func(string, ...any) {})
	src := constantSource("filler", 8000)
	m.play(src, 0, filler.MaxDuration, fadeOutFor(filler))
	if src.framesLeft != framesFor(48*time.Second) || src.fadeOutFrames != framesFor(3*time.Second) {
		t.Fatalf("planned end = %d frames with a %d-frame fade; want 2400 and 150", src.framesLeft, src.fadeOutFrames)
	}
	// Ordinary programming ends where its audio ends: no planned end at all.
	plain := constantSource("episode", 8000)
	m.play(plain, 0, 0, fadeOutDefault)
	if plain.framesLeft != -1 {
		t.Fatalf("an item nobody asked to cut has a planned end of %d frames", plain.framesLeft)
	}
	// A gap shorter than the fade is all fade rather than a click at the end.
	short := constantSource("short", 8000)
	m.play(short, 0, 2*time.Second, 3*time.Second)
	if short.fadeOutFrames != short.framesLeft {
		t.Fatalf("short-gap fade is %d frames over a %d-frame end", short.fadeOutFrames, short.framesLeft)
	}

	// The decoder is handed levelling and nothing else: one -af, and it is
	// the loudness filter, after -i.
	args := decodeArgs(filler, 44100, "volume=1.7dB,alimiter=limit=0.89")
	filters, inputAt, filterAt := 0, -1, -1
	for index, arg := range args {
		switch arg {
		case "-af":
			filters++
			filterAt = index
		case "-i":
			inputAt = index
		}
	}
	if filters != 1 || filterAt < inputAt || args[filterAt+1] != "volume=1.7dB,alimiter=limit=0.89" {
		t.Fatalf("decoder args carry %d -af flags: %v", filters, args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "afade") {
			t.Fatalf("the fade is the mixer's job, not a filter: %v", args)
		}
	}
}

// The fade itself: full level until the fade begins, down to silence on the
// last frame, and the item reported as ended by its play window.
func TestTheMixerFadesAnItemOutIntoItsPlayWindow(t *testing.T) {
	levels := []float64{}
	m := newMixer(44100, func(frame []byte) error {
		levels = append(levels, peakOf(frame))
		return nil
	}, func(string, ...any) {})
	src := constantSource("filler", 8000)
	m.play(src, 0, 10*mixFrame, 4*mixFrame)
	for i := 0; i < 12; i++ {
		if err := m.emit(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-src.finished:
	default:
		t.Fatal("the item did not end at its play window")
	}
	if !errors.Is(src.reason, context.DeadlineExceeded) {
		t.Fatalf("an item cut at its window ended with %v, want the deadline", src.reason)
	}
	// Frames 0..5 at full level, 6..9 falling, 10.. silence.
	if levels[5] < 7900 || levels[6] >= levels[5] || levels[9] >= levels[8] || levels[9] > 3000 || levels[10] != 0 {
		t.Fatalf("fade profile is wrong: %v", levels)
	}
}

// A crossfade: the outgoing item goes down as the incoming one comes up, the
// two overlap, and the incoming one is at full level when the fade ends.
func TestACrossfadeOverlapsAndLandsAtFullLevel(t *testing.T) {
	frames := [][]byte{}
	m := newMixer(44100, func(frame []byte) error {
		frames = append(frames, append([]byte(nil), frame...))
		return nil
	}, func(string, ...any) {})
	outgoing := constantSource("song", 8000)
	m.play(outgoing, 0, 0, 0)
	m.emit()
	incoming := constantSource("station", -8000) // opposite sign, so the two can be told apart
	m.crossfade(incoming, 4*mixFrame)
	for i := 0; i < 5; i++ {
		m.emit()
	}
	// During the fade both are present: the sum sits between the two levels
	// and never at either extreme.
	mid := sampleOf(frames[2])
	if mid <= -8000 || mid >= 8000 {
		t.Fatalf("mid-crossfade sample %d shows no overlap", mid)
	}
	// After it, only the incoming source, at full level.
	if got := sampleOf(frames[len(frames)-1]); got != -8000 {
		t.Fatalf("after the crossfade the incoming source is at %d, want -8000", got)
	}
	select {
	case <-outgoing.finished:
	default:
		t.Fatal("the outgoing item was not settled once its fade completed")
	}
	if !m.onAir(incoming) || m.onAir(outgoing) {
		t.Fatal("the incoming source should be on air alone")
	}
}

// A live station takes about two and a half seconds to answer — measured on the
// real server against KRCC, three runs, 2.55s/2.57s/2.63s from spawning ffmpeg
// to its first audio out. Spent after the boundary that is two and a half
// seconds of the news nobody hears, so the connection is made early and what it
// produces in the meantime is thrown away.
func TestAWarmedStationIsSilentUntilItsBoundaryThenGoesStraightOut(t *testing.T) {
	ring := newPCMRing(1<<16, true)
	ring.discard = true
	src := newPCMSource("KRCC", ring, nil)

	// Before the boundary: everything it produces is dropped.
	if !ring.push(context.Background(), []byte("the end of the previous hour")) {
		t.Fatal("push refused")
	}
	if ring.len() != 0 {
		t.Fatalf("%d bytes of pre-boundary audio were kept for the listener", ring.len())
	}

	// On the boundary: on air, from this instant.
	m := newMixer(44100, func([]byte) error { return nil }, func(string, ...any) {})
	m.crossfade(src, crossfadeCutIn)
	if !ring.push(context.Background(), []byte("live from NPR News")) {
		t.Fatal("push refused after the boundary")
	}
	got := make([]byte, 64)
	n := ring.pull(got)
	if string(got[:n]) != "live from NPR News" {
		t.Fatalf("expected the audio from after the boundary, got %q", got[:n])
	}
}

// And the streamer only adopts the connection it actually warmed.
//
// The station is entitled to change its mind in the seconds before a boundary;
// what it must not do is put the wrong source to air because one happened to be
// connected.
func TestAWarmedSourceIsOnlyAdoptedByTheItemItWasWarmedFor(t *testing.T) {
	streamer := quietStreamer(t)

	warmed := PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true}
	streamer.setWarm(liveWarm(warmed))

	// A different item: the warmed connection is dropped, not adopted, and this
	// streamer has no ffmpeg to fall back on — so the error IS the assertion.
	if _, err := streamer.openSource(PlaybackItem{URL: "http://elsewhere/live"}); err == nil {
		t.Fatal("a warmed source was adopted by an item that is not the one it was warmed for")
	}
	if streamer.takeWarm(warmed) != nil {
		t.Fatal("the unused connection should have been reaped, not left holding a socket")
	}

	// The item it WAS warmed for gets it, without going anywhere near ffmpeg.
	streamer.setWarm(liveWarm(warmed))
	if _, err := streamer.openSource(warmed); err != nil {
		t.Fatalf("the warmed connection was not adopted at its own boundary: %v", err)
	}
}

// A connection that dies before its slot must not be adopted.
//
// The mixer would read an immediate end from it, and an item that produces no
// audio is treated as a dead source — ref suppressed, source stepped off. A
// station that blinked while waiting for its hour would take itself off the air
// for the rest of the day.
func TestAWarmedSourceThatDiedIsNotAdopted(t *testing.T) {
	streamer := quietStreamer(t)
	item := PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true}

	dead := liveWarm(item)
	dead.src.release() // as a lost connection does: the decoder ends
	streamer.setWarm(dead)

	// No ffmpeg on this streamer, so "dialled again" surfaces as the start
	// error rather than a silent adoption of a corpse.
	if _, err := streamer.openSource(item); err == nil {
		t.Fatal("a dead connection was adopted; the booked show would have read as a dead source")
	}
}

// The crossfade only begins toward a warmed station that is actually
// producing; one that has not answered yet gets the item ducked to silence
// instead, and the decision at the boundary dials properly.
func TestTheCrossfadeWaitsForAStationThatIsProducing(t *testing.T) {
	streamer := quietStreamer(t)
	at := time.Now().Add(time.Minute)
	silent := liveWarm(PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true})
	silent.at = at
	streamer.setWarm(silent)
	if streamer.warmReadyFor(at) != nil {
		t.Fatal("a warmed source with no audio yet was offered for a crossfade")
	}
	silent.src.decoded.Store(4096)
	if streamer.warmReadyFor(at) == nil {
		t.Fatal("a warmed source that is producing was not offered for the crossfade")
	}
}

// liveWarm is a warmed source with nothing behind it — the shape the real one
// has, without a subprocess.
func liveWarm(item PlaybackItem) *warmSource {
	ring := newPCMRing(1<<16, true)
	ring.discard = true
	var once sync.Once
	src := newPCMSource(item.Title, ring, nil)
	src.release = func() { once.Do(func() { ring.close(); close(src.decoderDone) }) }
	return &warmSource{item: item, at: time.Now(), src: src}
}

// constantSource is a source whose audio is one sample value, for ever.
func constantSource(title string, level int16) *pcmSource {
	ring := newPCMRing(1<<20, true)
	src := newPCMSource(title, ring, nil)
	src.decoded.Store(1)
	buf := make([]byte, 1<<20)
	for i := 0; i < len(buf); i += 2 {
		buf[i] = byte(level)
		buf[i+1] = byte(level >> 8)
	}
	ring.push(context.Background(), buf)
	// Kept topped up by the pull: a ring this size holds several seconds of
	// frames, more than any test here consumes.
	return src
}

// peakOf is the largest sample magnitude in a frame.
func peakOf(frame []byte) float64 {
	peak := 0.0
	for i := 0; i+1 < len(frame); i += 2 {
		sample := float64(int16(uint16(frame[i]) | uint16(frame[i+1])<<8))
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	return peak
}

// sampleOf is the first sample of a frame.
func sampleOf(frame []byte) int {
	return int(int16(uint16(frame[0]) | uint16(frame[1])<<8))
}

// The same handover against a real decoder and a real pipe.
//
// The parts that only exist once there is a subprocess: audio flowing before
// anybody wants it, a pipe handed from the warming goroutine to the mixer, and
// an exit status that more than one path will ask for — os/exec answers a
// second Wait with a complaint about the first, and reporting that as the item
// having failed would make the station drop the show it just started.
func TestAdoptingARealDecoderYieldsAudioAndOneExitStatus(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on this machine")
	}
	wav := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-ac", "2", "-ar", "44100", wav).CombinedOutput(); err != nil {
		t.Fatalf("could not make a test tone: %v\n%s", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamer := newChannelStreamer(
		Channel{ID: "chan-test", Name: "Test", Codec: "mp3"},
		Dependencies{}, NewScheduler(Dependencies{}),
		StreamerOptions{FFmpegPath: ffmpeg, Logger: log.New(io.Discard, "", 0), BaseContext: ctx},
		nil,
	)
	// Warmed: decoding, discarding. A file paced at real time stands in for
	// the station — a live source is not paced by ffmpeg, and a thirty-second
	// file read at full speed would be over before the boundary.
	src, err := streamer.spawnDecoder(PlaybackItem{URL: wav, Title: "A station"}, true)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if src.decoded.Load() == 0 {
		t.Fatal("the warmed decoder produced nothing")
	}
	if src.ring.len() != 0 {
		t.Fatalf("%d bytes of pre-boundary audio were kept for the listener", src.ring.len())
	}
	select {
	case <-src.decoderDone:
		t.Fatal("the warmed decoder died before its boundary")
	default:
	}

	// On air: audio flows into the ring from here.
	src.ring.keep()
	time.Sleep(300 * time.Millisecond)
	if src.ring.len() == 0 {
		t.Fatal("no audio after the handover")
	}

	// Ending it twice — the mixer releasing it, and the streamer reaping what
	// it thinks is still warm — must not manufacture an error or hang.
	src.release()
	src.release()
	if stored := src.decoderErr.Load(); stored != nil && !errors.Is(*stored, context.Canceled) &&
		!strings.Contains((*stored).Error(), "signal") {
		t.Fatalf("release reported %v", *stored)
	}
}

// The handover at a boundary, both ways: with the slot's station warmed and
// producing, the item on air crossfades into it; without, the item is ducked
// to silence by the boundary and nothing is put on air in its place.
func TestBeginCutInCrossfadesToAWarmedStationOrDucks(t *testing.T) {
	streamer := quietStreamer(t)
	run := streamer.ensureMixer()
	t.Cleanup(streamer.stopMixer)
	at := time.Now().Add(crossfadeCutIn)

	playing := constantSource("episode", 8000)
	run.mixer.play(playing, 0, 0, 0)

	// Nothing warmed: the episode goes out under the crossfade-length fade,
	// and the mixer has nothing new on air.
	streamer.beginCutIn(run, playing, at)
	if run.mixer.onAir(playing) && run.mixer.current == playing {
		t.Fatal("the item on air was not taken off for the boundary")
	}
	if run.mixer.current != nil {
		t.Fatalf("nothing should have come on air, got %q", run.mixer.current.title)
	}

	// Warmed and producing: the station comes on air under the crossfade
	// while the item goes out under it.
	playing = constantSource("episode", 8000)
	run.mixer.play(playing, 0, 0, 0)
	warm := liveWarm(PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true})
	warm.at = at
	warm.src.decoded.Store(4096)
	streamer.setWarm(warm)
	streamer.beginCutIn(run, playing, at)
	if run.mixer.current != warm.src {
		t.Fatal("the warmed station was not crossfaded on air")
	}
	if run.mixer.outgoing != playing {
		t.Fatal("the item on air was not faded out under the crossfade")
	}
	if warm.src.gain.frames != framesFor(crossfadeCutIn) || playing.gain.frames != framesFor(crossfadeCutIn) {
		t.Fatalf("crossfade lengths are %d in / %d out frames, want %d",
			warm.src.gain.frames, playing.gain.frames, framesFor(crossfadeCutIn))
	}
	// And the decision at the boundary adopts what is already on air.
	if src := streamer.takeWarm(warm.item); src != warm.src {
		t.Fatal("the station on air was not the one adopted at the boundary")
	}
}
