package catalog

import "testing"

func TestShelfItemsForAuthorAndSeries(t *testing.T) {
	service := NewService(Seed{
		ShelfAuthors: []ShelfAuthor{{ID: "author-1", Name: "Nora Noise"}},
		ShelfSeries: []ShelfSeries{{
			ID:      "series-1",
			Name:    "Signal House",
			ItemIDs: []string{"book-2"},
		}},
		ShelfItems: []ShelfItem{
			{
				ID:        "book-1",
				MediaType: ShelfMediaTypeBook,
				Book: &BookMetadata{
					Title:   "Archive",
					Authors: []Contributor{{ID: "author-1", Name: "Nora Noise"}},
				},
			},
			{
				ID:        "book-2",
				MediaType: ShelfMediaTypeBook,
				Book:      &BookMetadata{Title: "Signal Two"},
			},
		},
	})

	authorItems, err := service.ShelfItemsForAuthor("author-1", PageRequest{Limit: 10})
	if err != nil || authorItems.Total != 1 || authorItems.Items[0].ID != "book-1" {
		t.Fatalf("author items = %#v err = %v", authorItems, err)
	}
	seriesItems, err := service.ShelfItemsForSeries("series-1", PageRequest{Limit: 10})
	if err != nil || seriesItems.Total != 1 || seriesItems.Items[0].ID != "book-2" {
		t.Fatalf("series items = %#v err = %v", seriesItems, err)
	}
}
