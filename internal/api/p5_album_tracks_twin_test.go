package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// P5 (2026-09-17): every client — the Android add-to-playlist path, the
// desktop controller, the mobile detail fallback, Subsonic — enumerates an
// album through this one endpoint, so this is where "a drop-folder twin
// cannot leak through add-to-playlist / play album / queue" is pinned.
func TestP5AlbumTracksEndpointHidesDropFolderTwin(t *testing.T) {
	handler := catalogTestServer(t, catalog.Seed{
		MusicArtists: []catalog.MusicArtist{{ID: "artist-1", Name: "Radiohead"}},
		MusicAlbums: []catalog.MusicAlbum{
			{ID: "album-mixed", Title: "Pablo Honey", AlbumArtistIDs: []string{"artist-1"}, TrackCount: 2},
			{ID: "album-explo", Title: "Weekly Drop", IsExplo: true, HiddenFromRecentlyAdded: true, TrackCount: 1},
		},
		MusicTracks: []catalog.MusicTrack{
			{ID: "track-kept", Title: "Creep", AlbumID: "album-mixed", ArtistIDs: []string{"artist-1"},
				AudioFiles: []catalog.AudioFile{{ID: "file-kept", Path: "/music/Radiohead/Pablo Honey/02 - Creep.mp3"}}},
			{ID: "track-twin", Title: "Creep", AlbumID: "album-mixed", ArtistIDs: []string{"artist-1"}, IsExplo: true,
				AudioFiles: []catalog.AudioFile{{ID: "file-twin", Path: "/music/explo/Weekly-Exploration/creep.mp3"}}},
			{ID: "track-drop", Title: "Something Else", AlbumID: "album-explo", IsExplo: true,
				AudioFiles: []catalog.AudioFile{{ID: "file-drop", Path: "/music/explo/Weekly-Exploration/something.mp3"}}},
		},
	})

	albumTracks := func(albumID string) []catalog.MusicTrack {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/music/albums/"+albumID+"/tracks", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET album tracks %s: status = %d body=%s", albumID, rec.Code, rec.Body.String())
		}
		var body struct {
			Items []catalog.MusicTrack `json:"items"`
			Total int                  `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Total != len(body.Items) {
			t.Fatalf("total = %d, items = %d", body.Total, len(body.Items))
		}
		return body.Items
	}

	// The library album lists its kept copy only — what its detail card shows.
	got := albumTracks("album-mixed")
	if len(got) != 1 || got[0].ID != "track-kept" {
		ids := make([]string, 0, len(got))
		for _, track := range got {
			ids = append(ids, track.ID)
		}
		t.Fatalf("mixed album tracks = %v, want [track-kept]: the drop-folder twin leaked into add-to-playlist", ids)
	}

	// The explo album, reached on purpose from Explore, still lists its drop.
	if got := albumTracks("album-explo"); len(got) != 1 || got[0].ID != "track-drop" {
		t.Fatalf("explo album tracks = %#v, want its one drop", got)
	}

	// The twin itself stays resolvable by id — the Explore playlist points at it.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/music/tracks/track-twin", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET twin by id: status = %d, want 200 (by-id resolution must survive)", rec.Code)
	}

	// And the global list — the Android mirror's source — agrees with the album view.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/music/tracks?limit=100", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tracks: status = %d", rec.Code)
	}
	var list struct {
		Items []catalog.MusicTrack `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, track := range list.Items {
		if track.IsExplo || track.ID == "track-twin" || track.ID == "track-drop" {
			t.Fatalf("explo track %s leaked into the global list", track.ID)
		}
	}
	if len(list.Items) != 1 || list.Items[0].ID != "track-kept" {
		t.Fatalf("global list = %#v, want only the kept copy", list.Items)
	}
}
