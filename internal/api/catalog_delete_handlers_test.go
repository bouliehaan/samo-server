package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestDeleteMusicAlbumRequiresAdmin(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	userService, adminToken, _ := testUserServiceWithTokens(t, ctx, db)

	albumID := "album_delete"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_albums (id, title, display_artist)
		VALUES (?, 'Gone', 'Artist')`, albumID); err != nil {
		t.Fatal(err)
	}

	reloadCalled := false
	handler := NewServer(ServerOptions{
		DB:      db,
		Catalog: catalog.NewService(catalog.Seed{}),
		Users:   userService,
		ReloadCatalog: func(context.Context) error {
			reloadCalled = true
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/music/albums/"+albumID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !reloadCalled {
		t.Fatal("expected catalog reload after delete")
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_albums WHERE id = ?`, albumID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("album count = %d, want 0", count)
	}
}

func TestDeleteMusicAlbumRejectsNonAdmin(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "samo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}

	userService, _, userToken := testUserServiceWithTokens(t, ctx, db)
	handler := NewServer(ServerOptions{
		DB:      db,
		Catalog: catalog.NewService(catalog.Seed{}),
		Users:   userService,
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/music/albums/album_x", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}
