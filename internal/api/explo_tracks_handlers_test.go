package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/explo"
	"github.com/bouliehaan/samo-server/internal/metadata"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/radio"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

// exploTestServer wires a real explo service (storagetest DB) plus a catalog
// projection into an API server, seeding one identified explo track.
func exploTestServer(t *testing.T, configured bool) http.Handler {
	t.Helper()
	db := storagetest.Open(t)
	if _, err := db.Exec(`
		INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Music', 'music', '/music');
		INSERT INTO music_albums (id, title, track_count, duration_seconds) VALUES ('album-explo', 'Weekly Drop', 1, 0);
		INSERT INTO music_tracks (id, title, display_artist, album_id, duration_seconds, is_explo)
		VALUES ('track-1', 'Real Song', 'Real Artist', 'album-explo', 200, 1);
		INSERT INTO media_files (id, library_id, track_id, path, relative_path, file_name, duration_seconds)
		VALUES ('file-1', 'lib-1', 'track-1', '/music/explo/one.mp3', 'explo/one.mp3', 'one.mp3', 200);
		INSERT INTO explo_tracks (track_id, status, matched_title, matched_artist, cover_status)
		VALUES ('track-1', 'matched', 'Real Song', 'Real Artist', 'done');
	`); err != nil {
		t.Fatal(err)
	}

	dirs := []string{}
	if configured {
		dirs = []string{"/music/explo"}
	}
	exploService := explo.NewService(explo.ServiceOptions{
		DB:             db,
		Dirs:           dirs,
		AcoustIDAPIKey: "k",
		FpcalcPath:     "/fake/fpcalc",
		MetadataApply:  metadata.NewMetadataApplyServiceWithOptions(db, metadata.MetadataApplyOptions{}),
		Playlists:      playlists.New(db),
	})

	radioService, err := radio.NewService(radio.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerOptions{
		Catalog: catalog.NewService(catalog.Seed{
			MusicTracks: []catalog.MusicTrack{{
				ID: "track-1", Title: "Real Song", DisplayArtist: "Real Artist",
				AlbumID: "album-explo", AlbumTitle: "Real Album", IsExplo: true,
			}},
			MusicAlbums: []catalog.MusicAlbum{{ID: "album-explo", Title: "Real Album", TrackCount: 1, IsExplo: true}},
		}),
		Radio: radioService,
		Explo: exploService,
	})
}

func TestExploStatusGatesTabOnConfigured(t *testing.T) {
	for _, configured := range []bool{true, false} {
		handler := exploTestServer(t, configured)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/explo/status", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("configured=%v status = %d body=%s", configured, rec.Code, rec.Body.String())
		}
		var body struct {
			Configured bool `json:"configured"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Configured != configured {
			t.Fatalf("configured=%v but status reported %v", configured, body.Configured)
		}
	}
}

func TestExploTracksListsLedgerWithCatalogTitles(t *testing.T) {
	handler := exploTestServer(t, true)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/explo/tracks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Configured bool `json:"configured"`
		Summary    struct {
			InFolder   int `json:"inFolder"`
			Identified int `json:"identified"`
			CoversDone int `json:"coversDone"`
		} `json:"summary"`
		Tracks []struct {
			TrackID     string `json:"trackId"`
			Status      string `json:"status"`
			Title       string `json:"title"`
			Artist      string `json:"artist"`
			AlbumID     string `json:"albumId"`
			AlbumTitle  string `json:"albumTitle"`
			CoverStatus string `json:"coverStatus"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured {
		t.Fatal("expected configured=true")
	}
	if body.Summary.InFolder != 1 || body.Summary.Identified != 1 || body.Summary.CoversDone != 1 {
		t.Fatalf("summary = %+v", body.Summary)
	}
	if len(body.Tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(body.Tracks))
	}
	track := body.Tracks[0]
	if track.TrackID != "track-1" || track.Status != "matched" || track.CoverStatus != "done" {
		t.Fatalf("track = %+v", track)
	}
	// Display fields come from the (override-aware) catalog projection.
	if track.Title != "Real Song" || track.AlbumTitle != "Real Album" || track.AlbumID != "album-explo" {
		t.Fatalf("decorated fields = %+v", track)
	}
}
