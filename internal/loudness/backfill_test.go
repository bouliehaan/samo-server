package loudness

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// The first deploy measured nothing. The sweep was ordered by path, so it
// opened on 23-hour audiobooks and read them end to end, and 20,000 music
// files sat behind them. This drives the real sweep over real audio and
// checks both halves of that fix.
func TestBackfillMeasuresRealFilesShortestFirst(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	db := storagetest.Open(t)
	ctx := context.Background()
	dir := t.TempDir()

	// A stand-in for an audiobook: longer than the analysis cap, so it must be
	// sampled rather than read whole. Twenty minutes is enough to prove the
	// window without making the test slow to build.
	const longSeconds = 1200
	tone := func(name string, seconds int, trimDB int) string {
		t.Helper()
		path := filepath.Join(dir, name)
		build := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:duration=%d:sample_rate=44100", seconds),
			"-af", fmt.Sprintf("volume=%ddB", trimDB), "-ac", "2", "-b:a", "128k", path)
		if out, err := build.CombinedOutput(); err != nil {
			t.Skipf("could not synthesise %s: %v: %s", name, err, out)
		}
		return path
	}

	book := tone("book.mp3", longSeconds, -6)
	song := tone("song.mp3", 20, -12)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"lib-1", "Media", "music", dir); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	// Inserted book-first, exactly as ORDER BY path would have found them.
	for _, f := range []struct {
		id       string
		path     string
		duration int
	}{
		{"f-book", book, longSeconds},
		{"f-song", song, 20},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO media_files (id, library_id, path, duration_seconds, missing)
			 VALUES (?, ?, ?, ?, 0)`, f.id, "lib-1", f.path, f.duration); err != nil {
			t.Fatalf("seed %s: %v", f.id, err)
		}
	}

	svc := NewService(ServiceOptions{
		DB: db, FFmpegPath: ffmpeg, Logger: log.New(io.Discard, "", 0), BaseContext: ctx,
	})
	if !svc.Enabled() {
		t.Fatal("service should be enabled with a db and an ffmpeg")
	}

	// The shortest file must be picked first: cheapest work, and the material
	// that actually needs levelling on a music station.
	pending, err := Backfill{Service: svc}.pending(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 2 || pending[0].path != song {
		t.Fatalf("sweep order = %+v, want the 20s song before the %ds book", pending, longSeconds)
	}

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Backfill{Service: svc, Pause: 10 * time.Millisecond}.Run(runCtx)
	}()

	// Both rows should appear well inside the budget. Before the fix the book
	// alone would have consumed the entire default ten-minute timeout and then
	// recorded a failure.
	deadline := time.Now().Add(90 * time.Second)
	var measured int
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM loudness_measurements WHERE failure = ''`).Scan(&measured); err != nil {
			t.Fatalf("count: %v", err)
		}
		if measured >= 2 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	cancel()
	<-done

	if measured < 2 {
		var failure string
		_ = db.QueryRowContext(ctx,
			`SELECT failure FROM loudness_measurements WHERE failure <> '' LIMIT 1`).Scan(&failure)
		t.Fatalf("only %d of 2 files measured; first failure: %q", measured, failure)
	}

	// The long file must be measured, usable, and NOT flagged partial — it was
	// sampled, but a sample of a uniform file is not a guess about a stream,
	// and flagging it would re-scan every audiobook weekly forever.
	var lufs, peak float64
	var partial bool
	if err := db.QueryRowContext(ctx,
		`SELECT integrated_lufs, true_peak_dbtp, partial FROM loudness_measurements WHERE cache_key = ?`,
		RequestFor(book, longSeconds, false).key()).Scan(&lufs, &peak, &partial); err != nil {
		t.Fatalf("book row: %v", err)
	}
	if partial {
		t.Error("a windowed file must not be marked partial")
	}
	if !(Measurement{IntegratedLUFS: lufs, TruePeakDBTP: peak}).Valid() {
		t.Errorf("book measured %.1f LUFS / %.1f dBTP, which is not usable", lufs, peak)
	}

	// And the two files, built 6 dB apart, must measure 6 dB apart.
	var songLUFS float64
	if err := db.QueryRowContext(ctx,
		`SELECT integrated_lufs FROM loudness_measurements WHERE cache_key = ?`,
		RequestFor(song, 20, false).key()).Scan(&songLUFS); err != nil {
		t.Fatalf("song row: %v", err)
	}
	if gap := lufs - songLUFS; gap < 4.5 || gap > 7.5 {
		t.Errorf("measured gap %.1f LU between files built 6 dB apart (book %.1f, song %.1f)",
			gap, lufs, songLUFS)
	}
}
