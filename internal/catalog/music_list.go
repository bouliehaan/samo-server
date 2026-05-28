package catalog

import (
	"sort"
	"strings"
	"time"
)

const (
	MusicListSortAZ        = "az"
	MusicListSortRecent    = "recent"
	MusicListSortRelease   = "release"
	MusicListSortPlayCount = "playCount"
	SortDirectionAsc       = "asc"
	SortDirectionDesc      = "desc"
)

type MusicListOptions struct {
	Page      PageRequest
	Sort      string
	Direction string
}

func (s *Service) ListMusicArtistsSorted(options MusicListOptions) Page[MusicArtist] {
	s.mu.RLock()
	items := append([]MusicArtist(nil), s.musicArtists...)
	s.mu.RUnlock()

	sortMusicArtistList(items, options)
	return paginate(items, options.Page)
}

func (s *Service) ListMusicAlbumsSorted(options MusicListOptions) Page[MusicAlbum] {
	s.mu.RLock()
	items := append([]MusicAlbum(nil), s.musicAlbums...)
	s.mu.RUnlock()

	sortMusicAlbumList(items, options)
	return paginate(items, options.Page)
}

func (s *Service) ListMusicTracksSorted(options MusicListOptions) Page[MusicTrack] {
	s.mu.RLock()
	items := append([]MusicTrack(nil), s.musicTracks...)
	s.mu.RUnlock()

	sortMusicTrackList(items, options)
	return paginate(items, options.Page)
}

func sortMusicArtistList(items []MusicArtist, options MusicListOptions) {
	sortBy := normalizeMusicListSort(options.Sort)
	desc := normalizeSortDirection(options.Direction) == SortDirectionDesc
	sort.SliceStable(items, func(i, j int) bool {
		if sortBy == MusicListSortRecent {
			if cmp := compareOptionalTimes(items[i].AddedAt, items[j].AddedAt, desc); cmp != 0 {
				return cmp < 0
			}
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
		}
		return compareText(firstNonEmpty(items[i].SortTitle, items[i].Title), firstNonEmpty(items[j].SortTitle, items[j].Title), desc) < 0
	})
}

func normalizeMusicListSort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case MusicListSortRecent, "recents", "added", "added_at", "addedat":
		return MusicListSortRecent
	case MusicListSortRelease, "release_date", "releasedate", "year", "newest":
		return MusicListSortRelease
	case MusicListSortPlayCount, "play_count", "plays", "most_played", "mostplayed":
		return MusicListSortPlayCount
	case MusicListSortAZ, "a-z", "title", "name":
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
