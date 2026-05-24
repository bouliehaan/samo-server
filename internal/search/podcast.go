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

func podcastSearchText(item catalog.PodcastItem) string {
	values := []string{
		item.ID, item.Path,
		strings.Join(item.Tags, " "), strings.Join(item.Genres, " "),
	}
	if item.Podcast != nil {
		values = append(values,
			item.Podcast.Title, item.Podcast.Author, item.Podcast.Description,
			item.Podcast.FeedURL, item.Podcast.SiteURL, item.Podcast.OwnerEmail,
			item.Podcast.ExternalIDs.FeedGUID, strings.Join(item.Podcast.ExternalIDs.URLs, " "),
			strings.Join(item.Podcast.Categories, " "),
		)
	}
	return joinFields(values...)
}

func episodeSearchText(item catalog.PodcastEpisode) string {
	return joinFields(
		item.ID,
		item.PodcastID,
		item.Title,
		item.Subtitle,
		item.Description,
		item.EnclosureURL,
		item.EnclosureType,
		item.ExternalIDs.FeedGUID,
		strings.Join(item.ExternalIDs.URLs, " "),
	)
}

func sortPodcasts(items []catalog.PodcastItem, query PodcastQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort, query.Text,
			podcastTitle(items[i]), items[i].AddedAt, items[i].Progress, podcastSearchText(items[i]),
			podcastTitle(items[j]), items[j].AddedAt, items[j].Progress, podcastSearchText(items[j]))
	})
}

func sortPodcastEpisodes(items []catalog.PodcastEpisode, query PodcastQuery) {
	sort.SliceStable(items, func(i, j int) bool {
		return longformLess(query.Sort, query.Text,
			items[i].Title, items[i].AddedAt, items[i].Progress, episodeSearchText(items[i]),
			items[j].Title, items[j].AddedAt, items[j].Progress, episodeSearchText(items[j]))
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
