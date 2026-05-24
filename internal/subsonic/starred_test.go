package subsonic

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/internal/users"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestGetStarredUsesPerUserPlayback(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, playback_json, added_at, updated_at)
		VALUES ('album-1', 'Night Broadcasts', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	playbackService := playback.New(db)
	if _, err := playbackService.Patch(ctx, users.BootstrapUserID, playback.TargetMusicAlbum, "album-1", playback.PatchInput{
		Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(Options{
		Catalog: catalog.NewService(catalog.Seed{
			MusicAlbums: []catalog.MusicAlbum{{
				ID:    "album-1",
				Title: "Night Broadcasts",
			}},
		}),
		Playback: playbackService,
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/getStarred.view?f=json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	response := decodeSubsonicResponse(t, rec)
	starred := response["starred"].(map[string]any)
	albums := starred["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("starred album count = %d, want 1", len(albums))
	}
}

func TestStarAndUnstarTrack(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, playback_json, added_at, updated_at)
		VALUES ('album-1', 'Night Broadcasts', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, album_id, album_title, duration_seconds, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Signal One', 'album-1', 'Night Broadcasts', 120, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	playbackService := playback.New(db)
	handler := New(Options{
		Catalog: catalog.NewService(catalog.Seed{
			MusicTracks: []catalog.MusicTrack{{
				ID:         "track-1",
				Title:      "Signal One",
				AlbumID:    "album-1",
				AlbumTitle: "Night Broadcasts",
			}},
		}),
		Playback: playbackService,
	})

	starReq := httptest.NewRequest(http.MethodGet, "/rest/star.view?f=json&id=track-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, starReq)
	decodeSubsonicResponse(t, rec)

	state, err := playbackService.Get(ctx, users.BootstrapUserID, playback.TargetMusicTrack, "track-1")
	if err != nil || !state.Starred {
		t.Fatalf("state = %#v err = %v", state, err)
	}

	unstarReq := httptest.NewRequest(http.MethodGet, "/rest/unstar.view?f=json&id=track-1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unstarReq)
	decodeSubsonicResponse(t, rec)

	state, err = playbackService.Get(ctx, users.BootstrapUserID, playback.TargetMusicTrack, "track-1")
	if err != nil || state.Starred {
		t.Fatalf("state = %#v err = %v", state, err)
	}
}

func TestGetAlbumList2StarredType(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, playback_json, added_at, updated_at)
		VALUES ('album-1', 'Night Broadcasts', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	playbackService := playback.New(db)
	if _, err := playbackService.Patch(ctx, users.BootstrapUserID, playback.TargetMusicAlbum, "album-1", playback.PatchInput{
		Starred: boolPtr(true),
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(Options{
		Catalog: catalog.NewService(catalog.Seed{
			MusicAlbums: []catalog.MusicAlbum{{
				ID:    "album-1",
				Title: "Night Broadcasts",
			}},
		}),
		Playback: playbackService,
	})

	req := httptest.NewRequest(http.MethodGet, "/rest/getAlbumList2.view?f=json&type=starred&size=10", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	response := decodeSubsonicResponse(t, rec)
	list := response["albumList2"].(map[string]any)
	albums := list["album"].([]any)
	if len(albums) != 1 {
		t.Fatalf("album count = %d, want 1", len(albums))
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func decodeSubsonicResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	response := payload["subsonic-response"]
	if response["status"] != "ok" {
		t.Fatalf("status = %v", response["status"])
	}
	return response
}
