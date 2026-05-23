package metadata

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *MetadataApplyService) loadShelfMediaType(ctx context.Context, id string) (catalog.ShelfMediaType, error) {
	var mediaType string
	err := s.db.QueryRowContext(ctx, `SELECT media_type FROM shelf_items WHERE id = ?`, id).Scan(&mediaType)
	if err == sql.ErrNoRows {
		return "", ErrApplyNotFound
	}
	if err != nil {
		return "", err
	}
	return catalog.ShelfMediaType(mediaType), nil
}

func (s *MetadataApplyService) loadShelfItemByID(ctx context.Context, id string) (catalog.ShelfItem, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.ShelfItem{}, err
	}
	for _, item := range seed.ShelfItems {
		if item.ID == id {
			return item, nil
		}
	}
	return catalog.ShelfItem{}, ErrApplyNotFound
}

func (s *MetadataApplyService) applyShelfItem(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeItem, err := s.loadShelfItemByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterItem := beforeItem
	if beforeItem.MediaType == catalog.ShelfMediaTypePodcast {
		afterItem = mergeShelfPodcastItem(beforeItem, candidate, fields)
	} else {
		afterItem = mergeShelfBookItem(beforeItem, candidate, fields)
	}
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeItem, afterItem, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetShelfItem, targetID, applied, afterItem, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeItem, afterItem, applied, skipped, nil
}

func mergeShelfBookItem(item catalog.ShelfItem, candidate SearchResult, fields []string) catalog.ShelfItem {
	set := fieldSet(fields)
	book := item.Book
	if book == nil {
		book = &catalog.BookMetadata{}
	}
	if wantsField(set, "title") && candidate.Title != "" {
		book.Title = candidate.Title
	}
	if wantsField(set, "subtitle") && candidate.Subtitle != "" {
		book.Subtitle = candidate.Subtitle
	}
	if wantsField(set, "sortTitle") && candidate.SortTitle != "" {
		book.SortTitle = candidate.SortTitle
	}
	if wantsField(set, "description") && candidate.Description != "" {
		book.Description = candidate.Description
	}
	if wantsField(set, "publisher") && candidate.Publisher != "" {
		book.Publisher = candidate.Publisher
	}
	if wantsField(set, "publishedDate") && candidate.PublishedDate != "" {
		book.PublishedDate = candidate.PublishedDate
	}
	if wantsField(set, "publishedYear") && candidate.PublishedYear != "" {
		book.PublishedYear = candidate.PublishedYear
	}
	if wantsField(set, "language") && candidate.Language != "" {
		book.Language = candidate.Language
	}
	if wantsField(set, "genres") && len(candidate.Genres) > 0 {
		book.Genres = append([]string(nil), candidate.Genres...)
		item.Genres = append([]string(nil), candidate.Genres...)
	}
	if wantsField(set, "tags") && len(candidate.Tags) > 0 {
		book.Tags = append([]string(nil), candidate.Tags...)
		item.Tags = append([]string(nil), candidate.Tags...)
	}
	if wantsField(set, "explicit") {
		book.Explicit = candidate.Explicit
	}
	if wantsField(set, "authors") && len(candidate.Authors) > 0 {
		book.Authors = append([]catalog.Contributor(nil), candidate.Authors...)
	}
	if wantsField(set, "narrators") && len(candidate.Narrators) > 0 {
		book.Narrators = append([]catalog.Contributor(nil), candidate.Narrators...)
	}
	if wantsField(set, "series") && len(candidate.Series) > 0 {
		book.Series = append([]catalog.SeriesRef(nil), candidate.Series...)
	}
	if wantsField(set, "externalIds") {
		book.ExternalIDs = mergeExternalIDs(book.ExternalIDs, candidate.ExternalIDs)
		if isbns := isbnsFromCandidate(candidate); len(isbns) > 0 {
			book.ISBNs = isbns
		}
	}
	if wantsField(set, "cover") {
		if cover := coverFromCandidate(candidate); cover != nil {
			item.Cover = cover
		}
	}
	item.Book = book
	return item
}

func mergeShelfPodcastItem(item catalog.ShelfItem, candidate SearchResult, fields []string) catalog.ShelfItem {
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

func (s *MetadataApplyService) applyShelfEpisode(
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
	afterEpisode := mergeShelfEpisode(beforeEpisode, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeEpisode, afterEpisode, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetShelfEpisode, targetID, applied, afterEpisode, candidate); err != nil {
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

func mergeShelfEpisode(episode catalog.PodcastEpisode, candidate SearchResult, fields []string) catalog.PodcastEpisode {
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
