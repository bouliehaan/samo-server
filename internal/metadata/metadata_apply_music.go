package metadata

import (
	"context"
	"strconv"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *MetadataApplyService) applyMusicArtist(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeArtist, err := s.loadMusicArtistByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterArtist := mergeMusicArtist(beforeArtist, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeArtist, afterArtist, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetMusicArtist, targetID, applied, afterArtist, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeArtist, afterArtist, applied, skipped, nil
}

func (s *MetadataApplyService) applyMusicAlbum(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeAlbum, err := s.loadMusicAlbumByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterAlbum := mergeMusicAlbum(beforeAlbum, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeAlbum, afterAlbum, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetMusicAlbum, targetID, applied, afterAlbum, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeAlbum, afterAlbum, applied, skipped, nil
}

func (s *MetadataApplyService) applyMusicTrack(
	ctx context.Context,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	beforeTrack, err := s.loadMusicTrackByID(ctx, targetID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	afterTrack := mergeMusicTrack(beforeTrack, candidate, fields)
	applied, skipped = partitionApplyFields(fields, candidate)
	if dryRun {
		return beforeTrack, afterTrack, applied, skipped, nil
	}
	if err := s.persistMetadataOverride(ctx, ApplyTargetMusicTrack, targetID, applied, afterTrack, candidate); err != nil {
		return nil, nil, nil, nil, err
	}
	return beforeTrack, afterTrack, applied, skipped, nil
}

func (s *MetadataApplyService) loadMusicArtistByID(ctx context.Context, id string) (catalog.MusicArtist, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.MusicArtist{}, err
	}
	for _, artist := range seed.MusicArtists {
		if artist.ID == id {
			return artist, nil
		}
	}
	return catalog.MusicArtist{}, ErrApplyNotFound
}

func (s *MetadataApplyService) loadMusicAlbumByID(ctx context.Context, id string) (catalog.MusicAlbum, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.MusicAlbum{}, err
	}
	for _, album := range seed.MusicAlbums {
		if album.ID == id {
			return album, nil
		}
	}
	return catalog.MusicAlbum{}, ErrApplyNotFound
}

func (s *MetadataApplyService) loadMusicTrackByID(ctx context.Context, id string) (catalog.MusicTrack, error) {
	seed, err := catalog.LoadSeedFromDB(ctx, s.db)
	if err != nil {
		return catalog.MusicTrack{}, err
	}
	for _, track := range seed.MusicTracks {
		if track.ID == id {
			return track, nil
		}
	}
	return catalog.MusicTrack{}, ErrApplyNotFound
}

func mergeMusicArtist(artist catalog.MusicArtist, candidate SearchResult, fields []string) catalog.MusicArtist {
	set := fieldSet(fields)
	if wantsField(set, "name") && candidate.Title != "" {
		artist.Name = candidate.Title
	}
	if wantsField(set, "sortName") && candidate.SortTitle != "" {
		artist.SortName = candidate.SortTitle
	}
	if wantsField(set, "description") && candidate.Description != "" {
		artist.Disambiguation = candidate.Description
	}
	if wantsField(set, "genres") && len(candidate.Genres) > 0 {
		artist.Genres = append([]string(nil), candidate.Genres...)
	}
	if wantsField(set, "tags") && len(candidate.Tags) > 0 {
		artist.Moods = append([]string(nil), candidate.Tags...)
	}
	if wantsField(set, "externalIds") {
		artist.ExternalIDs = mergeExternalIDs(artist.ExternalIDs, candidate.ExternalIDs)
	}
	return artist
}

func mergeMusicAlbum(album catalog.MusicAlbum, candidate SearchResult, fields []string) catalog.MusicAlbum {
	set := fieldSet(fields)
	if wantsField(set, "title") && candidate.Title != "" {
		album.Title = candidate.Title
	}
	if wantsField(set, "sortTitle") && candidate.SortTitle != "" {
		album.SortTitle = candidate.SortTitle
	}
	if wantsField(set, "version") && candidate.Subtitle != "" {
		album.Version = candidate.Subtitle
	}
	if wantsField(set, "displayArtist") && len(candidate.Authors) > 0 {
		album.DisplayArtist = joinContributorNames(candidate.Authors)
	}
	if wantsField(set, "releaseDate") && candidate.PublishedDate != "" {
		album.ReleaseDate = candidate.PublishedDate
	}
	if wantsField(set, "originalReleaseDate") && candidate.PublishedDate != "" {
		album.OriginalReleaseDate = candidate.PublishedDate
	}
	if wantsField(set, "releaseYear") && candidate.PublishedYear != "" {
		year, _ := strconv.Atoi(candidate.PublishedYear)
		album.ReleaseYear = year
	}
	if wantsField(set, "releaseType") && candidate.Description != "" {
		album.ReleaseType = candidate.Description
	}
	if wantsField(set, "recordLabel") && candidate.Publisher != "" {
		album.RecordLabel = candidate.Publisher
	}
	if wantsField(set, "genres") && len(candidate.Genres) > 0 {
		album.Genres = append([]string(nil), candidate.Genres...)
	}
	if wantsField(set, "tags") && len(candidate.Tags) > 0 {
		album.Tags = append([]string(nil), candidate.Tags...)
	}
	if wantsField(set, "cover") {
		if cover := coverFromCandidate(candidate); cover != nil {
			album.Images = []catalog.Image{*cover}
		}
	}
	if wantsField(set, "externalIds") {
		album.ExternalIDs = mergeExternalIDs(album.ExternalIDs, candidate.ExternalIDs)
	}
	return album
}

func mergeMusicTrack(track catalog.MusicTrack, candidate SearchResult, fields []string) catalog.MusicTrack {
	set := fieldSet(fields)
	if wantsField(set, "title") && candidate.Title != "" {
		track.Title = candidate.Title
	}
	if wantsField(set, "sortTitle") && candidate.SortTitle != "" {
		track.SortTitle = candidate.SortTitle
	}
	if wantsField(set, "subtitle") && candidate.Subtitle != "" {
		track.Subtitle = candidate.Subtitle
	}
	if wantsField(set, "displayArtist") && len(candidate.Authors) > 0 {
		track.DisplayArtist = joinContributorNames(candidate.Authors)
	}
	if wantsField(set, "releaseDate") && candidate.PublishedDate != "" {
		track.ReleaseDate = candidate.PublishedDate
	}
	if wantsField(set, "releaseYear") && candidate.PublishedYear != "" {
		year, _ := strconv.Atoi(candidate.PublishedYear)
		track.ReleaseYear = year
	}
	if wantsField(set, "genres") && len(candidate.Genres) > 0 {
		track.Genres = append([]string(nil), candidate.Genres...)
	}
	if wantsField(set, "tags") && len(candidate.Tags) > 0 {
		track.Tags = append([]string(nil), candidate.Tags...)
	}
	if wantsField(set, "explicit") {
		track.Explicit = candidate.Explicit
	}
	if wantsField(set, "cover") {
		if cover := coverFromCandidate(candidate); cover != nil {
			track.Images = []catalog.Image{*cover}
		}
	}
	if wantsField(set, "externalIds") {
		track.ExternalIDs = mergeExternalIDs(track.ExternalIDs, candidate.ExternalIDs)
	}
	return track
}

func joinContributorNames(contributors []catalog.ContributorRef) string {
	names := make([]string, 0, len(contributors))
	for _, contributor := range contributors {
		if name := strings.TrimSpace(contributor.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}
