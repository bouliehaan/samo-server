package explo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/metadata"
)

// Keep never asks MusicBrainz for the album name. The ledger holds the title
// identification resolved, and a release-group lookup that would answer with
// something else proves it was not consulted — the interactive request used
// to wait on exactly that call, through a VPN, past the phone's deadline.
func TestKeepAlbumTitleReadsTheLedgerNotTheNetwork(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Should Not Be Asked"}`))
	}))
	defer srv.Close()
	old := musicbrainzReleaseGroupURL
	musicbrainzReleaseGroupURL = srv.URL + "/"
	t.Cleanup(func() { musicbrainzReleaseGroupURL = old })

	if _, err := db.ExecContext(ctx, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_release_group_id, matched_title, matched_artist, matched_album)
		VALUES ('track-matched', 'matched', 'rg-1', 'Bad Habit', 'Steve Lacy', 'Gemini Rights')`); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceOptions{
		DB:         db,
		Dirs:       []string{exploDir},
		HTTPClient: srv.Client(),
	})
	got, err := service.keepAlbumTitle(ctx, "track-matched", catalog.MusicTrack{
		Title:      "Bad Habit",
		AlbumTitle: "explo", // the drop folder, which is what the scanner read
	})
	if err != nil || got != "Gemini Rights" {
		t.Fatalf("got (%q, %v), want (Gemini Rights, nil)", got, err)
	}
	if asked != 0 {
		t.Fatalf("keep asked MusicBrainz %d time(s); it must read the ledger only", asked)
	}
}

// A ledger row with no title — identified before the column existed, or while
// MusicBrainz was unreachable — is not Keep's problem to fix. It refuses the
// drop-folder name rather than reach for the network, and the pipeline's own
// pass fills the title in.
func TestKeepAlbumTitleWithoutLedgerTitleFallsBackWithoutTheNetwork(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Oracular Spectacular"}`))
	}))
	defer srv.Close()
	old := musicbrainzReleaseGroupURL
	musicbrainzReleaseGroupURL = srv.URL + "/"
	t.Cleanup(func() { musicbrainzReleaseGroupURL = old })

	if _, err := db.ExecContext(ctx, `
		INSERT INTO explo_tracks (track_id, status, musicbrainz_release_group_id, matched_title, matched_artist)
		VALUES ('track-matched', 'matched', 'rg-1', 'Kids', 'MGMT')`); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{DB: db, Dirs: []string{exploDir}, HTTPClient: srv.Client()})

	// The effective catalog title is fine when it is a real album name: for a
	// drop identified before the ledger kept titles, that is the identified
	// release group applied as an override.
	got, err := service.keepAlbumTitle(ctx, "track-matched", catalog.MusicTrack{
		Title:      "Kids",
		AlbumTitle: "Oracular Spectacular",
	})
	if err != nil || got != "Oracular Spectacular" {
		t.Fatalf("got (%q, %v), want (Oracular Spectacular, nil)", got, err)
	}
	// ...and refused when it is the drop folder.
	if _, err := service.keepAlbumTitle(ctx, "track-matched", catalog.MusicTrack{
		Title:      "Kids",
		AlbumTitle: "explo",
	}); err == nil || !strings.Contains(err.Error(), "explo") {
		t.Fatalf("expected a refusal naming the drop folder, got %v", err)
	}
	if asked != 0 {
		t.Fatalf("keep asked MusicBrainz %d time(s); it must never wait on the network", asked)
	}
}

// The pipeline pass fills in titles the ledger lacks — one lookup each, from
// the release group identification already recorded — and names the album
// when nothing has named it yet.
func TestBackfillAlbumTitlesFillsBlankRows(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	withStubReleaseGroup(t, `{"title":"Oracular Spectacular"}`, 0)

	// A second drop in its own album, whose album already carries a title
	// override from before the ledger kept titles.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, track_count, duration_seconds)
		VALUES ('album-explo-2', 'explo', 1, 0);
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds)
		VALUES ('track-corrected', '04 Track Four', 'Unknown Artist', 'album-explo-2', 210);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-corrected', 'lib-1', 'track-corrected', '/music/explo/04 Track Four.mp3', 'explo/04 Track Four.mp3', '04 Track Four.mp3', 210);
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json)
		VALUES ('music-album', 'album-explo-2', '{"title":"Already Named"}');

		INSERT INTO explo_tracks (track_id, status, musicbrainz_recording_id, musicbrainz_release_group_id, matched_title, matched_artist)
		VALUES ('track-matched', 'matched', 'mb-rec-1', 'rg-1', 'Kids', 'MGMT'),
		       ('track-corrected', 'matched', 'mb-rec-4', 'rg-4', 'Time to Pretend', 'MGMT');
		INSERT INTO explo_tracks (track_id, status, musicbrainz_release_group_id, matched_title, matched_artist, matched_album)
		VALUES ('track-unmatched', 'matched', 'rg-2', 'Electric Feel', 'MGMT', 'Already Here');
	`); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceOptions{
		DB:            db,
		Dirs:          []string{exploDir},
		HTTPClient:    http.DefaultClient,
		MetadataApply: metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
	})

	if resolved := service.backfillAlbumTitles(ctx); resolved != 2 {
		t.Fatalf("resolved = %d, want 2", resolved)
	}

	ledgerAlbum := func(trackID string) string {
		t.Helper()
		var album string
		if err := db.QueryRowContext(ctx, `SELECT matched_album FROM explo_tracks WHERE track_id = ?`, trackID).Scan(&album); err != nil {
			t.Fatal(err)
		}
		return album
	}
	if got := ledgerAlbum("track-matched"); got != "Oracular Spectacular" {
		t.Fatalf("track-matched matched_album = %q, want Oracular Spectacular", got)
	}
	if got := ledgerAlbum("track-corrected"); got != "Oracular Spectacular" {
		t.Fatalf("track-corrected matched_album = %q, want Oracular Spectacular", got)
	}
	// A row that already had its title was neither rewritten nor looked up.
	if got := ledgerAlbum("track-unmatched"); got != "Already Here" {
		t.Fatalf("a row with a title was rewritten to %q", got)
	}

	// The nameless album is named; the one already named is left alone.
	if got := service.overriddenAlbumTitle(ctx, "album-explo"); got != "Oracular Spectacular" {
		t.Fatalf("album-explo title override = %q, want the resolved title", got)
	}
	if got := service.overriddenAlbumTitle(ctx, "album-explo-2"); got != "Already Named" {
		t.Fatalf("album-explo-2 title override = %q; the backfill must not overwrite a name already on file", got)
	}

	// Nothing left owed: a second pass makes no lookups and resolves nothing.
	if resolved := service.backfillAlbumTitles(ctx); resolved != 0 {
		t.Fatalf("second pass resolved = %d, want 0", resolved)
	}

	// Keep now files under it, from the ledger.
	got, err := service.keepAlbumTitle(ctx, "track-matched", catalog.MusicTrack{Title: "Kids", AlbumTitle: "explo"})
	if err != nil || got != "Oracular Spectacular" {
		t.Fatalf("keepAlbumTitle = (%q, %v)", got, err)
	}
}
