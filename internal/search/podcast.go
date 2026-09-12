package search

import (
	"sort"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type podcastIndex struct {
	podcasts []catalog.PodcastItem
	episodes []catalog.PodcastEpisode
}

func buildPodcastIndex(seed catalog.Seed) podcastIndex {
	return podcastIndex{
		podcasts: append([]catalog.PodcastItem(nil), seed.Podcasts...),
		episodes: append([]catalog.PodcastEpisode(nil), seed.PodcastEpisodes...),
	}
}

func (idx podcastIndex) search(query PodcastQuery, overlay PlaybackOverlay) catalog.PodcastSearchResults {
	page := catalog.NormalizePage(query.Page)
	results := catalog.PodcastSearchResults{Limit: page.Limit, Offset: page.Offset}

	showMatches := filterPodcasts(idx.podcasts, query, overlay)
	episodeMatches := filterPodcastEpisodes(idx.episodes, query, overlay)

	sortPodcasts(showMatches, query)
	sortPodcastEpisodes(episodeMatches, query)

	results.Podcasts = catalog.Paginate(showMatches, page).Items
	results.Episodes = catalog.Paginate(episodeMatches, page).Items
	results.Total = len(showMatches) + len(episodeMatches)
	return results
}

func filterPodcasts(items []catalog.PodcastItem, query PodcastQuery, overlay PlaybackOverlay) []catalog.PodcastItem {
	matches := make([]catalog.PodcastItem, 0)
	for _, item := range items {
		if query.LibraryID != "" && item.LibraryID != query.LibraryID {
			continue
		}
		item.Progress = overlayPodcasts(overlay, item.ID, item.Progress)
		if !longformItemMatchesQuery(query.toCommon(), item.Genres, item.AddedAt, item.Progress, podcastSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

func filterPodcastEpisodes(items []catalog.PodcastEpisode, query PodcastQuery, overlay PlaybackOverlay) []catalog.PodcastEpisode {
	matches := make([]catalog.PodcastEpisode, 0)
	for _, item := range items {
		if query.LibraryID != "" && item.LibraryID != query.LibraryID {
			continue
		}
		item.Progress = overlayEpisodes(overlay, item.ID, item.Progress)
		if !longformItemMatchesQuery(query.toCommon(), nil, item.AddedAt, item.Progress, episodeSearchText(item)) {
			continue
		}
		matches = append(matches, item)
	}
	return matches
}

// Each record's search text is split into the title the ranker scores and
// the secondary text that is only its floor; the filter sees the two joined.

func podcastSearchFields(item catalog.PodcastItem) (title, secondary string) {
	values := []string{
		item.ID, item.Path,
		strings.Join(item.Tags, " "), strings.Join(item.Genres, " "),
	}
	if item.Podcast != nil {
		values = append(values,
			item.Podcast.Author, item.Podcast.Description,
			item.Podcast.FeedURL, item.Podcast.SiteURL, item.Podcast.OwnerEmail,
			item.Podcast.ExternalIDs.FeedGUID, strings.Join(item.Podcast.ExternalIDs.URLs, " "),
			strings.Join(item.Podcast.Categories, " "),
		)
	}
	return podcastTitle(item), joinFields(values...)
}

func episodeSearchFields(item catalog.PodcastEpisode) (title, secondary string) {
	return item.Title, joinFields(
		item.ID,
		item.PodcastID,
		item.Subtitle,
		item.Description,
		item.EnclosureURL,
		item.EnclosureType,
		item.ExternalIDs.FeedGUID,
		strings.Join(item.ExternalIDs.URLs, " "),
	)
}

func podcastSearchText(item catalog.PodcastItem) string {
	return joinFields(podcastSearchFields(item))
}

func episodeSearchText(item catalog.PodcastEpisode) string {
	return joinFields(episodeSearchFields(item))
}

func sortPodcasts(items []catalog.PodcastItem, query PodcastQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.PodcastItem) rankFields {
			title, secondary := podcastSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Progress.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort,
			podcastTitle(items[i]), items[i].AddedAt, items[i].Progress,
			podcastTitle(items[j]), items[j].AddedAt, items[j].Progress)
	})
}

func sortPodcastEpisodes(items []catalog.PodcastEpisode, query PodcastQuery) {
	if isRelevanceSort(query.Sort) {
		sortByRelevance(items, query.Text, func(item catalog.PodcastEpisode) rankFields {
			title, secondary := episodeSearchFields(item)
			return rankFields{Title: title, Secondary: secondary, Plays: item.Progress.PlayCount}
		})
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort,
			items[i].Title, items[i].AddedAt, items[i].Progress,
			items[j].Title, items[j].AddedAt, items[j].Progress)
	})
}

func podcastTitle(item catalog.PodcastItem) string {
	if item.Podcast != nil {
		return item.Podcast.Title
	}
	return item.ID
}

func overlayPodcasts(overlay PlaybackOverlay, id string, current catalog.PlaybackState) catalog.PlaybackState {
	if state, ok := overlay.Podcasts[id]; ok {
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
