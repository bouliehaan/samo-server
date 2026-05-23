package catalog

import "strings"

type ShelfAuthorDetail struct {
	ShelfAuthor
	Items Page[ShelfItem] `json:"items"`
}

type ShelfSeriesDetail struct {
	ShelfSeries
	Items Page[ShelfItem] `json:"items"`
}

func (s *Service) ShelfAuthorDetail(id string, page PageRequest) (ShelfAuthorDetail, error) {
	author, err := s.ShelfAuthor(id)
	if err != nil {
		return ShelfAuthorDetail{}, err
	}
	items, err := s.ShelfItemsForAuthor(id, page)
	if err != nil {
		return ShelfAuthorDetail{}, err
	}
	return ShelfAuthorDetail{ShelfAuthor: author, Items: items}, nil
}

func (s *Service) ShelfItemsForAuthor(authorID string, page PageRequest) (Page[ShelfItem], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	authorID = strings.TrimSpace(authorID)
	if _, ok := s.shelfAuthorByID[authorID]; !ok {
		return Page[ShelfItem]{}, ErrNotFound
	}
	matches := make([]ShelfItem, 0)
	for _, item := range s.shelfItems {
		if item.MediaType != ShelfMediaTypeBook || item.Book == nil {
			continue
		}
		if itemMatchesAuthorID(item, authorID) {
			matches = append(matches, item)
		}
	}
	return paginate(matches, page), nil
}

func (s *Service) ShelfSeriesDetail(id string, page PageRequest) (ShelfSeriesDetail, error) {
	series, err := s.ShelfSeries(id)
	if err != nil {
		return ShelfSeriesDetail{}, err
	}
	items, err := s.ShelfItemsForSeries(id, page)
	if err != nil {
		return ShelfSeriesDetail{}, err
	}
	return ShelfSeriesDetail{ShelfSeries: series, Items: items}, nil
}

func (s *Service) ShelfItemsForSeries(seriesID string, page PageRequest) (Page[ShelfItem], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	series, ok := s.shelfSeriesByID[seriesID]
	if !ok {
		return Page[ShelfItem]{}, ErrNotFound
	}
	matches := make([]ShelfItem, 0, len(series.ItemIDs))
	for _, itemID := range series.ItemIDs {
		item, ok := s.shelfItemByID[itemID]
		if !ok {
			continue
		}
		matches = append(matches, item)
	}
	return paginate(matches, page), nil
}

func itemMatchesAuthorID(item ShelfItem, authorID string) bool {
	if item.Book == nil {
		return false
	}
	for _, author := range item.Book.Authors {
		if author.ID == authorID {
			return true
		}
	}
	return false
}
