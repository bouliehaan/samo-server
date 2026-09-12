package search

import (
	"sort"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type audiobookIndex struct {
	audiobooks   []catalog.AudiobookItem
	contributors []catalog.Contributor
	series       []catalog.Series
}

func buildAudiobookIndex(seed catalog.Seed) audiobookIndex {
	return audiobookIndex{
		audiobooks:   append([]catalog.AudiobookItem(nil), seed.Audiobooks...),
		contributors: append([]catalog.Contributor(nil), seed.Contributors...),
		series:       append([]catalog.Series(nil), seed.Series...),
	}
}

func (idx audiobookIndex) search(query AudiobookQuery, overlay PlaybackOverlay) catalog.AudiobookSearchResults {
	page := catalog.NormalizePage(query.Page)
	results := catalog.AudiobookSearchResults{Limit: page.Limit, Offset: page.Offset}

	bookMatches := filterAudiobooks(idx.audiobooks, query, overlay)
	contributorMatches := filterContributors(idx.contributors, query)
	seriesMatches := filterAudiobookSeries(idx.series, query)

	sortAudiobooks(bookMatches, query)
	sortContributors(contributorMatches, query)
	sortAudiobookSeries(seriesMatches, query)

	results.Audiobooks = catalog.Paginate(bookMatches, page).Items
	results.Contributors = catalog.Paginate(contributorMatches, page).Items
	results.Series = catalog.Paginate(seriesMatches, page).Items
	results.Total = len(bookMatches) + len(contributorMatches) + len(seriesMatches)
	return results
}

func filterAudiobooks(items []catalog.AudiobookItem, query AudiobookQuery, overlay PlaybackOverlay) []catalog.AudiobookItem {
	matches := make([]catalog.AudiobookItem, 0)
	for _, item := range items {
		if query.LibraryID != "" && item.LibraryID != query.LibraryID {
			continue
		}
		item.Progress = overlayAudiobooks(overlay, item.ID, item.Progress)
		if !longformItemMatchesQuery(query.toCommon(), item.Genres, item.AddedAt, item.Progress, audiobookSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterContributors(items []catalog.Contributor, query AudiobookQuery) []catalog.Contributor {
	if query.LibraryID != "" || query.RecentlyPlayed || query.RecentlyAdded || query.Completed != nil || query.Favorite != nil || query.Starred != nil || query.MinRating > 0 {
		return nil
	}
	matches := make([]catalog.Contributor, 0)
	for _, item := range items {
		if !MatchText(contributorSearchText(item), query.Text) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterAudiobookSeries(items []catalog.Series, query AudiobookQuery) []catalog.Series {
	if query.LibraryID != "" || query.RecentlyPlayed || query.RecentlyAdded || query.Completed != nil || query.Favorite != nil || query.Starred != nil || query.MinRating > 0 {
		return nil
	}
	matches := make([]catalog.Series, 0)
	for _, item := range items {
		if !MatchText(audiobookSeriesSearchText(item), query.Text) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

// Each record's search text is split into the title the ranker scores and
// the secondary text that is only its floor; the filter sees the two joined.

func audiobookSearchFields(item catalog.AudiobookItem) (title, secondary string) {
	values := []string{
		item.ID, item.Path,
		strings.Join(item.Tags, " "), strings.Join(item.Genres, " "),
	}
	if item.Book != nil {
		values = append(values,
			item.Book.Subtitle, item.Book.SortTitle, item.Book.Description, item.Book.Publisher,
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
	return audiobookTitle(item), joinFields(values...)
}

func contributorSearchFields(item catalog.Contributor) (title, secondary string) {
	return item.Name, joinFields(item.SortName, item.Description)
}

func audiobookSeriesSearchFields(item catalog.Series) (title, secondary string) {
	authors := make([]string, 0, len(item.Authors))
	for _, author := range item.Authors {
		authors = append(authors, author.Name, author.SortName)
	}
	return item.Name, joinFields(item.Description, strings.Join(authors, " "))
}

func audiobookSearchText(item catalog.AudiobookItem) string {
	return joinFields(audiobookSearchFields(item))
}

func contributorSearchText(item catalog.Contributor) string {
	return joinFields(contributorSearchFields(item))
}

func audiobookSeriesSearchText(item catalog.Series) string {
	return joinFields(audiobookSeriesSearchFields(item))
}

func sortAudiobooks(items []catalog.AudiobookItem, query AudiobookQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.AudiobookItem) rankFields {
			title, secondary := audiobookSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Progress.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort,
			audiobookTitle(items[i]), items[i].AddedAt, items[i].Progress,
			audiobookTitle(items[j]), items[j].AddedAt, items[j].Progress)
	})
}

func sortContributors(items []catalog.Contributor, query AudiobookQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.Contributor) rankFields {
			title, secondary := contributorSearchFields(item)
			return rankFields{Title: title, Secondary: secondary}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort,
			items[i].Name, nil, catalog.PlaybackState{},
			items[j].Name, nil, catalog.PlaybackState{})
	})
}

func sortAudiobookSeries(items []catalog.Series, query AudiobookQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.Series) rankFields {
			title, secondary := audiobookSeriesSearchFields(item)
			return rankFields{Title: title, Secondary: secondary}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort,
			items[i].Name, nil, catalog.PlaybackState{},
			items[j].Name, nil, catalog.PlaybackState{})
	})
}

func audiobookTitle(item catalog.AudiobookItem) string {
	if item.Book != nil {
		return item.Book.Title
	}
	return item.ID
}

func overlayAudiobooks(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Audiobooks[id]; ok {
		return state
	}
	return current
}

// longformItemMatchesQuery / longformLess are shared by the audiobook and
// podcast indexers. They live here rather than in a `longform_common.go`
// because there is essentially no shared structure between AudiobookItem
// and PodcastItem — only the playback-state filter and sort tiebreak look
// identical, and inlining them here keeps the per-domain files standalone.
func longformItemMatchesQuery(query commonLongformFilter, genres []string, addedAt *time.Time, playback catalog.PlaybackState, searchText string) bool {
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

// longformLess orders the explicit sort modes; relevance has its own path.
func longformLess(sortMode string,
	titleI string, addedI *time.Time, playbackI catalog.PlaybackState,
	titleJ string, addedJ *time.Time, playbackJ catalog.PlaybackState) bool {
	switch sortMode {
	case SortAdded:
		return timeAfter(addedI, addedJ)
	case SortPlayed:
		return timeAfter(playbackI.LastPlayedAt, playbackJ.LastPlayedAt)
	default:
		return strings.ToLower(titleI) < strings.ToLower(titleJ)
	}
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
