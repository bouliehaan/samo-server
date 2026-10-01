package catalog

import "testing"

func TestPlaylistUpsertEnrichesArtworkLikeFullLoad(t *testing.T) {
	service := NewService(Seed{MusicTracks: []MusicTrack{
		{ID: "one", Images: []Image{{ID: "cover_one"}}},
		{ID: "two", Images: []Image{{ID: "cover_two"}}},
	}})
	playlist := MusicPlaylist{ID: "pl", Name: "Mix", TrackIDs: []string{"one", "two"}}
	service.UpsertMusicPlaylist(playlist)
	detail, err := service.MusicPlaylist("pl")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Images) != 4 {
		t.Fatalf("incremental detail lost grid: %#v", detail.Images)
	}
	// Reordering must update the generated image order, without a full reload.
	playlist.TrackIDs = []string{"two", "one"}
	service.UpsertMusicPlaylist(playlist)
	detail, _ = service.MusicPlaylist("pl")
	if detail.Images[0].ID != "cover_two" {
		t.Fatalf("stale grid: %#v", detail.Images)
	}
	if got := service.MusicPlaylistCoverImages("pl"); len(got) != 4 || got[0].ID != "cover_two" {
		t.Fatalf("cover endpoint differs: %#v", got)
	}
}
