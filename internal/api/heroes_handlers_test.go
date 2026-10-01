package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/heroes"
	"github.com/bouliehaan/samo-server/internal/playback"
	"github.com/bouliehaan/samo-server/internal/storage/storagetest"
	"github.com/bouliehaan/samo-server/internal/users"
)

// The endpoint through the real server: a seeded catalog with the drop and a
// show, the caller's playback state in a real playback store, and the ranked
// cards out the other side.
func TestHomeHeroesRanksTheDropAndANewEpisode(t *testing.T) {
	ctx := t.Context()
	db := storagetest.Open(t)
	now := time.Now()
	at := func(d time.Duration) *time.Time {
		v := now.Add(d)
		return &v
	}

	episodes := []catalog.PodcastEpisode{
		{ID: "ep-new", PodcastID: "show-a", Title: "Mother Plants", DurationSeconds: 4320, PublishedAt: at(-2 * time.Hour)},
	}
	for i, id := range []string{"ep-1", "ep-2", "ep-3"} {
		episodes = append(episodes, catalog.PodcastEpisode{ID: id, PodcastID: "show-a", Title: id, PublishedAt: at(-time.Duration(30+i) * 24 * time.Hour)})
	}
	handler := NewServer(ServerOptions{
		Catalog: catalog.NewService(catalog.Seed{
			MusicTracks: []catalog.MusicTrack{
				{ID: "t1", Title: "Float On", DisplayArtist: "Modest Mouse", AlbumID: "al-1", DurationSeconds: 208, Images: []catalog.Image{{ID: "cover_a"}}, AddedAt: at(-2 * 24 * time.Hour)},
				{ID: "t2", Title: "Kids", DisplayArtist: "MGMT", AlbumID: "al-2", DurationSeconds: 303, Images: []catalog.Image{{ID: "cover_b"}}, AddedAt: at(-2 * 24 * time.Hour)},
			},
			MusicPlaylists: []catalog.MusicPlaylist{
				{ID: "pl-explore", Name: "Explore", System: true, TrackIDs: []string{"t1", "t2"}, TrackCount: 2},
			},
			Podcasts:        []catalog.PodcastItem{{ID: "show-a", Podcast: &catalog.PodcastMetadata{Title: "The Dude Grows Show"}}},
			PodcastEpisodes: episodes,
		}),
		Playback: playback.New(db),
	})

	// The playback store only records state for targets it can see.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, path) VALUES ('lib-1', 'Podcasts', 'podcast', 'samo://podcast-feeds');
		INSERT INTO podcasts (id, library_id, path) VALUES ('show-a', 'lib-1', 'samo://show');
		INSERT INTO podcast_episodes (id, library_id, podcast_id, title, enclosure_url) VALUES
		  ('ep-new', 'lib-1', 'show-a', 'Mother Plants', 'https://example.com/new.mp3'),
		  ('ep-1', 'lib-1', 'show-a', 'ep-1', 'https://example.com/1.mp3'),
		  ('ep-2', 'lib-1', 'show-a', 'ep-2', 'https://example.com/2.mp3'),
		  ('ep-3', 'lib-1', 'show-a', 'ep-3', 'https://example.com/3.mp3');
		INSERT INTO music_playlists (id, name, owner_id, public, track_ids_json, track_count, duration_seconds)
		VALUES ('pl-explore', 'Explore', 'user-explo', 0, '["t1","t2"]', 2, 511);
	`); err != nil {
		t.Fatal(err)
	}

	// The token-less test request resolves to the bootstrap admin; its
	// finished episodes are what make show-a one worth a card.
	states := playback.New(db)
	for _, id := range []string{"ep-1", "ep-2", "ep-3"} {
		if _, err := states.Put(ctx, users.BootstrapUserID, playback.TargetPodcastEpisode, id, playback.State{Completed: true, PlayCount: 1}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/home/heroes", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []heroes.Hero `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want the drop and the episode", body.Items)
	}
	byKind := map[heroes.Kind]heroes.Hero{}
	for _, hero := range body.Items {
		byKind[hero.Kind] = hero
	}
	drop, episode := byKind[heroes.KindExplore], byKind[heroes.KindEpisode]
	if drop.Eyebrow != "Fresh drop · 2 new this week" || drop.Sleeves[0].URL != "/api/v1/media/images/cover_a/image" {
		t.Fatalf("drop = %+v", drop)
	}
	if episode.Subtitle != "The Dude Grows Show" || episode.Target.ID != "ep-new" {
		t.Fatalf("episode = %+v", episode)
	}

	// A new visit remembers that Explore was just shown and promotes an alternative.
	if _, err := states.Put(ctx, users.BootstrapUserID, playback.TargetMusicPlaylist, "pl-explore", playback.State{PlayCount: 1, LastPlayedAt: at(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home/heroes?session=next&seen=%5B%22playlist%3Apl-explore%22%5D", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].Kind != heroes.KindEpisode || body.Items[1].Kind != heroes.KindExplore {
		t.Fatalf("after seeing Explore the episode leads: %+v", body.Items)
	}
}

func TestHomeHeroesIsEmptyNotAbsentWithNothingToSay(t *testing.T) {
	handler := NewServer(ServerOptions{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home/heroes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body != "{\"items\":[]}\n" && body != "{\"items\":[]}" {
		t.Fatalf("body = %q", body)
	}
}

func TestHomeHeroesFeaturesLibraryWithoutExplore(t *testing.T) {
	handler := NewServer(ServerOptions{Catalog: catalog.NewService(catalog.Seed{
		MusicAlbums: []catalog.MusicAlbum{{ID: "album", Title: "My record", TrackCount: 10}},
	})})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home/heroes?session=visit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []heroes.Hero `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Target.Type != "album" {
		t.Fatalf("items=%+v", body.Items)
	}
}

func TestHomeHeroesRejectsMalformedVisitHistory(t *testing.T) {
	handler := NewServer(ServerOptions{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/home/heroes?seen=garbage", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
