package catalog

import (
	"sort"
	"strings"
	"time"
)

const (
	MusicListSortAZ         = "az"
	MusicListSortRecent     = "recent"
	MusicListSortRelease    = "release"
	MusicListSortPlayCount  = "playCount"
	MusicListSortLastPlayed = "lastPlayed"
	SortDirectionAsc        = "asc"
	SortDirectionDesc       = "desc"
)

// MusicListPlaybackOverlay supplies per-user playback stats for list sorting
// and filtering. Without it, list items keep empty Playback and playCount sorts
// are meaningless.
type MusicListPlaybackOverlay struct {
	TrackStates  map[string]PlaybackState
	AlbumStates  map[string]PlaybackState
	ArtistStates map[string]PlaybackState
}

type MusicListOptions struct {
	Page      PageRequest
	Sort      string
	Direction string
	Playback  MusicListPlaybackOverlay
}

func (s *Service) ListMusicArtistsSorted(options MusicListOptions) Page[MusicArtist] {
	s.mu.RLock()
	items := append([]MusicArtist(nil), s.musicArtists...)
	var tracks []MusicTrack
	if len(options.Playback.TrackStates) > 0 {
		tracks = append([]MusicTrack(nil), s.musicTracks...)
	}
	s.mu.RUnlock()

	items = filterUpdatedSince(items, options.Page.UpdatedSince, func(a MusicArtist) *time.Time { return a.UpdatedAt })
	applyArtistPlaybackOverlay(items, options.Playback.ArtistStates, tracks, options.Playback.TrackStates)
	sortMusicArtistList(items, options)
	return paginate(items, options.Page)
}

func (s *Service) ListMusicAlbumsSorted(options MusicListOptions) Page[MusicAlbum] {
	s.mu.RLock()
	items := append([]MusicAlbum(nil), s.musicAlbums...)
	var tracks []MusicTrack
	if len(options.Playback.TrackStates) > 0 {
		tracks = append([]MusicTrack(nil), s.musicTracks...)
	}
	s.mu.RUnlock()

	items = filterUpdatedSince(items, options.Page.UpdatedSince, func(a MusicAlbum) *time.Time { return a.UpdatedAt })
	applyAlbumPlaybackOverlay(items, options.Playback.AlbumStates, tracks, options.Playback.TrackStates)
	sortMusicAlbumList(items, options)
	return paginate(items, options.Page)
}

func (s *Service) ListMusicTracksSorted(options MusicListOptions) Page[MusicTrack] {
	s.mu.RLock()
	items := append([]MusicTrack(nil), s.musicTracks...)
	s.mu.RUnlock()

	items = filterUpdatedSince(items, options.Page.UpdatedSince, func(t MusicTrack) *time.Time { return t.UpdatedAt })
	applyTrackPlaybackOverlay(items, options.Playback.TrackStates)
	sortMusicTrackList(items, options)
	return paginate(items, options.Page)
}

func applyArtistPlaybackOverlay(
	items []MusicArtist,
	states map[string]PlaybackState,
	tracks []MusicTrack,
	trackStates map[string]PlaybackState,
) {
	if len(states) == 0 && len(trackStates) == 0 {
		return
	}
	rolledArtists, _ := rollupTrackPlaybackToParents(tracks, trackStates)
	for index := range items {
		id := items[index].ID
		state := states[id]
		if rolled, ok := rolledArtists[id]; ok {
			state = mergePlaybackStates(state, rolled)
		}
		if !playbackStateIsEmpty(state) {
			items[index].Playback = state
		}
	}
}

func applyAlbumPlaybackOverlay(
	items []MusicAlbum,
	states map[string]PlaybackState,
	tracks []MusicTrack,
	trackStates map[string]PlaybackState,
) {
	if len(states) == 0 && len(trackStates) == 0 {
		return
	}
	_, rolledAlbums := rollupTrackPlaybackToParents(tracks, trackStates)
	for index := range items {
		id := items[index].ID
		state := states[id]
		if rolled, ok := rolledAlbums[id]; ok {
			state = mergePlaybackStates(state, rolled)
		}
		if !playbackStateIsEmpty(state) {
			items[index].Playback = state
		}
	}
}

func applyTrackPlaybackOverlay(items []MusicTrack, states map[string]PlaybackState) {
	if len(states) == 0 {
		return
	}
	for index := range items {
		if state, ok := states[items[index].ID]; ok {
			items[index].Playback = state
		}
	}
}

