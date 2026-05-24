package metadata

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func (s *MetadataApplyService) persistMetadataOverride(
	ctx context.Context,
	kind ApplyTargetKind,
	targetID string,
	applied []string,
	after any,
	candidate SearchResult,
) error {
	patch, err := buildOverridePatch(kind, applied, after, candidate)
	if err != nil {
		return err
	}
	return catalog.UpsertMetadataOverride(ctx, s.db, string(kind), targetID, patch)
}

func buildOverridePatch(
	kind ApplyTargetKind,
	applied []string,
	after any,
	candidate SearchResult,
) (catalog.MetadataOverridePatch, error) {
	patch := catalog.MetadataOverridePatch{}
	for _, field := range applied {
		value, ok, err := overrideFieldValue(kind, field, after, candidate)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		patch[field] = raw
	}
	return patch, nil
}

func overrideFieldValue(
	kind ApplyTargetKind,
	field string,
	after any,
	candidate SearchResult,
) (any, bool, error) {
	switch kind {
	case ApplyTargetMusicArtist:
		artist, ok := after.(catalog.MusicArtist)
		if !ok {
			return nil, false, nil
		}
		return musicArtistOverrideValue(artist, field, candidate)
	case ApplyTargetMusicAlbum:
		album, ok := after.(catalog.MusicAlbum)
		if !ok {
			return nil, false, nil
		}
		return musicAlbumOverrideValue(album, field, candidate)
	case ApplyTargetMusicTrack:
		track, ok := after.(catalog.MusicTrack)
		if !ok {
			return nil, false, nil
		}
		return musicTrackOverrideValue(track, field, candidate)
	case ApplyTargetAudiobook:
		item, ok := after.(catalog.AudiobookItem)
		if !ok {
			return nil, false, nil
		}
		return audiobookOverrideValue(item, field, candidate)
	case ApplyTargetPodcast:
		item, ok := after.(catalog.PodcastItem)
		if !ok {
			return nil, false, nil
		}
		return podcastOverrideValue(item, field)
	case ApplyTargetPodcastEpisode:
		episode, ok := after.(catalog.PodcastEpisode)
		if !ok {
			return nil, false, nil
		}
		return podcastEpisodeOverrideValue(episode, field)
	case ApplyTargetPodcastFeed:
		feed, ok := after.(podcastFeedApplyRow)
		if !ok {
			return nil, false, nil
		}
		return podcastFeedOverrideValue(feed, field)
	default:
		return nil, false, nil
	}
}

func musicArtistOverrideValue(artist catalog.MusicArtist, field string, candidate SearchResult) (any, bool, error) {
	switch field {
	case "name":
		return artist.Name, artist.Name != "", nil
	case "sortName":
		return artist.SortName, artist.SortName != "", nil
	case "description":
		return artist.Disambiguation, artist.Disambiguation != "", nil
	case "genres":
		return artist.Genres, len(artist.Genres) > 0, nil
	case "tags":
		return artist.Moods, len(artist.Moods) > 0, nil
	case "externalIds":
		return artist.ExternalIDs, !externalIDsEmpty(artist.ExternalIDs), nil
	case "artists":
		return candidate.Authors, len(candidate.Authors) > 0, nil
	default:
		return nil, false, nil
	}
}

func musicAlbumOverrideValue(album catalog.MusicAlbum, field string, candidate SearchResult) (any, bool, error) {
	switch field {
	case "title":
		return album.Title, album.Title != "", nil
	case "sortTitle":
		return album.SortTitle, album.SortTitle != "", nil
	case "version":
		return album.Version, album.Version != "", nil
	case "displayArtist":
		return album.DisplayArtist, album.DisplayArtist != "", nil
	case "releaseDate":
		return album.ReleaseDate, album.ReleaseDate != "", nil
	case "originalReleaseDate":
		return album.OriginalReleaseDate, album.OriginalReleaseDate != "", nil
	case "releaseYear":
		return album.ReleaseYear, album.ReleaseYear != 0, nil
	case "releaseType":
		return album.ReleaseType, album.ReleaseType != "", nil
	case "recordLabel":
		return album.RecordLabel, album.RecordLabel != "", nil
	case "catalogNumber":
		return album.CatalogNumber, album.CatalogNumber != "", nil
	case "barcode":
		return album.Barcode, album.Barcode != "", nil
	case "genres":
		return album.Genres, len(album.Genres) > 0, nil
	case "styles":
		return album.Styles, len(album.Styles) > 0, nil
	case "moods":
		return album.Moods, len(album.Moods) > 0, nil
	case "tags":
		return album.Tags, len(album.Tags) > 0, nil
	case "cover":
		return album.Images, len(album.Images) > 0, nil
	case "externalIds":
		return album.ExternalIDs, !externalIDsEmpty(album.ExternalIDs), nil
	case "artists":
		return candidate.Authors, len(candidate.Authors) > 0, nil
	default:
		return nil, false, nil
	}
}

