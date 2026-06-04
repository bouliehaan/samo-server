package catalog

import (
	"testing"
	"time"
)

// TestListUpdatedSinceFiltersChangedRows verifies the delta filter: a non-zero
// UpdatedSince returns only rows updated at/after it (inclusive boundary), a
// nil UpdatedAt is always included, and a zero UpdatedSince is a no-op.
func TestListUpdatedSinceFiltersChangedRows(t *testing.T) {
	since := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	older := since.Add(-time.Hour)
	newer := since.Add(time.Hour)

	service := NewService(Seed{
		MusicAlbums: []MusicAlbum{
			{ID: "old", Title: "A", UpdatedAt: timePtr(older)},
			{ID: "boundary", Title: "B", UpdatedAt: timePtr(since)},
			{ID: "new", Title: "C", UpdatedAt: timePtr(newer)},
			{ID: "untracked", Title: "D", UpdatedAt: nil},
		},
	})

	// Zero UpdatedSince = full list (unchanged behavior).
	full := service.ListMusicAlbumsSorted(MusicListOptions{Page: PageRequest{Limit: 100}})
	if full.Total != 4 {
		t.Fatalf("full total = %d, want 4", full.Total)
	}

	delta := service.ListMusicAlbumsSorted(MusicListOptions{Page: PageRequest{Limit: 100, UpdatedSince: since}})
	got := map[string]bool{}
	for _, item := range delta.Items {
		got[item.ID] = true
	}
	if delta.Total != 3 {
		t.Fatalf("delta total = %d, want 3 (boundary,new,untracked)", delta.Total)
	}
	if got["old"] {
		t.Fatalf("delta included %q updated before the watermark", "old")
	}
	for _, want := range []string{"boundary", "new", "untracked"} {
		if !got[want] {
			t.Fatalf("delta missing %q", want)
		}
	}
}

// TestListUpdatedSinceAcrossTypes spot-checks the filter on the other entity
// list endpoints the client mirrors, so a delta sync of every type is covered.
func TestListUpdatedSinceAcrossTypes(t *testing.T) {
	since := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	older := since.Add(-time.Hour)
	newer := since.Add(time.Hour)

	service := NewService(Seed{
		MusicArtists: []MusicArtist{
			{ID: "artist-old", Name: "A", UpdatedAt: timePtr(older)},
			{ID: "artist-new", Name: "B", UpdatedAt: timePtr(newer)},
		},
		MusicTracks: []MusicTrack{
			{ID: "track-old", Title: "A", UpdatedAt: timePtr(older)},
			{ID: "track-new", Title: "B", UpdatedAt: timePtr(newer)},
		},
		MusicPlaylists: []MusicPlaylist{
			{ID: "pl-old", Name: "A", Public: true, UpdatedAt: timePtr(older)},
			{ID: "pl-new", Name: "B", Public: true, UpdatedAt: timePtr(newer)},
		},
		Audiobooks: []AudiobookItem{
			{ID: "book-old", Book: &BookMetadata{Title: "A"}, UpdatedAt: timePtr(older)},
			{ID: "book-new", Book: &BookMetadata{Title: "B"}, UpdatedAt: timePtr(newer)},
		},
		Podcasts: []PodcastItem{
			{ID: "pod-old", Podcast: &PodcastMetadata{Title: "A"}, UpdatedAt: timePtr(older)},
			{ID: "pod-new", Podcast: &PodcastMetadata{Title: "B"}, UpdatedAt: timePtr(newer)},
		},
		PodcastEpisodes: []PodcastEpisode{
			{ID: "ep-old", PodcastID: "pod-old", Title: "A", UpdatedAt: timePtr(older)},
			{ID: "ep-new", PodcastID: "pod-new", Title: "B", UpdatedAt: timePtr(newer)},
		},
	})

	page := PageRequest{Limit: 100, UpdatedSince: since}

	assertOnlyNew := func(name string, ids []string) {
		t.Helper()
		if len(ids) != 1 {
			t.Fatalf("%s delta returned %d ids, want 1: %v", name, len(ids), ids)
		}
	}

	artistIDs := idsOf(service.ListMusicArtistsSorted(MusicListOptions{Page: page}).Items, func(a MusicArtist) string { return a.ID })
	assertOnlyNew("artists", artistIDs)
	if artistIDs[0] != "artist-new" {
		t.Fatalf("artists delta = %v, want [artist-new]", artistIDs)
	}

	trackIDs := idsOf(service.ListMusicTracksSorted(MusicListOptions{Page: page}).Items, func(tr MusicTrack) string { return tr.ID })
	assertOnlyNew("tracks", trackIDs)

	playlistIDs := idsOf(service.ListMusicPlaylistsForUser("user-1", page).Items, func(p MusicPlaylist) string { return p.ID })
	assertOnlyNew("playlists", playlistIDs)

	bookIDs := idsOf(service.ListAudiobooks(page).Items, func(a AudiobookItem) string { return a.ID })
	assertOnlyNew("audiobooks", bookIDs)

	podIDs := idsOf(service.ListPodcasts(page).Items, func(p PodcastItem) string { return p.ID })
	assertOnlyNew("podcasts", podIDs)

	epIDs := idsOf(service.ListPodcastEpisodes(page).Items, func(e PodcastEpisode) string { return e.ID })
	assertOnlyNew("episodes", epIDs)
}

