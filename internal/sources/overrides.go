package sources

import (
	"context"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *Service) projectPodcastFeed(ctx context.Context, feed PodcastFeed) (PodcastFeed, error) {
	idx, err := catalog.LoadOverrideIndex(ctx, s.db)
	if err != nil {
		return PodcastFeed{}, err
	}
	patch := idx.Patch(catalog.OverrideKindPodcastFeed, feed.ID)
	if len(patch) == 0 {
		return feed, nil
	}
	projected := catalog.ProjectPodcastFeedFields(catalog.PodcastFeedFields{
		Title:       feed.Title,
		Description: feed.Description,
		Author:      feed.Author,
		SiteURL:     feed.SiteURL,
		ImageURL:    feed.ImageURL,
		Language:    feed.Language,
		Explicit:    feed.Explicit,
		Categories:  feed.Categories,
	}, patch)
	feed.Title = projected.Title
	feed.Description = projected.Description
	feed.Author = projected.Author
	feed.SiteURL = projected.SiteURL
	feed.ImageURL = projected.ImageURL
	feed.Language = projected.Language
	feed.Explicit = projected.Explicit
	feed.Categories = projected.Categories
	return feed, nil
}

func (s *Service) guardPodcastFeedSave(
	ctx context.Context,
	idx *catalog.OverrideIndex,
	feedID, podcastID string,
	parsed parsedPodcastFeed,
) (parsedPodcastFeed, error) {
	if idx == nil || idx.IsEmpty() {
		return parsed, nil
	}
	row, err := idx.GuardPodcastFeedRow(ctx, s.db, catalog.PodcastFeedWriteRow{
		FeedID:      feedID,
		PodcastID:   podcastID,
		Title:       parsed.Title,
		Description: parsed.Description,
		Author:      parsed.Author,
		SiteURL:     parsed.SiteURL,
		ImageURL:    parsed.ImageURL,
		Language:    parsed.Language,
		Explicit:    parsed.Explicit,
		Categories:  cleanStringSlice(parsed.Categories),
		Cover:       imageFromURL(parsed.ImageURL),
	})
	if err != nil {
		return parsed, err
	}
	parsed.Title = row.Title
	parsed.Description = row.Description
	parsed.Author = row.Author
	parsed.SiteURL = row.SiteURL
	parsed.ImageURL = row.ImageURL
	parsed.Language = row.Language
	parsed.Explicit = row.Explicit
	parsed.Categories = append([]string(nil), row.Categories...)
	return parsed, nil
}

func (s *Service) guardPodcastEpisodeSave(
	ctx context.Context,
	idx *catalog.OverrideIndex,
	episode catalog.PodcastEpisode,
) (catalog.PodcastEpisode, error) {
	if idx == nil || idx.IsEmpty() {
		return episode, nil
	}
	return idx.GuardPodcastEpisode(ctx, s.db, episode)
}

func (s *Service) guardPodcastEpisodesSave(
	ctx context.Context,
	idx *catalog.OverrideIndex,
	podcastID string,
	libraryID string,
	episodes []parsedPodcastEpisode,
) ([]catalog.PodcastEpisode, error) {
	if strings.TrimSpace(libraryID) == "" {
		libraryID = remotePodcastLibraryID
	}
	guarded := make([]catalog.PodcastEpisode, 0, len(episodes))
	for _, parsedEpisode := range episodes {
		episodeID := podcastEpisodeID(podcastID, parsedEpisode)
		externalIDs := catalog.ExternalIDs{
			FeedGUID: parsedEpisode.GUID,
			URLs:     cleanStringSlice(append([]string{parsedEpisode.Link, parsedEpisode.EnclosureURL}, parsedEpisode.ExternalURLs...)),
		}
		episode := catalog.PodcastEpisode{
			ID:              episodeID,
			LibraryID:       libraryID,
			PodcastID:       podcastID,
			Title:           parsedEpisode.Title,
			Subtitle:        parsedEpisode.Subtitle,
			Description:     parsedEpisode.Description,
			PublishedAt:     parsedEpisode.PublishedAt,
			Season:          parsedEpisode.Season,
			Episode:         parsedEpisode.Episode,
			EpisodeType:     parsedEpisode.EpisodeType,
			DurationSeconds: parsedEpisode.DurationSeconds,
			Explicit:        parsedEpisode.Explicit,
			EnclosureURL:    parsedEpisode.EnclosureURL,
			EnclosureType:   parsedEpisode.EnclosureType,
			EnclosureBytes:  parsedEpisode.EnclosureBytes,
			ExternalIDs:     externalIDs,
		}
		var err error
		episode, err = s.guardPodcastEpisodeSave(ctx, idx, episode)
		if err != nil {
			return nil, err
		}
		guarded = append(guarded, episode)
	}
	return guarded, nil
}

func imageFromURL(url string) *catalog.Image {
	if url == "" {
		return nil
	}
	return &catalog.Image{URL: url}
}
