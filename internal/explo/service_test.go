package explo

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

func TestLikePrefixEscapesWildcards(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/music/explo", "/music/explo/"},
		{"/music/explo/", "/music/explo/"},
		{"/music/100%_done", `/music/100\%\_done/`},
	}
	for _, tc := range tests {
		if got := likePrefix(tc.in); got != tc.want {
			t.Fatalf("likePrefix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// acoustidStub serves canned AcoustID responses keyed by fingerprint value,
// so each seeded track can resolve to a different (or no) match.
func acoustidStub(t *testing.T, byFingerprint map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fingerprint := r.URL.Query().Get("fingerprint")
		w.Header().Set("Content-Type", "application/json")
		body, ok := byFingerprint[fingerprint]
		if !ok {
			_, _ = w.Write([]byte(`{"status": "ok", "results": []}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func setupExploTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	ctx := context.Background()
	db := storagetest.Open(t)

	userService := users.New(users.ServiceOptions{DB: db})
	if err := userService.Bootstrap(ctx, users.BootstrapInput{AdminUsername: "owner", AdminPassword: "owner-pass-123"}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Music', 'music', '/music');

		INSERT INTO music_albums (id, title, track_count, duration_seconds)
		VALUES ('album-explo', 'explo', 1, 0);

		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds)
		VALUES
		  ('track-matched', '02 Track Two', 'Unknown Artist', 'album-explo', 320),
		  ('track-unmatched', '03 Track Three', 'Unknown Artist', 'album-explo', 200);

		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES
		  ('file-matched', 'lib-1', 'track-matched', '/music/explo/02 Track Two.mp3', 'explo/02 Track Two.mp3', '02 Track Two.mp3', 320),
		  ('file-unmatched', 'lib-1', 'track-unmatched', '/music/explo/03 Track Three.mp3', 'explo/03 Track Three.mp3', '03 Track Three.mp3', 200);
	`); err != nil {
		t.Fatal(err)
	}
	return db, "/music/explo"
}

func TestProcessNewTracksEndToEnd(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	// fpcalc: distinguish tracks by which file path it was asked about.
	fpcalc := fakeFpcalc(t, `
case "$1 $2" in
  "-json "*"Two.mp3") echo '{"duration": 320.0, "fingerprint": "FP-MATCHED"}' ;;
  *) echo '{"duration": 200.0, "fingerprint": "FP-UNMATCHED"}' ;;
esac`)

	acoustid := acoustidStub(t, map[string]string{
		"FP-MATCHED": `{"status": "ok", "results": [{"id": "acoustid-1", "score": 0.9, "recordings": [{
			"id": "mb-rec-1", "title": "Real Song Title", "artists": [{"name": "Real Artist"}],
			"releasegroups": [{"title": "Real Album", "type": "Album"}]
		}]}]}`,
	})
	defer acoustid.Close()

	var reloaded int
	reloadCatalog := func(context.Context) error {
		reloaded++
		return nil
	}

	service := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "test-key",
		FpcalcPath:     fpcalc,
		HTTPClient:     acoustid.Client(),
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
		ReloadCatalog:  reloadCatalog,
	})
	acoustidLookupURL = acoustid.URL // route the real lookup call at the stub
	t.Cleanup(func() { acoustidLookupURL = "https://api.acoustid.org/v2/lookup" })

	if !service.Enabled() {
		t.Fatal("expected service to be enabled")
	}

	result, err := service.ProcessNewTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Matched != 1 || result.Unmatched != 1 || result.Errored != 0 {
		t.Fatalf("result = %#v", result)
	}
	if reloaded == 0 {
		t.Fatal("expected catalog reload after processing")
	}

	// Both tracks are marked processed so a second run is a no-op.
	second, err := service.ProcessNewTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanned != 0 {
		t.Fatalf("second run scanned = %d, want 0 (already processed)", second.Scanned)
	}

	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM explo_tracks WHERE track_id = ?`, "track-matched").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "matched" {
		t.Fatalf("track-matched status = %q", status)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM explo_tracks WHERE track_id = ?`, "track-unmatched").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unmatched" {
		t.Fatalf("track-unmatched status = %q", status)
	}

	// The matched track's title/artist were applied as a metadata override
	// and are visible through the normal catalog projection - no file touched.
	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var matchedTrack *catalog.MusicTrack
	for i := range seed.MusicTracks {
		if seed.MusicTracks[i].ID == "track-matched" {
			matchedTrack = &seed.MusicTracks[i]
		}
	}
	if matchedTrack == nil {
		t.Fatal("track-matched not found in catalog")
	}
	if matchedTrack.Title != "Real Song Title" || matchedTrack.DisplayArtist != "Real Artist" {
		t.Fatalf("matched track = %#v", matchedTrack)
	}
	if matchedTrack.ExternalIDs.MusicBrainzRecordingID != "mb-rec-1" {
		t.Fatalf("matched track external ids = %#v", matchedTrack.ExternalIDs)
	}

	// The album is 100% explo-sourced (both tracks processed), so it's
	// hidden from Recently Added regardless of match outcome.
	var hidden int
	if err := db.QueryRowContext(ctx, `SELECT hidden_from_recently_added FROM music_albums WHERE id = ?`, "album-explo").Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden != 1 {
		t.Fatal("expected album to be hidden from recently added")
	}
	recent := catalog.NewService(seed).ListRecentlyAdded(catalog.PageRequest{Limit: 50})
	for _, entry := range recent.Items {
		if entry.ID == "album-explo" {
			t.Fatal("explo album must not appear in recently added")
		}
	}

	// Both tracks (matched AND unmatched) land in the system Explo playlist.
	var playlistID string
	var system int
	var trackIDsJSON string
	if err := db.QueryRowContext(ctx, `SELECT id, system, track_ids_json FROM music_playlists WHERE name = ?`, DefaultPlaylistName).
		Scan(&playlistID, &system, &trackIDsJSON); err != nil {
		t.Fatal(err)
	}
	if system != 1 {
		t.Fatal("expected explo playlist to be marked system")
	}
	var trackIDs []string
	if err := json.Unmarshal([]byte(trackIDsJSON), &trackIDs); err != nil {
		t.Fatal(err)
	}
	if len(trackIDs) != 2 {
		t.Fatalf("playlist track ids = %#v", trackIDs)
	}
}

// TestProcessNewTracksFallsBackToTextSearchWhenAcoustIDMisses covers the
// full pipeline for a track AcoustID can't identify: it should fall back to
// the filename+duration-gated MusicBrainz text search, apply that match,
// and record it distinctly as "matched-fallback" so it's auditable which
// method identified it.
func TestProcessNewTracksFallsBackToTextSearchWhenAcoustIDMisses(t *testing.T) {
	ctx := context.Background()
	db, exploDir := setupExploTestDB(t)

	// AcoustID never returns a match for either track in this test.
	fpcalc := fakeFpcalc(t, `echo '{"duration": 200.0, "fingerprint": "FP-NONE"}'`)
	acoustid := acoustidStub(t, map[string]string{})
	defer acoustid.Close()

	// track-unmatched is seeded (setupExploTestDB) at "03 Track Three.mp3"
	// with duration_seconds=200 - give the fallback provider a candidate
	// whose duration matches that.
	fallbackProvider := &fakeMusicProvider{
		results: []metadata.SearchResult{
			{
				Authors:         []catalog.ContributorRef{{Name: "Fallback Artist"}},
				DurationSeconds: 199, // within the duration-tolerance gate of 200
				ExternalIDs:     catalog.ExternalIDs{MusicBrainzRecordingID: "mb-fallback-1"},
				Score:           80,
				Title:           "Fallback Title",
			},
		},
	}

	service := NewService(ServiceOptions{
		DB:             db,
		Dirs:           []string{exploDir},
		AcoustIDAPIKey: "test-key",
		FpcalcPath:     fpcalc,
		HTTPClient:     acoustid.Client(),
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Metadata:       metadata.NewService(metadata.ServiceOptions{Providers: []metadata.Provider{fallbackProvider}}),
		Playlists:      playlists.New(db),
	})
	acoustidLookupURL = acoustid.URL
	t.Cleanup(func() { acoustidLookupURL = "https://api.acoustid.org/v2/lookup" })

	result, err := service.ProcessNewTracks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Both candidate tracks miss AcoustID; the fallback only has a
	// duration-matched candidate wired for track-unmatched (200s), so
	// track-matched (320s) stays genuinely unmatched.
	if result.Matched != 1 || result.Unmatched != 1 {
		t.Fatalf("result = %#v", result)
	}

	var status, matchedArtist, mbRecordingID string
	if err := db.QueryRowContext(ctx,
		`SELECT status, matched_artist, musicbrainz_recording_id FROM explo_tracks WHERE track_id = ?`,
		"track-unmatched").Scan(&status, &matchedArtist, &mbRecordingID); err != nil {
		t.Fatal(err)
	}
	if status != "matched-fallback" {
		t.Fatalf("status = %q, want matched-fallback", status)
	}
	if matchedArtist != "Fallback Artist" || mbRecordingID != "mb-fallback-1" {
		t.Fatalf("matched_artist = %q, musicbrainz_recording_id = %q", matchedArtist, mbRecordingID)
	}

	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var track *catalog.MusicTrack
	for i := range seed.MusicTracks {
		if seed.MusicTracks[i].ID == "track-unmatched" {
			track = &seed.MusicTracks[i]
		}
	}
	if track == nil || track.Title != "Fallback Title" || track.DisplayArtist != "Fallback Artist" {
		t.Fatalf("fallback-matched track = %#v", track)
	}
}

func TestProcessNewTracksSkipsWhenDisabled(t *testing.T) {
	db, _ := setupExploTestDB(t)
	service := NewService(ServiceOptions{DB: db}) // no dirs/key/fpcalc configured
	if service.Enabled() {
		t.Fatal("expected service to be disabled without configuration")
	}
	result, err := service.ProcessNewTracks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 0 {
		t.Fatalf("result = %#v, want zero-value", result)
	}
}
