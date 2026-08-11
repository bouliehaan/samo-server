package loudness

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// The cache is the thing that makes measure-once affordable, so its SQL is
// worth exercising against the real engine rather than trusting by inspection.
func TestStoreRoundTrip(t *testing.T) {
	db := storagetest.Open(t)
	s := store{db: db}
	ctx := context.Background()

	if _, found := s.lookup(ctx, "file:/music/missing.flac"); found {
		t.Fatal("lookup found a row that was never written")
	}

	measured := Measurement{IntegratedLUFS: -9.4, TruePeakDBTP: -0.2, LoudnessRange: 4.8}
	if err := s.save(ctx, "file:/music/a.flac", "1234:5678", measured, nil); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, found := s.lookup(ctx, "file:/music/a.flac")
	if !found {
		t.Fatal("lookup missed the row just written")
	}
	if math.Abs(got.Measurement.IntegratedLUFS-(-9.4)) > 0.001 ||
		math.Abs(got.Measurement.TruePeakDBTP-(-0.2)) > 0.001 {
		t.Fatalf("measurement round-tripped as %+v", got.Measurement)
	}
	if got.Fingerprint != "1234:5678" {
		t.Fatalf("fingerprint = %q", got.Fingerprint)
	}
	if got.Failure != "" {
		t.Fatalf("failure = %q, want empty", got.Failure)
	}
	if time.Since(got.MeasuredAt) > time.Minute {
		t.Fatalf("measured_at = %v, want roughly now", got.MeasuredAt)
	}
}

// Re-measuring must overwrite in place. Without a working upsert the second
// save fails on the primary key and a re-tagged file keeps its stale numbers
// forever.
func TestStoreUpsertsOnRemeasure(t *testing.T) {
	db := storagetest.Open(t)
	s := store{db: db}
	ctx := context.Background()

	if err := s.save(ctx, "file:/music/a.flac", "old", Measurement{IntegratedLUFS: -20}, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.save(ctx, "file:/music/a.flac", "new", Measurement{IntegratedLUFS: -12}, nil); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, _ := s.lookup(ctx, "file:/music/a.flac")
	if math.Abs(got.Measurement.IntegratedLUFS-(-12)) > 0.001 || got.Fingerprint != "new" {
		t.Fatalf("row = %+v, want the second measurement", got)
	}
}

// A failure is recorded rather than dropped, so an unreadable file is not
// re-analysed on every single airing.
func TestStoreRecordsFailures(t *testing.T) {
	db := storagetest.Open(t)
	s := store{db: db}
	ctx := context.Background()

	if err := s.save(ctx, "file:/music/broken.flac", "", Measurement{}, errors.New("moov atom not found")); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found := s.lookup(ctx, "file:/music/broken.flac")
	if !found || got.Failure == "" {
		t.Fatalf("row = %+v, want a recorded failure", got)
	}
}

// The service must trust a fresh row, distrust one whose file has changed
// underneath it, and re-try a failure only after the cooldown.
func TestServiceFreshness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "track.flac")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := RequestFor(path, 240, false)
	svc := &Service{target: DefaultTarget.normalized()}

	good := record{
		Measurement: Measurement{IntegratedLUFS: -14, TruePeakDBTP: -1.2},
		Fingerprint: req.fingerprint(),
		MeasuredAt:  time.Now(),
	}
	if !svc.fresh(good, req) {
		t.Error("a current measurement of an unchanged file should be trusted")
	}

	stale := good
	stale.Fingerprint = "0:0"
	if svc.fresh(stale, req) {
		t.Error("a file whose bytes changed must be re-measured, not levelled from the old numbers")
	}

	recentFailure := record{Fingerprint: req.fingerprint(), Failure: "boom", MeasuredAt: time.Now()}
	if !svc.fresh(recentFailure, req) {
		t.Error("a fresh failure must suppress retries, or every airing re-runs ffmpeg on a broken file")
	}
	oldFailure := recentFailure
	oldFailure.MeasuredAt = time.Now().Add(-failureCooldown - time.Minute)
	if svc.fresh(oldFailure, req) {
		t.Error("a failure past its cooldown should be retried")
	}

	// Age alone does not expire a file's measurement — only a live source's
	// does. See TestWindowedFileIsNotMarkedPartial for both directions.
	aged := good
	aged.MeasuredAt = time.Now().Add(-partialTTL - time.Hour)
	if !svc.fresh(aged, req) {
		t.Error("a file's measurement should not expire with age")
	}
}

// The backfill sweep's query has to actually run, and its key expression has
// to agree with Request.key or it re-measures files that are already cached.
func TestBackfillFindsUnmeasuredFilesOnly(t *testing.T) {
	db := storagetest.Open(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO libraries (id, name, kind, path) VALUES (?, ?, ?, ?)`,
		"lib-1", "Music", "music", "/music"); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	seed := func(id, path string, duration int) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO media_files (id, library_id, path, duration_seconds, missing)
			 VALUES (?, ?, ?, ?, 0)`, id, "lib-1", path, duration); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("f1", "/music/unmeasured.flac", 240)
	seed("f2", "/music/measured.flac", 240)
	seed("f3", "/music/sting.wav", 1) // too short to measure meaningfully
	// A file the scanner has flagged as gone. `missing` is BIGINT, not
	// boolean — comparing it to FALSE is a runtime error in Postgres, not a
	// silently wrong result, so this row is what keeps the predicate honest.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO media_files (id, library_id, path, duration_seconds, missing)
		 VALUES (?, ?, ?, ?, 1)`, "f4", "lib-1", "/music/gone.flac", 240); err != nil {
		t.Fatalf("seed f4: %v", err)
	}

	svc := &Service{store: store{db: db}, target: DefaultTarget.normalized()}
	if err := svc.store.save(ctx, RequestFor("/music/measured.flac", 240, false).key(), "", Measurement{IntegratedLUFS: -14}, nil); err != nil {
		t.Fatalf("seed measurement: %v", err)
	}

	pending, err := Backfill{Service: svc}.pending(ctx)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].path != "/music/unmeasured.flac" {
		t.Fatalf("pending = %+v, want only the unmeasured full-length file", pending)
	}
}

// An ffmpeg without alimiter must degrade to peak-safe levelling rather than
// emitting a filtergraph it cannot run — which would fail the item outright
// and present as a dead source.
func TestPlanDropsLimiterWhenFFmpegCannotRunIt(t *testing.T) {
	svc := &Service{
		target:  DefaultTarget.normalized(),
		ffmpeg:  filepath.Join(t.TempDir(), "not-ffmpeg"),
		logger:  log.New(io.Discard, "", 0),
		baseCtx: context.Background(),
	}
	// Quiet and peaky: the one shape that wants the limiter.
	m := Measurement{IntegratedLUFS: -26, TruePeakDBTP: -0.5}

	if plan := DefaultTarget.Plan(m); !plan.Limit {
		t.Fatal("precondition: this measurement should want a limiter under the normal policy")
	}
	plan := svc.planFor(m)
	if plan.Limit {
		t.Error("must not emit a limiter this ffmpeg cannot run")
	}
	// Peak-safe means the gain stops at the item's own headroom, so it still
	// gets levelled as far as it safely can — just not all the way.
	if want := DefaultTarget.CeilingDBTP - m.TruePeakDBTP; math.Abs(plan.GainDB-want) > 0.05 {
		t.Errorf("gain = %+.1f dB, want the peak-safe %+.1f dB", plan.GainDB, want)
	}
}
