package explo

import (
	"context"
	"strconv"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
)

type fakeCoverDownloader struct{ calls int }

func (f *fakeCoverDownloader) DownloadFromURL(ctx context.Context, url string) (*catalog.Image, error) {
	f.calls++
	return &catalog.Image{ID: "cover_dl_" + strconv.Itoa(f.calls), Path: "/covers/dl-" + strconv.Itoa(f.calls) + ".jpg"}, nil
}

// TestBackfillMissingCoversAppliesAndMarksDone covers the automatic cover
// backfill for already-identified explo albums: it resolves the release group
// from the stored recording MBID, applies a Cover Art Archive cover through the
// override pipeline (downloading it locally), marks the album attempted, and is
// idempotent on a second run.
func TestBackfillMissingCoversAppliesAndMarksDone(t *testing.T) {
	ctx := context.Background()
	db, _ := setupExploTestDB(t)
	srv := withStubMusicBrainz(t, `{"releases":[{"release-group":{"id":"rg-abc","primary-type":"Album"}}]}`, 0)

	downloader := &fakeCoverDownloader{}
	apply := metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{CoverDownloader: downloader})
	svc := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{"/music/explo"},
		AcoustIDAPIKey: "k",
		FpcalcPath:     "/fake/fpcalc",
		HTTPClient:     srv.Client(),
		MetadataApply:  apply,
		Playlists:      playlists.New(db),
		Logger:         func(format string, args ...any) { t.Logf(format, args...) },
	})

	// album-explo's tracks are matched (one carries a recording MBID); no cover.
	mustExec(t, db, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, cover_status) VALUES
		  ('track-matched', 'matched', 'rec-1', ''),
		  ('track-unmatched', 'unmatched', '', '');
	`)

	n, err := svc.backfillMissingCovers(ctx, []string{"/music/explo"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("applied = %d, want 1", n)
	}
	if downloader.calls != 1 {
		t.Fatalf("cover downloads = %d, want 1 (fetched locally, CSP-safe)", downloader.calls)
	}
	var overrides int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_overrides WHERE target_id='album-explo' AND fields_json LIKE '%cover%'`).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if overrides != 1 {
		t.Fatalf("cover overrides for album = %d, want 1", overrides)
	}
	var pending int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM explo_tracks WHERE cover_status=''`).Scan(&pending)
	if pending != 0 {
		t.Fatalf("pending cover_status = %d, want 0 (all attempted)", pending)
	}

	// Idempotent: a second run neither re-queries nor re-applies.
	n2, _ := svc.backfillMissingCovers(ctx, []string{"/music/explo"})
	if n2 != 0 || downloader.calls != 1 {
		t.Fatalf("second run applied=%d downloads=%d, want 0/1", n2, downloader.calls)
	}
}
