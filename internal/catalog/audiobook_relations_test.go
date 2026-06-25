package catalog

import "testing"

func TestAudiobooksForContributorAndSeries(t *testing.T) {
	service := NewService(Seed{
		Contributors: []Contributor{{ID: "author-1", Name: "Nora Noise"}},
		Series: []Series{{
			ID:           "series-1",
			Name:         "Signal House",
			AudiobookIDs: []string{"book-2"},
		}},
		Audiobooks: []AudiobookItem{
			{
				ID: "book-1",
				Book: &BookMetadata{
					Title:   "Archive",
					Authors: []ContributorRef{{ID: "author-1", Name: "Nora Noise"}},
				},
			},
			{
				ID:   "book-2",
				Book: &BookMetadata{Title: "Signal Two"},
			},
		},
	})

	authorItems, err := service.AudiobooksForContributor("author-1", PageRequest{Limit: 10})
	if err != nil || authorItems.Total != 1 || authorItems.Items[0].ID != "book-1" {
		t.Fatalf("contributor items = %#v err = %v", authorItems, err)
	}
	seriesItems, err := service.AudiobooksForSeries("series-1", PageRequest{Limit: 10})
	if err != nil || seriesItems.Total != 1 || seriesItems.Items[0].ID != "book-2" {
		t.Fatalf("series items = %#v err = %v", seriesItems, err)
	}
}
