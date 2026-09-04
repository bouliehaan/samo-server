package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/playlists"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestPlaylistImportEndpointRebuildsFromLocalTracks(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
		VALUES ('track-1', 'Ceremony', 'New Order', 'Substance', 263)`); err != nil {
		t.Fatal(err)
	}

	catalogService := catalog.NewService(catalog.Seed{})
	reload := func(ctx context.Context) error {
		seed, err := catalogstore.LoadSeedFromDB(ctx, db)
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
	db := storagetest.Open(t)
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
		VALUES ('track-1', 'Christmas Time Is Here', 'Vince Guaraldi Trio', 'A Charlie Brown Christmas', 166)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
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
		seed, err := catalogstore.LoadSeedFromDB(ctx, db)
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

// Walking the offsets must yield each track exactly once.
//
// This route took `limit` and `offset` and ignored both, returning the whole
// playlist for every page. Both clients walk it — collectSamoPages in the
// TypeScript core, fetchAllPages on Android — so opening a 1,095-track playlist
// fetched it three times and concatenated the result into 3,285 rows with every
// track repeated three times.
func TestPlaylistTracksHonoursLimitAndOffset(t *testing.T) {
	ctx := context.Background()
	db := storagetest.Open(t)

	const total = 7
	lines := ""
	for i := 1; i <= total; i++ {
		id := fmt.Sprintf("track-%d", i)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO music_tracks (id, title, display_artist, album_title, duration_seconds)
			VALUES (?, ?, 'New Order', 'Substance', 200)`, id, fmt.Sprintf("Song %d", i)); err != nil {
			t.Fatal(err)
		}
		lines += fmt.Sprintf("New Order - Song %d\\n", i)
	}

	catalogService := catalog.NewService(catalog.Seed{})
	reload := func(ctx context.Context) error {
		seed, err := catalogstore.LoadSeedFromDB(ctx, db)
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/music/playlists/import",
		bytes.NewBufferString(`{"name":"Paged","sourceType":"plain","content":"`+lines+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%s", rec.Code, rec.Body.String())
	}
	var imported playlists.ImportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Playlist == nil || imported.MatchedCount != total {
		t.Fatalf("import matched %d of %d", imported.MatchedCount, total)
	}

	type page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	fetch := func(query string) page {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/music/playlists/"+imported.Playlist.ID+"/tracks"+query, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("tracks%s status = %d body=%s", query, rec.Code, rec.Body.String())
		}
		var decoded page
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	// The walk a client actually performs: pages of 3 until the list runs out.
	seen := map[string]int{}
	pages := 0
	for offset := 0; offset < total; offset += 3 {
		got := fetch(fmt.Sprintf("?limit=3&offset=%d", offset))
		pages++
		if got.Total != total {
			t.Errorf("offset %d: total = %d, want %d (the list, not the page)", offset, got.Total, total)
		}
		want := 3
		if offset+3 > total {
			want = total - offset
		}
		if len(got.Items) != want {
			t.Errorf("offset %d: %d items, want %d", offset, len(got.Items), want)
		}
		for _, item := range got.Items {
			seen[item.ID]++
		}
	}
	if pages != 3 {
		t.Fatalf("walked %d pages, want 3", pages)
	}
	if len(seen) != total {
		t.Errorf("walk collected %d distinct tracks, want %d", len(seen), total)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("%s appeared %d times across the walk, want once", id, count)
		}
	}

	// A caller that asks for no page still gets the whole playlist.
	if all := fetch(""); len(all.Items) != total || all.Total != total {
		t.Errorf("unpaged request returned %d items (total %d), want %d of %d",
			len(all.Items), all.Total, total, total)
	}
}
