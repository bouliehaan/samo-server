package channels

import (
	"context"
	"io"
	"os/exec"
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

// A filler is faded onto the boundary, and nothing else is ever faded.
//
// The fade is anchored on MaxDuration, not on the item's own length: the reason
// this item is playing is that its length does not fit, so the clock decides
// where it ends.
func TestOnlyAnItemTheClockWillTakeIsFaded(t *testing.T) {
	filler := PlaybackItem{URL: "/music/x.flac", MaxDuration: 48 * time.Second, FadeOut: 3 * time.Second}
	if got := fadeFilter(filler); got != "afade=t=out:st=45.00:d=3.00" {
		t.Fatalf("fade filter is %q", got)
	}
	// Ordinary programming ends where its audio ends.
	if got := fadeFilter(PlaybackItem{URL: "/music/x.flac", MaxDuration: 48 * time.Second}); got != "" {
		t.Fatalf("an item nobody asked to cut must not be faded, got %q", got)
	}
	// A gap shorter than the fade is all fade rather than a click at the end.
	short := PlaybackItem{MaxDuration: 2 * time.Second, FadeOut: 3 * time.Second}
	if got := fadeFilter(short); got != "afade=t=out:st=0.00:d=2.00" {
		t.Fatalf("short-gap fade is %q", got)
	}

	// Levelling and the fade have to arrive as ONE filtergraph. ffmpeg takes a
	// single -af and the last one wins, so passing two means either the item is
	// not levelled or it is not faded.
	combined := audioFilters(filler, "volume=1.7dB,alimiter=limit=0.89")
	if !strings.HasPrefix(combined, "volume=1.7dB") || !strings.Contains(combined, "afade=") {
		t.Fatalf("levelling and fade must be one graph, got %q", combined)
	}
	args := transcodeArgs(filler, "libmp3lame", "mp3", 192, 44100, "volume=1.7dB")
	filters := 0
	for _, arg := range args {
		if arg == "-af" {
			filters++
		}
	}
	if filters != 1 {
		t.Fatalf("ffmpeg was handed %d -af flags; it honours one", filters)
	}
}

// A live station takes about two and a half seconds to answer — measured on the
// real server against KRCC, three runs, 2.55s/2.57s/2.63s from spawning ffmpeg
// to its first audio out. Spent after the boundary that is two and a half
// seconds of the news nobody hears, so the connection is made early and what it
// produces in the meantime is thrown away.
func TestAWarmedStationIsSilentUntilItsBoundaryThenGoesStraightOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	warm := &warmSource{
		item:   PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true},
		at:     time.Now().Add(cutInWarmLead),
		out:    make(chan []byte, 8),
		cancel: cancel,
		done:   make(chan struct{}),
		wait:   func() error { return nil },
	}
	reader, writer := io.Pipe()
	go warm.pump(ctx, reader)

	// Before the boundary: everything it produces is dropped.
	if _, err := writer.Write([]byte("the end of the previous hour")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case chunk := <-warm.out:
		t.Fatalf("audio from before the boundary reached the listener: %q", chunk)
	case <-time.After(50 * time.Millisecond):
	}

	// On the boundary: on air, from this instant.
	source := warm.adopt(ctx)
	if _, err := writer.Write([]byte("live from NPR News")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := make([]byte, 64)
	n, err := source.read.Read(got)
	if err != nil {
		t.Fatalf("read after adoption: %v", err)
	}
	if string(got[:n]) != "live from NPR News" {
		t.Fatalf("expected the audio from after the boundary, got %q", got[:n])
	}
	writer.Close()
}

// And the streamer only adopts the connection it actually warmed.
//
// The station is entitled to change its mind in the seconds before a boundary;
// what it must not do is put the wrong source to air because one happened to be
// connected.
func TestAWarmedSourceIsOnlyAdoptedByTheItemItWasWarmedFor(t *testing.T) {
	streamer := quietStreamer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	warmed := PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true}
	streamer.setWarm(liveWarm(warmed))

	// A different item: the warmed connection is dropped, not adopted, and this
	// streamer has no ffmpeg to fall back on — so the error IS the assertion.
	if _, err := streamer.openSource(ctx, PlaybackItem{URL: "http://elsewhere/live"}, nil); err == nil {
		t.Fatal("a warmed source was adopted by an item that is not the one it was warmed for")
	}
	if streamer.takeWarm(warmed) != nil {
		t.Fatal("the unused connection should have been reaped, not left holding a socket")
	}

	// The item it WAS warmed for gets it, without going anywhere near ffmpeg.
	streamer.setWarm(liveWarm(warmed))
	if _, err := streamer.openSource(ctx, warmed, nil); err != nil {
		t.Fatalf("the warmed connection was not adopted at its own boundary: %v", err)
	}
}

