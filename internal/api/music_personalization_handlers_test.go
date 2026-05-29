package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage"
	"github.com/bouliehaan/samo-server/migrations"
)

func TestMusicDetailEndpointsOverlayUserPlayback(t *testing.T) {
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
		INSERT INTO music_tracks (id, title, playback_json, added_at, updated_at)
		VALUES ('track-1', 'Song', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}

	userService, _, userToken := testUserServiceWithTokens(t, ctx, db)
	listener, err := userService.AuthenticateCredentials(ctx, "listener", "listener-pass")
	if err != nil {
		t.Fatal(err)
	}
	catalogService := catalog.NewService(catalog.Seed{
		MusicArtists: []catalog.MusicArtist{{ID: "artist-1", Name: "Artist"}},
		MusicAlbums: []catalog.MusicAlbum{
			{ID: "album-1", Title: "Album", AlbumArtistIDs: []string{"artist-1"}, TrackCount: 1},
		},
		MusicTracks: []catalog.MusicTrack{
			{ID: "track-1", Title: "Song", AlbumID: "album-1", ArtistIDs: []string{"artist-1"}},
		},
	})
	playbackService := playback.New(db)
	handler := NewServer(ServerOptions{
		Catalog:  catalogService,
		Playback: playbackService,
		Users:    userService,
	})

	if _, err := playbackService.Patch(ctx, listener.User.ID, playback.TargetMusicTrack, "track-1", playback.PatchInput{
		IncrementPlayCount: true,
		TouchLastPlayedAt:  true,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/music/artists/artist-1", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("artist status = %d body=%s", rec.Code, rec.Body.String())
	}
	var artist catalog.MusicArtist
	if err := json.Unmarshal(rec.Body.Bytes(), &artist); err != nil {
		t.Fatal(err)
	}
	if artist.Playback.PlayCount != 1 {
		t.Fatalf("artist playCount = %d, want 1", artist.Playback.PlayCount)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/music/albums/album-1/tracks", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("album tracks status = %d body=%s", rec.Code, rec.Body.String())
	}
	var tracksBody struct {
		Items []catalog.MusicTrack `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tracksBody); err != nil {
		t.Fatal(err)
	}
	if len(tracksBody.Items) != 1 || tracksBody.Items[0].Playback.PlayCount != 1 {
		t.Fatalf("tracks = %#v", tracksBody.Items)
	}
}
