package search

import (
	"sync"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type Service struct {
	mu         sync.RWMutex
	music      musicIndex
	audiobooks audiobookIndex
	podcasts   podcastIndex
}

func New() *Service {
	return &Service{}
}

func (s *Service) Rebuild(seed catalog.Seed) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.music = buildMusicIndex(seed)
	s.audiobooks = buildAudiobookIndex(seed)
	s.podcasts = buildPodcastIndex(seed)
}

func (s *Service) SearchMusic(query MusicQuery, overlay PlaybackOverlay) catalog.MusicSearchResults {
	if s == nil {
		return catalog.MusicSearchResults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.music.search(query, overlay)
}

func (s *Service) SearchAudiobooks(query AudiobookQuery, overlay PlaybackOverlay) catalog.AudiobookSearchResults {
	if s == nil {
		return catalog.AudiobookSearchResults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.audiobooks.search(query, overlay)
}

func (s *Service) SearchPodcasts(query PodcastQuery, overlay PlaybackOverlay) catalog.PodcastSearchResults {
	if s == nil {
		return catalog.PodcastSearchResults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.podcasts.search(query, overlay)
}

// SearchMusicText is a compatibility helper for text-only callers (Subsonic, legacy tests).
func (s *Service) SearchMusicText(text string, page catalog.PageRequest) catalog.MusicSearchResults {
	return s.SearchMusic(MusicQuery{Text: text, Page: page, Sort: SortRelevance}, PlaybackOverlay{})
}

// SearchAudiobooksText is a compatibility helper for text-only callers.
func (s *Service) SearchAudiobooksText(text string, page catalog.PageRequest) catalog.AudiobookSearchResults {
	return s.SearchAudiobooks(AudiobookQuery{Text: text, Page: page, Sort: SortRelevance}, PlaybackOverlay{})
}

// SearchPodcastsText is a compatibility helper for text-only callers.
func (s *Service) SearchPodcastsText(text string, page catalog.PageRequest) catalog.PodcastSearchResults {
	return s.SearchPodcasts(PodcastQuery{Text: text, Page: page, Sort: SortRelevance}, PlaybackOverlay{})
}