func sortMusicArtistList(items []MusicArtist, options MusicListOptions) {
	sortBy := normalizeMusicListSort(options.Sort)
	desc := normalizeSortDirection(options.Direction) == SortDirectionDesc
	sort.SliceStable(items, func(i, j int) bool {
		switch sortBy {
		case MusicListSortRecent:
			if cmp := compareOptionalTimes(items[i].AddedAt, items[j].AddedAt, desc); cmp != 0 {
				return cmp < 0
			}
		case MusicListSortPlayCount:
			left := items[i].Playback.PlayCount
			right := items[j].Playback.PlayCount
			if left != right {
				if desc {
					return left > right
				}
				return left < right
			}
		case MusicListSortLastPlayed:
			if cmp := compareOptionalTimes(items[i].Playback.LastPlayedAt, items[j].Playback.LastPlayedAt, desc); cmp != 0 {
				return cmp < 0
			}
		default:
		}
		return compareText(firstNonEmpty(items[i].SortName, items[i].Name), firstNonEmpty(items[j].SortName, items[j].Name), desc) < 0
	})
}

func sortMusicAlbumList(items []MusicAlbum, options MusicListOptions) {
	sortBy := normalizeMusicListSort(options.Sort)
	desc := normalizeSortDirection(options.Direction) == SortDirectionDesc
	sort.SliceStable(items, func(i, j int) bool {
		switch sortBy {
		case MusicListSortRecent:
			if cmp := compareOptionalTimes(items[i].AddedAt, items[j].AddedAt, desc); cmp != 0 {
				return cmp < 0
			}
		case MusicListSortRelease:
			if cmp := compareReleaseSortKeys(
				albumReleaseSortKey(items[i]),
				albumReleaseSortKey(items[j]),
				desc,
			); cmp != 0 {
				return cmp < 0
			}
		case MusicListSortPlayCount:
			left := items[i].Playback.PlayCount
			right := items[j].Playback.PlayCount
			if left != right {
				if desc {
					return left > right
				}
				return left < right
			}
		case MusicListSortLastPlayed:
			if cmp := compareOptionalTimes(items[i].Playback.LastPlayedAt, items[j].Playback.LastPlayedAt, desc); cmp != 0 {
				return cmp < 0
			}
		default:
		}
		return compareText(firstNonEmpty(items[i].SortTitle, items[i].Title), firstNonEmpty(items[j].SortTitle, items[j].Title), desc) < 0
	})
}

func sortMusicTrackList(items []MusicTrack, options MusicListOptions) {
	sortBy := normalizeMusicListSort(options.Sort)
	desc := normalizeSortDirection(options.Direction) == SortDirectionDesc
	sort.SliceStable(items, func(i, j int) bool {
		switch sortBy {
		case MusicListSortRecent:
			if cmp := compareOptionalTimes(items[i].AddedAt, items[j].AddedAt, desc); cmp != 0 {
				return cmp < 0
			}
		case MusicListSortPlayCount:
			left := items[i].Playback.PlayCount
			right := items[j].Playback.PlayCount
			if left != right {
				if desc {
					return left > right
				}
				return left < right
			}
		case MusicListSortLastPlayed:
			if cmp := compareOptionalTimes(items[i].Playback.LastPlayedAt, items[j].Playback.LastPlayedAt, desc); cmp != 0 {
				return cmp < 0
			}
		default:
		}
		return compareText(firstNonEmpty(items[i].SortTitle, items[i].Title), firstNonEmpty(items[j].SortTitle, items[j].Title), desc) < 0
	})
}

func normalizeMusicListSort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "recent", "recents", "added", "added_at", "addedat":
		return MusicListSortRecent
	case "release", "release_date", "releasedate", "year", "newest":
		return MusicListSortRelease
	case "playcount", "play_count", "plays", "most_played", "mostplayed":
		return MusicListSortPlayCount
	case "lastplayed", "last_played", "lastplayedat", "played", "recentlyplayed", "recently_played":
		return MusicListSortLastPlayed
	case "az", "a-z", "title", "name":
		return MusicListSortAZ
	default:
		return MusicListSortAZ
	}
}

func normalizeSortDirection(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SortDirectionDesc, "descending":
		return SortDirectionDesc
	default:
		return SortDirectionAsc
	}
}

func compareOptionalTimes(left, right *time.Time, desc bool) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if left.Equal(*right) {
		return 0
	}
	if desc {
		if left.After(*right) {
			return -1
		}
		return 1
	}
	if left.Before(*right) {
		return -1
	}
	return 1
}

func compareText(left, right string, desc bool) int {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	cmp := strings.Compare(left, right)
	if desc {
		return -cmp
	}
	return cmp
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
