package explo

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

// TestReconcileExploTracksBothDirections covers the per-track silo marker:
// tracks under the folder gain is_explo, and narrowing the folder takes it
// away again — the same self-correcting shape as the album hidden flag.
func TestReconcileExploTracksBothDirections(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)
	mustExec(t, db, `
		INSERT INTO music_tracks (id, title, duration_seconds) VALUES ('track-real', 'Owned Song', 180);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-real', 'lib-1', 'track-real', '/music/owned/song.mp3', 'owned/song.mp3', 'song.mp3', 180);
	`)
	svc := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		MetadataApply: metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:     playlists.New(db),
	})

	flagged, unflagged, err := svc.reconcileExploTracks(ctx, []string{exploDir})
	if err != nil {
		t.Fatal(err)
	}
	if flagged != 2 || unflagged != 0 {
		t.Fatalf("flagged=%d unflagged=%d, want 2/0", flagged, unflagged)
	}
	assertIsExplo := func(trackID string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, `SELECT is_explo FROM music_tracks WHERE id = ?`, trackID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("is_explo(%s) = %d, want %d", trackID, got, want)
		}
	}
	assertIsExplo("track-matched", 1)
	assertIsExplo("track-unmatched", 1)
	assertIsExplo("track-real", 0)

	// Idempotent.
	flagged, unflagged, err = svc.reconcileExploTracks(ctx, []string{exploDir})
	if err != nil || flagged != 0 || unflagged != 0 {
		t.Fatalf("repeat run flagged=%d unflagged=%d err=%v, want 0/0/nil", flagged, unflagged, err)
	}

	// Clearing the folder un-flags everything — full recovery.
	flagged, unflagged, err = svc.reconcileExploTracks(ctx, nil)
	if err != nil || flagged != 0 || unflagged != 2 {
		t.Fatalf("clear run flagged=%d unflagged=%d err=%v, want 0/2/nil", flagged, unflagged, err)
	}
	assertIsExplo("track-matched", 0)
}

// TestProcessNewTracksCorralsDropBeforeIdentifyLoop locks in the flood fix:
// the moment a pass finds candidates, the drop is flagged + hidden and the
// catalog reloaded BEFORE the slow identify loop starts, not minutes later.
func TestProcessNewTracksCorralsDropBeforeIdentifyLoop(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	// fpcalc stub whose first invocation asserts the corral already happened.
	corralChecked := false
	var corralErr string
	fpcalc := fakeFpcalc(t, `echo '{"duration": 200.0, "fingerprint": "FP-X"}'`)
	acoustid := acoustidStub(t, nil)
	defer acoustid.Close()
	acoustidLookupURL = acoustid.URL
	t.Cleanup(func() { acoustidLookupURL = "https://api.acoustid.org/v2/lookup" })

	svc := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "k",
		FpcalcPath:     fpcalc,
		HTTPClient:     acoustid.Client(),
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
		ReloadCatalog: func(context.Context) error {
			if corralChecked {
				return nil
			}
			corralChecked = true
			// At the FIRST reload the identify loop hasn't finished, but the
			// drop must already be flagged and its album hidden.
			var isExplo, hidden int
			if err := db.QueryRowContext(ctx, `SELECT is_explo FROM music_tracks WHERE id='track-matched'`).Scan(&isExplo); err != nil {
				corralErr = err.Error()
				return nil
			}
			if err := db.QueryRowContext(ctx, `SELECT hidden_from_recently_added FROM music_albums WHERE id='album-explo'`).Scan(&hidden); err != nil {
				corralErr = err.Error()
				return nil
			}
			if isExplo != 1 || hidden != 1 {
				corralErr = fmt.Sprintf("first reload saw is_explo=%d hidden=%d, want 1/1", isExplo, hidden)
			}
			return nil
		},
	})

	if _, err := svc.ProcessNewTracks(ctx); err != nil {
		t.Fatal(err)
	}
	if !corralChecked {
		t.Fatal("catalog was never reloaded during the pass")
	}
	if corralErr != "" {
		t.Fatal(corralErr)
	}
}

// TestEligibilityExprPinsUTC is the regression lock for the retry-skew bug:
// the cutoff must format a UTC-pinned now(), not the session-TimeZone now().
// Checked both textually (the fix itself) and functionally, by pinning one
// session to a non-UTC zone and asserting the SQL cutoff still lands within
// a minute of real UTC now.
func TestEligibilityExprPinsUTC(t *testing.T) {
	for _, expr := range []string{
		exploEligibilityCheckExpr("et.processed_at"),
		exploCoverEligibilityExpr("et.cover_attempted_at"),
	} {
		if !strings.Contains(expr, "AT TIME ZONE 'UTC'") {
			t.Fatalf("eligibility expr lost its UTC pin: %s", expr)
		}
	}

	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET TIME ZONE 'Australia/Sydney'`); err != nil {
		t.Fatal(err)
	}
	var cutoff string
	// Zero-interval cutoff == "now" as the eligibility comparison sees it.
	query := `SELECT to_char((now() AT TIME ZONE 'UTC') - ('0 minutes')::interval, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`
	if err := conn.QueryRowContext(ctx, query).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	parsed, err := time.Parse(time.RFC3339, cutoff)
	if err != nil {
		t.Fatalf("cutoff %q is not RFC3339: %v", cutoff, err)
	}
	if diff := time.Since(parsed); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("UTC-pinned cutoff %q is %v away from real UTC now — session TimeZone leaked in", cutoff, diff)
	}
}
