package explo

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

func candidateTrackIDs(t *testing.T, svc *Service) map[string]bool {
	t.Helper()
	candidates, err := svc.findCandidateTracks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		ids[candidate.trackID] = true
	}
	return ids
}

// The ledger treated unmatched/error as terminal — one identification attempt
// per track, EVER. Explo drops are fresh releases AcoustID often can't
// identify yet, so the whole batch failed its single early attempt and stayed
// "Unknown Artist" forever. These lock the retry policy: failed rows (errors
// and unmatched alike) retry on the 1d/2d/4d/7d attempts ladder, stop at the
// attempt budget, and matched rows never re-run.
func TestFindCandidateTracksRetriesFailedIdentifications(t *testing.T) {
	db, _ := setupExploTestDB(t)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")
	if err := svc.LoadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	// No ledger rows yet: both seeded explo tracks are first-time candidates.
	ids := candidateTrackIDs(t, svc)
	if !ids["track-matched"] || !ids["track-unmatched"] {
		t.Fatalf("first-time candidates = %v, want both seeded tracks", ids)
	}

	// matched: never re-runs. unmatched with one attempt: due an HOUR later —
	// front-loaded so a transient (or since-fixed-bug) failure retries promptly
	// instead of leaving a broken drop unidentified for a full day.
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, processed_at, attempts) VALUES
		  ('track-matched', 'matched', datetime('now', '-30 days'), 1),
		  ('track-unmatched', 'unmatched', datetime('now', '-2 hours'), 1);
	`)
	ids = candidateTrackIDs(t, svc)
	if ids["track-matched"] {
		t.Fatal("matched track must never become a candidate again")
	}
	if !ids["track-unmatched"] {
		t.Fatal("first retry (attempts=1) must be due after an hour")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-10 minutes') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("first retry fired before its 1-hour wait")
	}

	// Each failure climbs the ladder: attempts=2 waits 6 hours...
	mustExec(t, db, `UPDATE explo_tracks SET attempts = 2, processed_at = datetime('now', '-2 hours') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("second retry fired before its 6-hour wait")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-7 hours') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); !ids["track-unmatched"] {
		t.Fatal("second retry must be due after 6 hours")
	}

	// ...attempts=3 waits 1 day...
	mustExec(t, db, `UPDATE explo_tracks SET attempts = 3, processed_at = datetime('now', '-6 hours') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("third retry fired before its 1-day wait")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-2 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); !ids["track-unmatched"] {
		t.Fatal("third retry must be due after 1 day")
	}

	// ...attempts=4 waits the 3-day rung...
	mustExec(t, db, `UPDATE explo_tracks SET attempts = 4, processed_at = datetime('now', '-2 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("fourth retry fired before its 3-day wait")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-4 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); !ids["track-unmatched"] {
		t.Fatal("fourth retry must be due after 3 days")
	}

	// ...and attempts 5..9 ride the final weekly rung. This is the range the
	// old 5-attempt budget retired forever — tracks off releases AcoustID
	// didn't know yet (fresh albums) burned out in ~8 days and never came
	// back. They must requalify now.
	mustExec(t, db, `UPDATE explo_tracks SET attempts = 5, processed_at = datetime('now', '-4 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("fifth retry fired before its 7-day wait")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-8 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); !ids["track-unmatched"] {
		t.Fatal("previously-retired rows (attempts=5) must requalify under the raised budget")
	}

	// Exhausted attempt budget: retired for good, no matter how old.
	mustExec(t, db, `UPDATE explo_tracks SET attempts = 10, processed_at = datetime('now', '-60 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("unmatched track past the attempt budget must stay retired")
	}

	// Errors ride the same ladder — no more flat daily re-fails.
	mustExec(t, db, `UPDATE explo_tracks SET status = 'error', attempts = 1, processed_at = datetime('now', '-2 days') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); !ids["track-unmatched"] {
		t.Fatal("errored track past its backoff must retry")
	}
	mustExec(t, db, `UPDATE explo_tracks SET processed_at = datetime('now', '-10 minutes') WHERE track_id = 'track-unmatched'`)
	if ids := candidateTrackIDs(t, svc); ids["track-unmatched"] {
		t.Fatal("errored track retried before its backoff")
	}
}

// An enabled pipeline that does no work must ALWAYS say why — a silent boot
// after "explo: folder feature enabled" is the whole "backfill never starts"
// complaint. Here the folder has tracks: report the identified / awaiting /
// retired breakdown.
func TestProcessNewTracksLogsIdleStatusBreakdown(t *testing.T) {
	db, _ := setupExploTestDB(t)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")
	if err := svc.LoadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Both seeded tracks already attempted: one mid-backoff, one out of
	// budget. Nothing is due, but the pass must explain the idle state.
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, processed_at, attempts) VALUES
		  ('track-matched', 'unmatched', datetime('now', '-10 minutes'), 1),
		  ('track-unmatched', 'error', datetime('now', '-60 days'), 10);
	`)
	// One warm-up reconcile absorbs first-pass side effects (album hiding,
	// playlist creation) whose non-zero counts would print the processed
	// line instead of the idle status asserted below.
	_, _, _, _ = svc.syncExploState(context.Background(), svc.effectiveDirs())
	logs := captureLogs(svc)
	if _, err := svc.ProcessNewTracks(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*logs, "\n")
	if !strings.Contains(joined, `2 track(s) under "/music/explo"`) {
		t.Fatalf("idle status did not report the folder track count, logs:\n%s", joined)
	}
	if !strings.Contains(joined, "1 awaiting retry (next due ") {
		t.Fatalf("idle status did not report the pending retry, logs:\n%s", joined)
	}
	if !strings.Contains(joined, "1 retired after 10 failed attempts") {
		t.Fatalf("idle status did not report the retired row, logs:\n%s", joined)
	}

	// Fully identified ledger: still reports, now as "all identified" — an
	// idle pass is never silent, but it also isn't alarming.
	mustExec(t, db, `UPDATE explo_tracks SET status = 'matched'`)
	*logs = nil
	if _, err := svc.ProcessNewTracks(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(*logs, "\n")
	if !strings.Contains(joined, "2 identified") {
		t.Fatalf("healthy idle pass must still report the identified count, logs:\n%s", joined)
	}
	if strings.Contains(joined, "awaiting retry") || strings.Contains(joined, "retired after") {
		t.Fatalf("healthy ledger must not report pending/retired rows, logs:\n%s", joined)
	}
}

// The most common real-world "backfill never starts": the configured folder
// doesn't match any library track (wrong path, or outside a scanned library).
// It used to be 100% silent — enabled, then nothing. Now it names the folder.
func TestProcessNewTracksWarnsOnZeroMatchFolder(t *testing.T) {
	db, _ := setupExploTestDB(t)
	// Points at a path no media_file lives under, but is otherwise fully
	// configured, so the feature is enabled and reaches the idle branch.
	svc := newConfigTestService(t, db, []string{"/music/does-not-exist"}, "env-key")
	if err := svc.LoadConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(svc)
	if _, err := svc.ProcessNewTracks(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*logs, "\n")
	if !strings.Contains(joined, `folder "/music/does-not-exist" matches 0 tracks`) {
		t.Fatalf("zero-match folder must be named in the log, logs:\n%s", joined)
	}
}

// captureLogs redirects a service's logger into a slice for assertions and
// returns a pointer to it (reset with *p = nil between passes).
func captureLogs(svc *Service) *[]string {
	var logs []string
	svc.logger = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	return &logs
}

// The boot log used to explain a disabled explo feature ONLY when the folder
// came from SAMO_EXPLO_DIRS — a web-UI-configured folder missing fpcalc or a
// key disabled itself in total silence ("the backfill never even starts").
func TestDisabledReasonNamesMissingPrerequisite(t *testing.T) {
	ctx := context.Background()

	newSvc := func(t *testing.T, fpcalcPath, envKey string) *Service {
		t.Helper()
		db, _ := setupExploTestDB(t)
		svc := NewService(ServiceOptions{
			DB:             db,
			AcoustIDAPIKey: envKey,
			FpcalcPath:     fpcalcPath,
			MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
			Playlists:      playlists.New(db),
		})
		return svc
	}

	// Never configured anywhere: stay silent.
	svc := newSvc(t, "/fake/fpcalc", "")
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if reason := svc.DisabledReason(ctx); reason != "" {
		t.Fatalf("unconfigured service should be silent, got %q", reason)
	}

	// UI-configured folder, key present, but fpcalc missing.
	svc = newSvc(t, "", "env-key")
	mustExec(t, svc.db, `INSERT INTO explo_config (id, enabled, folder) VALUES (1, 1, '/music/explo')`)
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if reason := svc.DisabledReason(ctx); !strings.Contains(reason, "fpcalc") {
		t.Fatalf("reason = %q, want fpcalc mention", reason)
	}

	// fpcalc ready, folder set, but no key anywhere.
	svc = newSvc(t, "/fake/fpcalc", "")
	mustExec(t, svc.db, `INSERT INTO explo_config (id, enabled, folder) VALUES (1, 1, '/music/explo')`)
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if reason := svc.DisabledReason(ctx); !strings.Contains(reason, "AcoustID") {
		t.Fatalf("reason = %q, want AcoustID mention", reason)
	}

	// Explicitly paused from the UI.
	svc = newSvc(t, "/fake/fpcalc", "env-key")
	mustExec(t, svc.db, `INSERT INTO explo_config (id, enabled, folder) VALUES (1, 0, '/music/explo')`)
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if reason := svc.DisabledReason(ctx); !strings.Contains(reason, "paused") {
		t.Fatalf("reason = %q, want paused mention", reason)
	}

	// Fully configured: enabled, no reason.
	svc = newSvc(t, "/fake/fpcalc", "env-key")
	mustExec(t, svc.db, `INSERT INTO explo_config (id, enabled, folder) VALUES (1, 1, '/music/explo')`)
	if err := svc.LoadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if !svc.Enabled() {
		t.Fatal("fully configured service should be enabled")
	}
	if reason := svc.DisabledReason(ctx); reason != "" {
		t.Fatalf("enabled service should have no reason, got %q", reason)
	}
}

func TestRecordProcessedBumpsAttemptsOnRetry(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	svc := newConfigTestService(t, db, []string{"/music/explo"}, "env-key")

	if err := svc.recordProcessed(ctx, "track-unmatched", "unmatched", identifiedTrack{}, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.recordProcessed(ctx, "track-unmatched", "matched", identifiedTrack{Title: "Real Title"}, ""); err != nil {
		t.Fatal(err)
	}

	var status, matchedTitle string
	var attempts int
	if err := db.QueryRowContext(ctx,
		`SELECT status, matched_title, attempts FROM explo_tracks WHERE track_id = 'track-unmatched'`,
	).Scan(&status, &matchedTitle, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "matched" || matchedTitle != "Real Title" {
		t.Fatalf("retry outcome not recorded: status=%q title=%q", status, matchedTitle)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (bumped on retry)", attempts)
	}
}
