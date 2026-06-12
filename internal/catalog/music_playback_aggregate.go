package catalog

// OverlayMusicTracks applies per-track user playback onto list items.
func OverlayMusicTracks(items []MusicTrack, trackStates map[string]PlaybackState) {
	applyTrackPlaybackOverlay(items, trackStates)
}

// OverlayMusicAlbums merges direct album playback with rolled-up track stats.
// scopeTracks should contain only tracks relevant to the albums (e.g. one album
// or one artist's catalog); when nil, rollup from trackStates is skipped.
func OverlayMusicAlbums(
	items []MusicAlbum,
	albumStates map[string]PlaybackState,
	scopeTracks []MusicTrack,
	trackStates map[string]PlaybackState,
) {
	applyAlbumPlaybackOverlay(items, albumStates, scopeTracks, trackStates)
}

// OverlayMusicArtists merges direct artist playback with rolled-up track stats.
func OverlayMusicArtists(
	items []MusicArtist,
	artistStates map[string]PlaybackState,
	scopeTracks []MusicTrack,
	trackStates map[string]PlaybackState,
) {
	applyArtistPlaybackOverlay(items, artistStates, scopeTracks, trackStates)
}

// rollupTrackPlaybackToParents derives artist and album playback stats from
// per-track user playback so list/browse sorts reflect listening history even
// when only track rows were updated.
func rollupTrackPlaybackToParents(
	tracks []MusicTrack,
	trackStates map[string]PlaybackState,
) (map[string]PlaybackState, map[string]PlaybackState) {
	if len(trackStates) == 0 {
		return nil, nil
	}

	artistStates := make(map[string]PlaybackState)
	albumStates := make(map[string]PlaybackState)

	for _, track := range tracks {
		state, ok := trackStates[track.ID]
		if !ok {
			continue
		}

		if track.AlbumID != "" {
			albumStates[track.AlbumID] = accumulatePlaybackState(albumStates[track.AlbumID], state)
		}

		for _, artistID := range uniqueNonEmptyStrings(append(track.ArtistIDs, track.AlbumArtistIDs...)) {
			artistStates[artistID] = accumulatePlaybackState(artistStates[artistID], state)
		}
	}

	return artistStates, albumStates
}

func accumulatePlaybackState(current, add PlaybackState) PlaybackState {
	current.PlayCount += add.PlayCount
	current.SkipCount += add.SkipCount
	if add.LastPlayedAt != nil {
		if current.LastPlayedAt == nil || add.LastPlayedAt.After(*current.LastPlayedAt) {
			t := *add.LastPlayedAt
			current.LastPlayedAt = &t
		}
	}
	if add.LastPositionAt != nil {
		if current.LastPositionAt == nil || add.LastPositionAt.After(*current.LastPositionAt) {
			t := *add.LastPositionAt
			current.LastPositionAt = &t
		}
	}
	current.Favorite = current.Favorite || add.Favorite
	current.Starred = current.Starred || add.Starred
	if add.Rating > current.Rating {
		current.Rating = add.Rating
	}
	if add.StateUpdatedAt != nil {
		if current.StateUpdatedAt == nil || add.StateUpdatedAt.After(*current.StateUpdatedAt) {
			t := *add.StateUpdatedAt
			current.StateUpdatedAt = &t
		}
	}
	return current
}

func mergePlaybackStates(direct, rolled PlaybackState) PlaybackState {
	out := direct
	if rolled.PlayCount > out.PlayCount {
		out.PlayCount = rolled.PlayCount
	}
	if rolled.SkipCount > out.SkipCount {
		out.SkipCount = rolled.SkipCount
	}
	if rolled.LastPlayedAt != nil {
		if out.LastPlayedAt == nil || rolled.LastPlayedAt.After(*out.LastPlayedAt) {
			t := *rolled.LastPlayedAt
			out.LastPlayedAt = &t
		}
	}
	if rolled.LastPositionAt != nil {
		if out.LastPositionAt == nil || rolled.LastPositionAt.After(*out.LastPositionAt) {
			t := *rolled.LastPositionAt
			out.LastPositionAt = &t
		}
	}
	out.Favorite = out.Favorite || rolled.Favorite
	out.Starred = out.Starred || rolled.Starred
	if rolled.Rating > out.Rating {
		out.Rating = rolled.Rating
	}
	if rolled.StateUpdatedAt != nil {
		if out.StateUpdatedAt == nil || rolled.StateUpdatedAt.After(*out.StateUpdatedAt) {
			t := *rolled.StateUpdatedAt
			out.StateUpdatedAt = &t
		}
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func playbackStateIsEmpty(state PlaybackState) bool {
	return state.PlayCount == 0 &&
		state.SkipCount == 0 &&
		state.Rating == 0 &&
		!state.Favorite &&
		!state.Starred &&
		!state.Completed &&
		state.ProgressSeconds == 0 &&
		state.LastPlayedAt == nil &&
		state.LastPositionAt == nil
}
