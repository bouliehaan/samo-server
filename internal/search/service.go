package search

import (
	"sync"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type Service struct {
	mu    sync.RWMutex
	music musicIndex
	shelf shelfIndex
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
	s.shelf = buildShelfIndex(seed)
}

func (s *Service) SearchMusic(query MusicQuery, overlay PlaybackOverlay) catalog.MusicSearchResults {
	if s == nil {
		return catalog.MusicSearchResults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.music.search(query, overlay)
}

func (s *Service) SearchShelf(query ShelfQuery, overlay PlaybackOverlay) catalog.ShelfSearchResults {
	if s == nil {
		return catalog.ShelfSearchResults{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shelf.search(query, overlay)
}

// SearchMusicText is a compatibility helper for text-only callers (Subsonic, legacy tests).
func (s *Service) SearchMusicText(text string, page catalog.PageRequest) catalog.MusicSearchResults {
	return s.SearchMusic(MusicQuery{Text: text, Page: page, Sort: SortRelevance}, PlaybackOverlay{})
}

// SearchShelfText is a compatibility helper for text-only callers.
func (s *Service) SearchShelfText(text string, page catalog.PageRequest) catalog.ShelfSearchResults {
	return s.SearchShelf(ShelfQuery{Text: text, Page: page, Sort: SortRelevance}, PlaybackOverlay{})
}