func musicTrackOverrideValue(track catalog.MusicTrack, field string, candidate SearchResult) (any, bool, error) {
	switch field {
	case "title":
		return track.Title, track.Title != "", nil
	case "sortTitle":
		return track.SortTitle, track.SortTitle != "", nil
	case "subtitle":
		return track.Subtitle, track.Subtitle != "", nil
	case "displayArtist":
		return track.DisplayArtist, track.DisplayArtist != "", nil
	case "releaseDate":
		return track.ReleaseDate, track.ReleaseDate != "", nil
	case "releaseYear":
		return track.ReleaseYear, track.ReleaseYear != 0, nil
	case "genres":
		return track.Genres, len(track.Genres) > 0, nil
	case "moods":
		return track.Moods, len(track.Moods) > 0, nil
	case "tags":
		return track.Tags, len(track.Tags) > 0, nil
	case "explicit":
		return track.Explicit, true, nil
	case "cover":
		return track.Images, len(track.Images) > 0, nil
	case "externalIds":
		return track.ExternalIDs, !externalIDsEmpty(track.ExternalIDs), nil
	case "artists":
		return candidate.Authors, len(candidate.Authors) > 0, nil
	default:
		return nil, false, nil
	}
}

func audiobookOverrideValue(item catalog.AudiobookItem, field string, candidate SearchResult) (any, bool, error) {
	book := item.Book
	if book == nil {
		book = &catalog.BookMetadata{}
	}
	switch field {
	case "title":
		return book.Title, book.Title != "", nil
	case "subtitle":
		return book.Subtitle, book.Subtitle != "", nil
	case "sortTitle":
		return book.SortTitle, book.SortTitle != "", nil
	case "description":
		return book.Description, book.Description != "", nil
	case "publisher":
		return book.Publisher, book.Publisher != "", nil
	case "publishedDate":
		return book.PublishedDate, book.PublishedDate != "", nil
	case "publishedYear":
		return book.PublishedYear, book.PublishedYear != "", nil
	case "language":
		return book.Language, book.Language != "", nil
	case "genres":
		return book.Genres, len(book.Genres) > 0, nil
	case "tags":
		return book.Tags, len(book.Tags) > 0, nil
	case "explicit":
		return book.Explicit, true, nil
	case "abridged":
		return book.Abridged, true, nil
	case "authors":
		return candidate.Authors, len(candidate.Authors) > 0, nil
	case "narrators":
		return candidate.Narrators, len(candidate.Narrators) > 0, nil
	case "series":
		return candidate.Series, len(candidate.Series) > 0, nil
	case "cover":
		if item.Cover == nil {
			return nil, false, nil
		}
		return *item.Cover, true, nil
	case "externalIds":
		return book.ExternalIDs, !externalIDsEmpty(book.ExternalIDs), nil
	default:
		return nil, false, nil
	}
}

func podcastOverrideValue(item catalog.PodcastItem, field string) (any, bool, error) {
	podcast := item.Podcast
	if podcast == nil {
		podcast = &catalog.PodcastMetadata{}
	}
	switch field {
	case "title":
		return podcast.Title, podcast.Title != "", nil
	case "description":
		return podcast.Description, podcast.Description != "", nil
	case "author":
		return podcast.Author, podcast.Author != "", nil
	case "siteUrl":
		return podcast.SiteURL, podcast.SiteURL != "", nil
	case "language":
		return podcast.Language, podcast.Language != "", nil
	case "genres":
		return item.Genres, len(item.Genres) > 0, nil
	case "categories":
		return podcast.Categories, len(podcast.Categories) > 0, nil
	case "explicit":
		return podcast.Explicit, true, nil
	case "cover":
		if item.Cover == nil {
			return nil, false, nil
		}
		return *item.Cover, true, nil
	case "externalIds":
		return podcast.ExternalIDs, !externalIDsEmpty(podcast.ExternalIDs), nil
	default:
		return nil, false, nil
	}
}

func podcastEpisodeOverrideValue(episode catalog.PodcastEpisode, field string) (any, bool, error) {
	switch field {
	case "title":
		return episode.Title, episode.Title != "", nil
	case "subtitle":
		return episode.Subtitle, episode.Subtitle != "", nil
	case "description":
		return episode.Description, episode.Description != "", nil
	case "publishedAt":
		if episode.PublishedAt == nil {
			return nil, false, nil
		}
		return episode.PublishedAt.UTC(), true, nil
	case "explicit":
		return episode.Explicit, true, nil
	case "externalIds":
		return episode.ExternalIDs, !externalIDsEmpty(episode.ExternalIDs), nil
	default:
		return nil, false, nil
	}
}

func podcastFeedOverrideValue(feed podcastFeedApplyRow, field string) (any, bool, error) {
	switch field {
	case "title":
		return feed.Title, feed.Title != "", nil
	case "description":
		return feed.Description, feed.Description != "", nil
	case "author":
		return feed.Author, feed.Author != "", nil
	case "siteUrl":
		return feed.SiteURL, feed.SiteURL != "", nil
	case "imageUrl":
		return feed.ImageURL, strings.TrimSpace(feed.ImageURL) != "", nil
	case "language":
		return feed.Language, feed.Language != "", nil
	case "categories":
		return feed.Categories, len(feed.Categories) > 0, nil
	case "explicit":
		return feed.Explicit, true, nil
	case "externalIds":
		return feed.ExternalIDs, !externalIDsEmpty(feed.ExternalIDs), nil
	case "cover":
		if feed.Cover == nil {
			if strings.TrimSpace(feed.ImageURL) == "" {
				return nil, false, nil
			}
			return catalog.Image{URL: feed.ImageURL}, true, nil
		}
		return *feed.Cover, true, nil
	default:
		return nil, false, nil
	}
}
