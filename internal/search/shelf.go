package search

import (
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type shelfIndex struct {
	items    []catalog.ShelfItem
	authors  []catalog.ShelfAuthor
	series   []catalog.ShelfSeries
	episodes []catalog.PodcastEpisode
}

func buildShelfIndex(seed catalog.Seed) shelfIndex {
	return shelfIndex{
		items:    append([]catalog.ShelfItem(nil), seed.ShelfItems...),
		authors:  append([]catalog.ShelfAuthor(nil), seed.ShelfAuthors...),
		series:   append([]catalog.ShelfSeries(nil), seed.ShelfSeries...),
		episodes: append([]catalog.PodcastEpisode(nil), seed.PodcastEpisodes...),
	}
}

func (idx shelfIndex) search(query ShelfQuery, overlay PlaybackOverlay) catalog.ShelfSearchResults {
	page := catalog.NormalizePage(query.Page)
	results := catalog.ShelfSearchResults{Limit: page.Limit, Offset: page.Offset}

	itemMatches := filterShelfItems(idx.items, query, overlay)
	authorMatches := filterShelfAuthors(idx.authors, query)
	seriesMatches := filterShelfSeries(idx.series, query)
	episodeMatches := filterShelfEpisodes(idx.episodes, query, overlay)

	sortShelfItems(itemMatches, query)
	sortShelfAuthors(authorMatches, query)
	sortShelfSeries(seriesMatches, query)
	sortShelfEpisodes(episodeMatches, query)

	results.Items = catalog.Paginate(itemMatches, page).Items
	results.Authors = catalog.Paginate(authorMatches, page).Items
	results.Series = catalog.Paginate(seriesMatches, page).Items
	results.Episodes = catalog.Paginate(episodeMatches, page).Items
	results.Total = len(itemMatches) + len(authorMatches) + len(seriesMatches) + len(episodeMatches)
	return results
}

func filterShelfItems(items []catalog.ShelfItem, query ShelfQuery, overlay PlaybackOverlay) []catalog.ShelfItem {
	matches := make([]catalog.ShelfItem, 0)
	for _, item := range items {
		if query.MediaType != "" && item.MediaType != query.MediaType {
			continue
		}
		if query.LibraryID != "" && item.LibraryID != query.LibraryID {
			continue
		}
		item.Progress = overlayItems(overlay, item.ID, item.Progress)
		if !shelfItemMatchesQuery(query, item.Genres, item.AddedAt, item.Progress, shelfItemSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterShelfAuthors(items []catalog.ShelfAuthor, query ShelfQuery) []catalog.ShelfAuthor {
	if query.MediaType != "" || query.LibraryID != "" || query.RecentlyPlayed || query.RecentlyAdded || query.Completed != nil || query.Favorite != nil || query.Starred != nil || query.MinRating > 0 {
		return nil
	}
	matches := make([]catalog.ShelfAuthor, 0)
	for _, item := range items {
		if !MatchText(authorSearchText(item), query.Text) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterShelfSeries(items []catalog.ShelfSeries, query ShelfQuery) []catalog.ShelfSeries {
	if query.MediaType != "" || query.LibraryID != "" || query.RecentlyPlayed || query.RecentlyAdded || query.Completed != nil || query.Favorite != nil || query.Starred != nil || query.MinRating > 0 {
		return nil
	}
	matches := make([]catalog.ShelfSeries, 0)
	for _, item := range items {
		if !MatchText(seriesSearchText(item), query.Text) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterShelfEpisodes(items []catalog.PodcastEpisode, query ShelfQuery, overlay PlaybackOverlay) []catalog.PodcastEpisode {
	matches := make([]catalog.PodcastEpisode, 0)
	for _, item := range items {
		if query.MediaType == catalog.ShelfMediaTypeBook {
			continue
		}
		if query.LibraryID != "" && item.LibraryID != query.LibraryID {
			continue
		}
		item.Progress = overlayEpisodes(overlay, item.ID, item.Progress)
		if !shelfItemMatchesQuery(query, nil, item.AddedAt, item.Progress, episodeSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func shelfItemMatchesQuery(query ShelfQuery, genres []string, addedAt *time.Time, playback catalog.PlaybackState, searchText string) bool {
	if query.Genre != "" && !genreMatches(genres, query.Genre) && !strings.Contains(searchText, strings.ToLower(query.Genre)) {
		return false
	}
	if !playbackMatches(MusicQuery{
		Favorite:       query.Favorite,
		Starred:        query.Starred,
		RecentlyPlayed: query.RecentlyPlayed,
		RecentlyAdded:  query.RecentlyAdded,
		Completed:      query.Completed,
		MinRating:      query.MinRating,
	}, playback, addedAt) {
		return false
	}
	return MatchText(searchText, query.Text)
}

func shelfItemSearchText(item catalog.ShelfItem) string {
	values := []string{
		item.ID, item.Path,
		strings.Join(item.Tags, " "), strings.Join(item.Genres, " "),
	}
	if item.Book != nil {
		values = append(values,
			item.Book.Title, item.Book.Subtitle, item.Book.SortTitle, item.Book.Description, item.Book.Publisher,
			item.Book.PublishedYear, strings.Join(item.Book.Genres, " "), strings.Join(item.Book.Tags, " "),
		)
		for _, author := range item.Book.Authors {
			values = append(values, author.Name, author.SortName)
		}
		for _, narrator := range item.Book.Narrators {
			values = append(values, narrator.Name, narrator.SortName)
		}
		for _, series := range item.Book.Series {
			values = append(values, series.Name)
		}
	}
	if item.Podcast != nil {
		values = append(values, item.Podcast.Title, item.Podcast.Author, item.Podcast.Description, item.Podcast.FeedURL, strings.Join(item.Podcast.Categories, " "))
	}
	return joinFields(values...)
}

func authorSearchText(item catalog.ShelfAuthor) string {
	return joinFields(item.Name, item.SortName, item.Description)
}

func seriesSearchText(item catalog.ShelfSeries) string {
	authors := make([]string, 0, len(item.Authors))
	for _, author := range item.Authors {
		authors = append(authors, author.Name, author.SortName)
	}
	return joinFields(item.Name, item.Description, strings.Join(authors, " "))
}

func episodeSearchText(item catalog.PodcastEpisode) string {
	return joinFields(item.Title, item.Subtitle, item.Description)
}

func sortShelfItems(items []catalog.ShelfItem, query ShelfQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return shelfLess(query, shelfItemTitle(items[i]), items[i].AddedAt, items[i].Progress, shelfItemSearchText(items[i]),
			shelfItemTitle(items[j]), items[j].AddedAt, items[j].Progress, shelfItemSearchText(items[j]))
	})
}

func sortShelfAuthors(items []catalog.ShelfAuthor, query ShelfQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return shelfLess(query, items[i].Name, nil, catalog.PlaybackState{}, authorSearchText(items[i]),
			items[j].Name, nil, catalog.PlaybackState{}, authorSearchText(items[j]))
	})
}

func sortShelfSeries(items []catalog.ShelfSeries, query ShelfQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return shelfLess(query, items[i].Name, nil, catalog.PlaybackState{}, seriesSearchText(items[i]),
			items[j].Name, nil, catalog.PlaybackState{}, seriesSearchText(items[j]))
	})
}

func sortShelfEpisodes(items []catalog.PodcastEpisode, query ShelfQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return shelfLess(query, items[i].Title, items[i].AddedAt, items[i].Progress, episodeSearchText(items[i]),
			items[j].Title, items[j].AddedAt, items[j].Progress, episodeSearchText(items[j]))
	})
}

func shelfLess(query ShelfQuery, titleI string, addedI *time.Time, playbackI catalog.PlaybackState, textI string,
	titleJ string, addedJ *time.Time, playbackJ catalog.PlaybackState, textJ string) bool {
	switch query.Sort {
	case SortTitle:
		return strings.ToLower(titleI) < strings.ToLower(titleJ)
	case SortAdded:
		return timeAfter(addedI, addedJ)
	case SortPlayed:
		return timeAfter(playbackI.LastPlayedAt, playbackJ.LastPlayedAt)
	default:
		return ScoreText(textI, query.Text) > ScoreText(textJ, query.Text)
	}
}

func shelfItemTitle(item catalog.ShelfItem) string {
	if item.Book != nil {
		return item.Book.Title
	}
	if item.Podcast != nil {
		return item.Podcast.Title
	}
	return item.ID
}

func overlayItems(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Items[id]; ok {
		return state
	}
	return current
}

func overlayEpisodes(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Episodes[id]; ok {
		return state
	}
	return current
}

func timeAfter(left, right *time.Time) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	return left.After(*right)
}