// A connection that dies before its slot must not be adopted.
//
// The item pump reads an immediate EOF from it, and an item that produces no
// audio is treated as a dead source — ref suppressed, source stepped off. A
// station that blinked while waiting for its hour would take itself off the air
// for the rest of the day.
func TestAWarmedSourceThatDiedIsNotAdopted(t *testing.T) {
	streamer := quietStreamer(t)
	item := PlaybackItem{URL: "http://station/live", Title: "KRCC", Live: true}

	dead := liveWarm(item)
	dead.cancel() // as a lost connection does: the pump ends, the channel closes
	<-dead.done
	streamer.setWarm(dead)

	// No ffmpeg on this streamer, so "dialled again" surfaces as the start
	// error rather than a silent adoption of a corpse.
	if _, err := streamer.openSource(context.Background(), item, nil); err == nil {
		t.Fatal("a dead connection was adopted; the booked show would have read as a dead source")
	}
}

// liveWarm is a warmed source with nothing behind it, whose pump ends when it
// is cancelled — the shape the real one has, without a subprocess.
func liveWarm(item PlaybackItem) *warmSource {
	done := make(chan struct{})
	out := make(chan []byte, 1)
	var once sync.Once
	return &warmSource{
		item: item, at: time.Now(), out: out, done: done,
		cancel: func() { once.Do(func() { close(out); close(done) }) },
		wait:   func() error { return nil },
	}
}

// The same handover against a real transcoder and a real pipe.
//
// The parts that only exist once there is a subprocess: audio flowing before
// anybody wants it, a pipe handed from the warming goroutine to the item pump,
// and an exit status that more than one path will ask for — os/exec answers a
// second Wait with a complaint about the first, and reporting that as the item
// having failed would make the station drop the show it just started.
func TestAdoptingARealTranscoderYieldsAudioAndOneExitStatus(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on this machine")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	warmCtx, warmCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(warmCtx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-re",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30",
		"-ac", "2", "-ar", "44100", "-b:a", "192k", "-c:a", "libmp3lame", "-f", "mp3", "pipe:1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	var once sync.Once
	var waitErr error
	warm := &warmSource{
		item:   PlaybackItem{URL: "lavfi://sine", Title: "A station", Live: true},
		at:     time.Now().Add(500 * time.Millisecond),
		out:    make(chan []byte, listenerBuffer),
		cancel: warmCancel,
		done:   make(chan struct{}),
		wait:   func() error { once.Do(func() { waitErr = cmd.Wait() }); return waitErr },
	}
	go warm.pump(warmCtx, stdout)

	// Let it get going — this is the two and a half seconds the boundary is not
	// supposed to pay for — and check none of it has leaked out.
	time.Sleep(700 * time.Millisecond)
	if len(warm.out) != 0 {
		t.Fatalf("%d chunks of pre-boundary audio were queued for the listener", len(warm.out))
	}
	// Silence because it is being dropped, not because nothing is connected —
	// which is the failure this test would otherwise pass straight through.
	select {
	case <-warm.done:
		t.Fatal("the warmed transcoder died before its boundary")
	default:
	}

	source := warm.adopt(ctx)
	got := make([]byte, 4096)
	n, err := source.read.Read(got)
	if err != nil || n == 0 {
		t.Fatalf("no audio after the handover: %d bytes, %v", n, err)
	}

	// Ending it twice — the item pump on its way out, and the streamer reaping
	// what it thinks is still warm — must not manufacture an error.
	source.stop()
	if err := source.wait(); err != nil && !strings.Contains(err.Error(), "signal") {
		t.Fatalf("second wait reported %v", err)
	}
	discardWarm(warm)
}
