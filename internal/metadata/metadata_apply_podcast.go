package metadata

import (
	"context"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *MetadataApplyService) loadPodcastByID(ctx context.Context, id string) (catalog.PodcastItem, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.PodcastItem{}, err
	}
	for _, item := range seed.Podcasts {
		if item.ID == id {
			return item, nil
		}
	}
	return catalog.PodcastItem{}, ErrApplyNotFound
}

func (s *MetadataApplyService) applyPodcast(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeItem, err := s.loadPodcastByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterItem := mergePodcast(beforeItem, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeItem, afterItem, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetPodcast, targetID, applied, afterItem, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeItem, afterItem, applied, skipped, nil
}

func mergePodcast(item catalog.PodcastItem, candidate SearchResult, fields []string) catalog.PodcastItem {
	set := fieldSet(fields)
	podcast := item.Podcast
	if podcast == nil {
		podcast = &catalog.PodcastMetadata{}
	}
	if wantsField(set, "title") && candidate.Title != "" {
		podcast.Title = candidate.Title
	}
	if wantsField(set, "description") && candidate.Description != "" {
		podcast.Description = candidate.Description
	}
	if wantsField(set, "author") && len(candidate.Authors) > 0 {
		podcast.Author = firstContributorName(candidate.Authors)
	}
	if wantsField(set, "siteUrl") {
		if url := firstLinkURL(candidate.Links); url != "" {
			podcast.SiteURL = url
		}
	}
	if wantsField(set, "language") && candidate.Language != "" {
		podcast.Language = candidate.Language
	}
	if wantsField(set, "genres") || wantsField(set, "categories") {
		if len(candidate.Genres) > 0 {
			podcast.Categories = append([]string(nil), candidate.Genres...)
			item.Genres = append([]string(nil), candidate.Genres...)
		}
	}
	if wantsField(set, "explicit") {
		podcast.Explicit = candidate.Explicit
	}
	if wantsField(set, "externalIds") {
		podcast.ExternalIDs = mergeExternalIDs(podcast.ExternalIDs, candidate.ExternalIDs)
	}
	if wantsField(set, "cover") {
		if cover := coverFromCandidate(candidate); cover != nil {
			item.Cover = cover
		}
	}
	item.Podcast = podcast
	return item
}

func (s *MetadataApplyService) applyPodcastEpisode(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeEpisode, err := s.loadPodcastEpisodeByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterEpisode := mergePodcastEpisode(beforeEpisode, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeEpisode, afterEpisode, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetPodcastEpisode, targetID, applied, afterEpisode, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeEpisode, afterEpisode, applied, skipped, nil
}

func (s *MetadataApplyService) loadPodcastEpisodeByID(ctx context.Context, id string) (catalog.PodcastEpisode, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.PodcastEpisode{}, err
	}
	for _, episode := range seed.PodcastEpisodes {
		if episode.ID == id {
			return episode, nil
		}
	}
	return catalog.PodcastEpisode{}, ErrApplyNotFound
}

func mergePodcastEpisode(episode catalog.PodcastEpisode, candidate SearchResult, fields []string) catalog.PodcastEpisode {
	set := fieldSet(fields)
	if wantsField(set, "title") && candidate.Title != "" {
		episode.Title = candidate.Title
	}
	if wantsField(set, "subtitle") && candidate.Subtitle != "" {
		episode.Subtitle = candidate.Subtitle
	}
	if wantsField(set, "description") && candidate.Description != "" {
		episode.Description = candidate.Description
	}
	if wantsField(set, "publishedAt") && candidate.PublishedDate != "" {
		episode.PublishedAt = parseApplyDate(candidate.PublishedDate)
	}
	if wantsField(set, "explicit") {
		episode.Explicit = candidate.Explicit
	}
	if wantsField(set, "externalIds") {
		episode.ExternalIDs = mergeExternalIDs(episode.ExternalIDs, candidate.ExternalIDs)
	}
	return episode
}

func parseApplyDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	formats := []string{time.RFC3339, "2006-01-02", "2006"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}