// TestSyncManifest verifies the manifest lists every current ID, scopes
// playlists to the caller's visibility, and stamps a recent second-granular
// server time.
func TestSyncManifest(t *testing.T) {
	service := NewService(Seed{
		MusicArtists: []MusicArtist{{ID: "artist-1", Name: "A"}},
		MusicAlbums:  []MusicAlbum{{ID: "album-1", Title: "A"}, {ID: "album-2", Title: "B"}},
		MusicTracks:  []MusicTrack{{ID: "track-1", Title: "A"}},
		MusicPlaylists: []MusicPlaylist{
			{ID: "pl-public", Name: "Public", Public: true},
			{ID: "pl-mine", Name: "Mine", OwnerID: "user-1"},
			{ID: "pl-theirs", Name: "Theirs", OwnerID: "user-2"},
		},
		Audiobooks:      []AudiobookItem{{ID: "book-1", Book: &BookMetadata{Title: "A"}}},
		Podcasts:        []PodcastItem{{ID: "pod-1", Podcast: &PodcastMetadata{Title: "A"}}},
		PodcastEpisodes: []PodcastEpisode{{ID: "ep-1", PodcastID: "pod-1", Title: "A"}},
	})

	manifest := service.SyncManifest("user-1")

	if len(manifest.IDs.Albums) != 2 {
		t.Fatalf("albums = %v, want 2", manifest.IDs.Albums)
	}
	if len(manifest.IDs.Artists) != 1 || len(manifest.IDs.Tracks) != 1 ||
		len(manifest.IDs.Audiobooks) != 1 || len(manifest.IDs.Podcasts) != 1 ||
		len(manifest.IDs.Episodes) != 1 {
		t.Fatalf("unexpected id counts: %+v", manifest.IDs)
	}

	// user-1 sees the public playlist and their own, never user-2's.
	playlists := map[string]bool{}
	for _, id := range manifest.IDs.Playlists {
		playlists[id] = true
	}
	if !playlists["pl-public"] || !playlists["pl-mine"] {
		t.Fatalf("user-1 manifest playlists = %v, want pl-public + pl-mine", manifest.IDs.Playlists)
	}
	if playlists["pl-theirs"] {
		t.Fatalf("user-1 manifest leaked another user's private playlist: %v", manifest.IDs.Playlists)
	}
	if other := service.SyncManifest("user-2"); len(other.IDs.Playlists) != 2 {
		t.Fatalf("user-2 manifest playlists = %v, want pl-public + pl-theirs", other.IDs.Playlists)
	}

	if manifest.Counts["albums"] != 2 {
		t.Fatalf("counts.albums = %d, want 2", manifest.Counts["albums"])
	}
	if manifest.ServerTime.IsZero() {
		t.Fatal("serverTime is zero")
	}
	if !manifest.ServerTime.Equal(manifest.ServerTime.Truncate(time.Second)) {
		t.Fatalf("serverTime %v is not truncated to whole seconds", manifest.ServerTime)
	}
	if time.Since(manifest.ServerTime) > time.Minute {
		t.Fatalf("serverTime %v is not recent", manifest.ServerTime)
	}
}
