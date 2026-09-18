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

// AlbumTracksAsSeen is the one definition of "the tracks of this album" for
// every surface that reaches an album by id: the album detail, add to
// playlist, play / queue / play-next an album, Subsonic getAlbum, the album
// playback overlay. tracks must be every track carrying the album's id.
//
// An album with at least one library track is a library album, and an explo
// track sharing its id is a stray: the drop-folder twin of a kept copy
// (Keep remuxes samo's identified tags into the copy, so the scanner files
// it under the very album the drop's tags resolved to), or a drop tagged to
// match a release the library already owns. The global lists already hide
// it; dropping it here is what makes the by-id view agree with them —
// without this, an album whose detail showed one track handed two to
// "Add to playlist" (2026-09-17), the second being the twin that rotation
// deletes the following week.
//
// An album with no library track IS explo content, reached only on purpose
// from the Explo tab or the Explore playlist, and shows all of its tracks.
func AlbumTracksAsSeen(tracks []MusicTrack) []MusicTrack {
	for _, track := range tracks {
		if !track.IsExplo {
			return WithoutExploTracks(tracks)
		}
	}
	return tracks
}

// RecountMixedAlbums re-derives TrackCount and DurationSeconds for albums
// whose id is shared by explo and library tracks, from the tracks as seen.
// The scanner's denormalized counts include every row with the album id, so
// a library album with a stray twin advertised one track more than any
// surface would list. Albums without strays keep their stored values.
func RecountMixedAlbums(seed *Seed) {
	if seed == nil || len(seed.MusicAlbums) == 0 || len(seed.MusicTracks) == 0 {
		return
	}
	type tally struct {
		library, explo, duration int
	}
	tallies := make(map[string]*tally)
	for _, track := range seed.MusicTracks {
		if track.AlbumID == "" {
			continue
		}
		entry := tallies[track.AlbumID]
		if entry == nil {
			entry = &tally{}
			tallies[track.AlbumID] = entry
		}
		if track.IsExplo {
			entry.explo++
			continue
		}
		entry.library++
		entry.duration += track.DurationSeconds
	}
	for index := range seed.MusicAlbums {
		entry := tallies[seed.MusicAlbums[index].ID]
		if entry == nil || entry.explo == 0 || entry.library == 0 {
			continue
		}
		seed.MusicAlbums[index].TrackCount = entry.library
		seed.MusicAlbums[index].DurationSeconds = entry.duration
	}
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
