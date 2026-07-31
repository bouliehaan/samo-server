package catalog

// This file is the catalog side of the explo silo. The explo reconciler
// maintains music_tracks.is_explo (and the album-level flag) from the
// configured drop folder(s); here those persisted facts are projected onto
// artists and enforced on the default listing surfaces.
//
// The rule everywhere: explo content is excluded from anything that LISTS
// the library (browse, sorted lists, search, artist relations, overview
// counts) but stays fully resolvable by ID and fully present in the sync
// manifest, so the Explo tab, the Explore playlist, and client-built explo
// shelves keep working.

// DeriveExploArtists computes MusicArtist.IsExplo for a freshly loaded seed:
// an artist is explo iff they have at least one attributable track and every
// attributable track is explo. Attribution covers both track credits
// (track.ArtistIDs) and album-artist credits (the album's AlbumArtistIDs),
// so an artist credited only at the album level is still counted. Derived
// here, not stored: the track flags are the single source of truth and this
// stays consistent with them by construction.
func DeriveExploArtists(seed *Seed) {
	if seed == nil || len(seed.MusicArtists) == 0 {
		return
	}
	albumArtistIDs := make(map[string][]string, len(seed.MusicAlbums))
	for _, album := range seed.MusicAlbums {
		if len(album.AlbumArtistIDs) > 0 {
			albumArtistIDs[album.ID] = album.AlbumArtistIDs
		}
	}

	type tally struct{ total, explo int }
	tallies := make(map[string]*tally, len(seed.MusicArtists))
	count := func(artistID string, isExplo bool) {
		if artistID == "" {
			return
		}
		entry := tallies[artistID]
		if entry == nil {
			entry = &tally{}
			tallies[artistID] = entry
		}
		entry.total++
		if isExplo {
			entry.explo++
		}
	}
	for _, track := range seed.MusicTracks {
		seen := map[string]struct{}{}
		for _, id := range track.ArtistIDs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			count(id, track.IsExplo)
		}
		for _, id := range albumArtistIDs[track.AlbumID] {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			count(id, track.IsExplo)
		}
	}

	for index := range seed.MusicArtists {
		entry := tallies[seed.MusicArtists[index].ID]
		seed.MusicArtists[index].IsExplo = entry != nil && entry.total > 0 && entry.explo == entry.total
	}
}

// WithoutExploArtists returns items minus explo-only artists. Allocation-free
// when nothing is explo (the common case): the original slice is returned.
func WithoutExploArtists(items []MusicArtist) []MusicArtist {
	return withoutExplo(items, func(a MusicArtist) bool { return a.IsExplo })
}

// WithoutExploAlbums returns items minus fully-explo albums.
func WithoutExploAlbums(items []MusicAlbum) []MusicAlbum {
	return withoutExplo(items, func(a MusicAlbum) bool { return a.IsExplo })
}

// WithoutExploTracks returns items minus explo tracks.
func WithoutExploTracks(items []MusicTrack) []MusicTrack {
	return withoutExplo(items, func(t MusicTrack) bool { return t.IsExplo })
}

func withoutExplo[T any](items []T, isExplo func(T) bool) []T {
	for index := range items {
		if !isExplo(items[index]) {
			continue
		}
		// First explo item found — copy the prefix and filter the rest.
		filtered := make([]T, 0, len(items)-1)
		filtered = append(filtered, items[:index]...)
		for _, item := range items[index+1:] {
			if !isExplo(item) {
				filtered = append(filtered, item)
			}
		}
		return filtered
	}
	return items
}
