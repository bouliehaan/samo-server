package catalog

import (
	"sort"
	"strings"
	"time"
)

const (
	RecentlyAddedKindMusicAlbum = "music-album"
	RecentlyAddedKindAudiobook  = "audiobook"
	RecentlyAddedKindPodcast    = "podcast"
)

// RecentlyAddedEntry is one row on the home "recently added" shelf across media kinds.
type RecentlyAddedEntry struct {
	Kind     string     `json:"kind"`
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Subtitle string     `json:"subtitle,omitempty"`
	AddedAt  *time.Time `json:"addedAt,omitempty"`
}

type RecentlyAddedResults struct {
	Items  []RecentlyAddedEntry `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

func (s *Service) ListRecentlyAdded(page PageRequest) RecentlyAddedResults {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]RecentlyAddedEntry, 0, len(s.musicAlbums)+len(s.audiobooks)+len(s.podcasts))
	for _, album := range s.musicAlbums {
		if entry, ok := recentlyAddedMusicAlbum(album); ok {
			entries = append(entries, entry)
		}
	}
	for _, item := range s.audiobooks {
		if entry, ok := recentlyAddedAudiobook(item); ok {
			entries = append(entries, entry)
		}
	}
	for _, item := range s.podcasts {
		if entry, ok := recentlyAddedPodcast(item); ok {
			entries = append(entries, entry)
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return addedAtAfter(entries[i].AddedAt, entries[j].AddedAt)
	})

	page = normalizePage(page)
	if page.Limit <= 0 {
		page.Limit = 50
	}
	total := len(entries)
	if page.Offset > len(entries) {
		entries = nil
	} else {
		end := page.Offset + page.Limit
		if end > len(entries) {
			end = len(entries)
		}
		entries = entries[page.Offset:end]
	}

	return RecentlyAddedResults{
		Items:  entries,
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}
}

func recentlyAddedMusicAlbum(album MusicAlbum) (RecentlyAddedEntry, bool) {
	if strings.TrimSpace(album.ID) == "" {
		return RecentlyAddedEntry{}, false
	}
	subtitle := strings.TrimSpace(album.DisplayArtist)
	if subtitle == "" && len(album.AlbumArtistNames) > 0 {
		subtitle = strings.Join(album.AlbumArtistNames, ", ")
	}
	if subtitle == "" {
		subtitle = "Album"
	}
	return RecentlyAddedEntry{
		Kind:     RecentlyAddedKindMusicAlbum,
		ID:       album.ID,
		Title:    firstNonEmpty(album.Title, "Untitled"),
		Subtitle: subtitle,
		AddedAt:  album.AddedAt,
	}, true
}

func recentlyAddedAudiobook(item AudiobookItem) (RecentlyAddedEntry, bool) {
	if strings.TrimSpace(item.ID) == "" || item.Missing {
		return RecentlyAddedEntry{}, false
	}
	title := audiobookTitle(item)
	subtitle := "Audiobook"
	if item.Book != nil && len(item.Book.Authors) > 0 {
		names := make([]string, 0, len(item.Book.Authors))
		for _, author := range item.Book.Authors {
			if name := strings.TrimSpace(author.Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			subtitle = strings.Join(names, ", ")
		}
	}
	return RecentlyAddedEntry{
		Kind:     RecentlyAddedKindAudiobook,
		ID:       item.ID,
		Title:    title,
		Subtitle: subtitle,
		AddedAt:  item.AddedAt,
	}, true
}

func recentlyAddedPodcast(item PodcastItem) (RecentlyAddedEntry, bool) {
	if strings.TrimSpace(item.ID) == "" || item.Missing {
		return RecentlyAddedEntry{}, false
	}
	title := podcastTitle(item)
	subtitle := "Podcast"
	if item.Podcast != nil {
		if author := strings.TrimSpace(item.Podcast.Author); author != "" {
			subtitle = author
		}
	}
	return RecentlyAddedEntry{
		Kind:     RecentlyAddedKindPodcast,
		ID:       item.ID,
		Title:    title,
		Subtitle: subtitle,
		AddedAt:  item.AddedAt,
	}, true
}
