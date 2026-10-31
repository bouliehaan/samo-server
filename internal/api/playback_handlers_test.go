package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/catalogstore"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
)

func TestPlaybackAPIPatch(t *testing.T) {
	ctx := t.Context()
	db := storagetest.Open(t)
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

// The renderer sent Jellyfin ticks as seconds on every pause and seek. A
// position past the end of a music track is dropped; a real one still saves.
func TestPlaybackPatchDropsMusicPositionPastTheEnd(t *testing.T) {
	ctx := t.Context()
	db := storagetest.Open(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO music_tracks (id, title, duration_seconds, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Home with No Delay', 322, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	seed, err := catalogstore.LoadSeedFromDB(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewServer(ServerOptions{Catalog: catalog.NewService(seed), Playback: playback.New(db)})

	patch := func(progress int) playback.State {
		body, _ := json.Marshal(playback.PatchInput{ProgressSeconds: intPtr(progress)})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/playback/music-track/track-1", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("progress %d: status = %d body=%s", progress, rec.Code, rec.Body.String())
		}
		var state playback.State
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	if got := patch(120).ProgressSeconds; got != 120 {
		t.Fatalf("real position: progress = %d, want 120", got)
	}
	if got := patch(37_550_000).ProgressSeconds; got != 120 {
		t.Fatalf("ticks-sized position: progress = %d, want the previous 120 kept", got)
	}
}
