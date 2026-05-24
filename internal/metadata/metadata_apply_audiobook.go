package metadata

import (
	"context"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *MetadataApplyService) loadAudiobookByID(ctx context.Context, id string) (catalog.AudiobookItem, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.AudiobookItem{}, err
	}
	for _, item := range seed.Audiobooks {
		if item.ID == id {
			return item, nil
		}
	}
	return catalog.AudiobookItem{}, ErrApplyNotFound
}

func (s *MetadataApplyService) applyAudiobook(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeItem, err := s.loadAudiobookByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterItem := mergeAudiobook(beforeItem, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeItem, afterItem, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetAudiobook, targetID, applied, afterItem, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeItem, afterItem, applied, skipped, nil
}

func mergeAudiobook(item catalog.AudiobookItem, candidate SearchResult, fields []string) catalog.AudiobookItem {
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
		book.Authors = append([]catalog.ContributorRef(nil), candidate.Authors...)
	}
	if wantsField(set, "narrators") && len(candidate.Narrators) > 0 {
		book.Narrators = append([]catalog.ContributorRef(nil), candidate.Narrators...)
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
