package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaylistImportEndpointRebuildsFromLocalTracks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
		VALUES ('track-1', 'Ceremony', 'New Order', 'Substance', 263)`); err != nil {
		t.Fatal(err)
	}

	catalogService := catalog.NewService(catalog.Seed{})
	reload := func(ctx context.Context) error {
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		return nil
	}
	if err := reload(ctx); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:       catalogService,
		Playlists:     playlists.New(db),
		ReloadCatalog: reload,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/music/playlists/import", bytes.NewBufferString(`{
		"name": "Imported",
		"sourceType": "plain",
		"content": "New Order - Ceremony"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result playlists.ImportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Playlist == nil || result.MatchedCount != 1 {
		t.Fatalf("result = %#v", result)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/music/playlists/"+result.Playlist.ID+"/tracks", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tracks status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"id":"track-1"`)) {
		t.Fatalf("tracks body = %s", rec.Body.String())
	}
}

func TestPlaylistVisibilityAllowsPublicSharingOnly(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	userService, adminToken, listenerToken := testUserServiceWithTokens(t, ctx, db)
	admin, err := userService.AuthenticateToken(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := userService.AuthenticateToken(ctx, listenerToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
		VALUES ('track-1', 'Christmas Time Is Here', 'Vince Guaraldi Trio', 'A Charlie Brown Christmas', 166);
		INSERT INTO music_playlists (id, name, owner_id, public, track_ids_json, track_count, duration_seconds)
		VALUES
		  ('admin-private', 'Admin Secret', ?, 0, '[]', 0, 0),
		  ('christmas', 'Christmas', ?, 1, '["track-1"]', 1, 166),
		  ('listener-private', 'Listener Secret', ?, 0, '[]', 0, 0)`,
		admin.User.ID, admin.User.ID, listener.User.ID); err != nil {
		t.Fatal(err)
	}

	catalogService := catalog.NewService(catalog.Seed{})
	reload := func(ctx context.Context) error {
		seed, err := catalog.LoadSeedFromDB(ctx, db)
		if err != nil {
			return err
		}
		catalogService.Replace(seed)
		return nil
	}
	if err := reload(ctx); err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{
		Catalog:       catalogService,
		Playlists:     playlists.New(db),
		ReloadCatalog: reload,
		Users:         userService,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/music/playlists", nil)
	req.Header.Set("Authorization", "Bearer "+listenerToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("Admin Secret")) {
		t.Fatalf("private playlist leaked in list: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Christmas")) || !bytes.Contains(rec.Body.Bytes(), []byte("Listener Secret")) {
		t.Fatalf("shared or owned playlist missing from list: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/music/playlists/admin-private", nil)
	req.Header.Set("Authorization", "Bearer "+listenerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("private get status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/music/playlists/christmas/tracks", nil)
	req.Header.Set("Authorization", "Bearer "+listenerToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("track-1")) {
		t.Fatalf("public tracks status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/music/playlists/christmas", bytes.NewBufferString(`{"public":false}`))
	req.Header.Set("Authorization", "Bearer "+listenerToken)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("shared edit status = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
}
