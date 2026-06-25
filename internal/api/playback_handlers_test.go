package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestPlaybackAPIPatch(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, t.TempDir()+"/samo.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.ApplyMigrations(ctx, db, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Signal One', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(ServerOptions{Playback: playback.New(db)})
	body, _ := json.Marshal(playback.PatchInput{ProgressSeconds: intPtr(30), Favorite: boolPtr(true)})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/playback/music-track/track-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }
