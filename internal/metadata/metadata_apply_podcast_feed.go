package metadata

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type podcastFeedApplyRow struct {
	ID           string
	PodcastID    string
	FeedURL      string
	Title        string
	Description  string
	Author       string
	SiteURL      string
	ImageURL     string
	Language     string
	Explicit     bool
	Categories   []string
	OwnerName    string
	OwnerEmail   string
	EpisodeCount int
	Cover        *catalog.Image
	ExternalIDs  catalog.ExternalIDs
}

func (s *MetadataApplyService) applyPodcastFeed(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeFeed, err := s.loadPodcastFeedByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterFeed := mergePodcastFeed(beforeFeed, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeFeed, afterFeed, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetPodcastFeed, targetID, applied, afterFeed, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeFeed, afterFeed, applied, skipped, nil
}

func (s *MetadataApplyService) loadPodcastFeedByID(ctx context.Context, id string) (podcastFeedApplyRow, error) {
	var (
		row         podcastFeedApplyRow
		explicit    int
		categories  string
		coverJSON   sql.NullString
		podcastJSON sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT f.id, f.podcast_id, f.feed_url, f.title, f.description, f.author, f.site_url, f.image_url,
		       f.language, f.explicit, f.categories_json, f.owner_name, f.owner_email, f.episode_count,
		       p.cover_json, p.podcast_json
		FROM podcast_feeds f
		LEFT JOIN podcasts p ON p.id = f.podcast_id
		WHERE f.id = ?`, id).Scan(
		&row.ID, &row.PodcastID, &row.FeedURL, &row.Title, &row.Description, &row.Author, &row.SiteURL,
		&row.ImageURL, &row.Language, &explicit, &categories, &row.OwnerName, &row.OwnerEmail,
		&row.EpisodeCount, &coverJSON, &podcastJSON,
	)
	if err == sql.ErrNoRows {
		return podcastFeedApplyRow{}, ErrApplyNotFound
	}
	if err != nil {
		return podcastFeedApplyRow{}, fmt.Errorf("load podcast feed metadata: %w", err)
	}
	row.Explicit = explicit != 0
	decodeApplyJSON(categories, &row.Categories)
	if coverJSON.Valid && coverJSON.String != "" {
		var cover catalog.Image
		decodeApplyJSON(coverJSON.String, &cover)
		if cover.ID != "" || cover.URL != "" || cover.Path != "" {
			row.Cover = &cover
			if row.ImageURL == "" && cover.URL != "" {
				row.ImageURL = cover.URL
			}
		}
	}
	if podcastJSON.Valid && podcastJSON.String != "" {
		var podcast catalog.PodcastMetadata
		decodeApplyJSON(podcastJSON.String, &podcast)
		row.ExternalIDs = podcast.ExternalIDs
	}
	return row, nil
}

func mergePodcastFeed(feed podcastFeedApplyRow, candidate SearchResult, fields []string) podcastFeedApplyRow {
	set := fieldSet(fields)
	if wantsField(set, "title") && candidate.Title != "" {
		feed.Title = candidate.Title
	}
	if wantsField(set, "description") && candidate.Description != "" {
		feed.Description = candidate.Description
	}
	if wantsField(set, "author") && len(candidate.Authors) > 0 {
		feed.Author = firstContributorName(candidate.Authors)
	}
	if wantsField(set, "siteUrl") {
		if url := firstLinkURL(candidate.Links); url != "" {
			feed.SiteURL = url
		}
	}
	if wantsField(set, "imageUrl") {
		if cover := coverFromCandidate(candidate); cover != nil {
			feed.ImageURL = cover.URL
			feed.Cover = cover
		}
	}
	if wantsField(set, "language") && candidate.Language != "" {
		feed.Language = candidate.Language
	}
	if wantsField(set, "categories") && len(candidate.Genres) > 0 {
		feed.Categories = append([]string(nil), candidate.Genres...)
	}
	if wantsField(set, "explicit") {
		feed.Explicit = candidate.Explicit
	}
	if wantsField(set, "externalIds") {
		feed.ExternalIDs = mergeExternalIDs(feed.ExternalIDs, candidate.ExternalIDs)
		if feed.SiteURL == "" {
			if url := firstLinkURL(candidate.Links); url != "" {
				feed.SiteURL = url
			}
		}
	}
	return feed
}
