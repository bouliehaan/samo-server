package explo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/covers"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// TestReset0005DoesNotDestroyRealCovers reproduces the exact state migration
// 0005 leaves behind: every explo album's cover_status blanked to ” and a
// backfill re-pass in which EVERY network source misses (the worst case). An
// album that already has a real, on-disk cover MUST survive that re-pass with
// its cover intact — the mass reset's safety rests entirely on the fast path.
func TestReset0005DoesNotDestroyRealCovers(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	// Real cover store on disk + a real cover file for the album.
	coverDir := t.TempDir()
	coverStore, err := covers.New(db, covers.Options{CoverDir: coverDir})
	if err != nil {
		t.Fatal(err)
	}
	realCoverPath := filepath.Join(coverDir, "cover_real.jpg")
	if err := os.WriteFile(realCoverPath, []byte("REAL-ALBUM-ART-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Every network source misses: MB 404, iTunes/Deezer empty. So if the
	// fast path fails to see the real art, the album falls to a PLACEHOLDER.
	mbSrv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	withStubCoverSources(t, "", "")
	oldCAA := caaBaseURL
	caaBaseURL = mbSrv.URL // any 404-ish base; downloads verify to false regardless
	t.Cleanup(func() { caaBaseURL = oldCAA })

	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: coverStore})
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		FpcalcPath:    "/fake/fpcalc",
		HTTPClient:    mbSrv.Client(),
		MetadataApply: apply,
		Playlists:     playlists.New(db),
		Covers:        coverStore,
		Logger:        func(format string, args ...any) { t.Logf(format, args...) },
	})

	// Case A: album whose real cover lives in the metadata override (a
	// successfully-downloaded CAA cover under the OLD pipeline), now reset
	// to cover_status='' by 0005.
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, cover_status)
		VALUES ('track-matched', 'matched', 'rec-1', '');
	`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-track', 'track-matched', ?)`,
		`{"cover":[{"id":"cover_real","path":"`+realCoverPath+`","mimeType":"image/jpeg"}]}`); err != nil {
		t.Fatal(err)
	}

	applied, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Case A (real cover in override): applied=%d placeholders=%d", applied, placeholders)

	// The override's cover must STILL point at the real file — not a placeholder.
	var fieldsJSON string
	if err := db.QueryRowContext(ctx, `SELECT fields_json FROM metadata_overrides WHERE target_id='track-matched'`).Scan(&fieldsJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("Case A override after re-pass: %s", fieldsJSON)
	if placeholders != 0 {
		t.Errorf("REGRESSION: real cover was replaced by a placeholder (placeholders=%d)", placeholders)
	}
	if !containsPath(fieldsJSON, realCoverPath) {
		t.Errorf("REGRESSION: album no longer references its real cover %q; override now: %s", realCoverPath, fieldsJSON)
	}

	var coverStatus string
	if err := db.QueryRowContext(ctx, `SELECT cover_status FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "done" {
		t.Errorf("real-cover album ended at cover_status=%q, want done (should not churn every pass)", coverStatus)
	}
}

// TestReset0005KeepsLiveUrlOnlyCovers is the regression lock for the fix. The
// old pipeline stored some covers as a URL only (the local download failed,
// but the external URL renders fine via redirect). 0005 blanked their
// cover_status; the buggy re-pass saw no local file and, on a chain miss,
// stamped a placeholder over the working cover. The fix verifies a URL-only
// cover before touching it: a LIVE one is kept (and upgraded to a local file),
// never replaced by a placeholder — even when every discovery source misses.
func TestReset0005KeepsLiveUrlOnlyCovers(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	// Chain misses everywhere (worst case: fresh drop CAA lacks, or an outage
	// mid re-pass). Only the existing URL is live.
	mbSrv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	withStubCoverSources(t, "", "")
	oldCAA := caaBaseURL
	caaBaseURL = mbSrv.URL
	t.Cleanup(func() { caaBaseURL = oldCAA })

	store := newFakeCoverStore(t)
	const workingURL = "https://coverartarchive.org/release-group/real-rg/front-500"
	store.allow(workingURL) // the old cover's URL still resolves to bytes

	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		FpcalcPath:    "/fake/fpcalc",
		HTTPClient:    mbSrv.Client(),
		MetadataApply: apply,
		Playlists:     playlists.New(db),
		Covers:        store,
		Logger:        func(format string, args ...any) { t.Logf(format, args...) },
	})

	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, cover_status)
		VALUES ('track-matched', 'matched', 'rec-1', '');
	`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-track', 'track-matched', ?)`,
		`{"cover":[{"id":"cover_x","url":"`+workingURL+`"}]}`); err != nil {
		t.Fatal(err)
	}

	applied, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}

	var fieldsJSON string
	if err := db.QueryRowContext(ctx, `SELECT fields_json FROM metadata_overrides WHERE target_id='track-matched'`).Scan(&fieldsJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("URL-only cover after re-pass: applied=%d placeholders=%d override=%s", applied, placeholders, fieldsJSON)
	if placeholders != 0 {
		t.Fatalf("REGRESSION: a placeholder was stamped over a live URL-only cover")
	}
	if !stringContains(fieldsJSON, workingURL) {
		t.Fatalf("live URL-only cover was lost; override now: %s", fieldsJSON)
	}
	// And it was upgraded to a local file (same-origin), not left URL-only.
	if !stringContains(fieldsJSON, `"path"`) {
		t.Fatalf("live URL cover should have been adopted to a local path; override: %s", fieldsJSON)
	}
	var coverStatus string
	if err := db.QueryRowContext(ctx, `SELECT cover_status FROM explo_tracks WHERE track_id='track-matched'`).Scan(&coverStatus); err != nil {
		t.Fatal(err)
	}
	if coverStatus != "done" {
		t.Fatalf("adopted cover ended at cover_status=%q, want done", coverStatus)
	}
}

// TestReset0005DeadUrlGetsPlaceholder confirms the other half: a URL-only
// cover whose URL is now DEAD (CAA 404) is correctly allowed to be replaced —
// a dead external link renders as a broken image, so a placeholder is an
// improvement, and the album keeps retrying real sources.
func TestReset0005DeadUrlGetsPlaceholder(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	mbSrv := withStubMusicBrainz(t, `{"releases":[]}`, 0)
	withStubCoverSources(t, "", "")
	oldCAA := caaBaseURL
	caaBaseURL = mbSrv.URL
	t.Cleanup(func() { caaBaseURL = oldCAA })

	store := newFakeCoverStore(t) // allow-list empty: every URL is dead
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: store})
	svc := NewService(ServiceOptions{
		DB: db, Dirs: []string{exploDir}, FpcalcPath: "/fake/fpcalc",
		HTTPClient: mbSrv.Client(), MetadataApply: apply,
		Playlists: playlists.New(db), Covers: store,
		Logger: func(format string, args ...any) { t.Logf(format, args...) },
	})

	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, cover_status)
		VALUES ('track-matched', 'matched', 'rec-1', '');
	`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-track', 'track-matched', '{"cover":[{"id":"cover_dead","url":"https://coverartarchive.org/release-group/dead/front-500"}]}')`); err != nil {
		t.Fatal(err)
	}

	_, placeholders, err := svc.backfillMissingCovers(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if placeholders != 1 {
		t.Fatalf("dead-URL album should have received a placeholder, got placeholders=%d", placeholders)
	}
}

func containsPath(haystack, needle string) bool {
	return len(needle) > 0 && stringContains(haystack, needle)
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
