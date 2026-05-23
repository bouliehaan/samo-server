package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func (idx *OverrideIndex) GuardMusicArtist(ctx context.Context, db *sql.DB, incoming MusicArtist) (MusicArtist, error) {
	patch := idx.Patch(OverrideKindMusicArtist, incoming.ID)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingMusicArtist(ctx, db, incoming.ID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "name") {
		out.Name = existing.Name
	}
	if patchHasField(patch, "sortName") {
		out.SortName = existing.SortName
	}
	if patchHasField(patch, "description") {
		out.Disambiguation = existing.Disambiguation
	}
	if patchHasField(patch, "genres") {
		out.Genres = append([]string(nil), existing.Genres...)
	}
	if patchHasField(patch, "tags") {
		out.Moods = append([]string(nil), existing.Moods...)
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return out, nil
}

func (idx *OverrideIndex) GuardMusicAlbum(ctx context.Context, db *sql.DB, incoming MusicAlbum) (MusicAlbum, error) {
	patch := idx.Patch(OverrideKindMusicAlbum, incoming.ID)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingMusicAlbum(ctx, db, incoming.ID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "sortTitle") {
		out.SortTitle = existing.SortTitle
	}
	if patchHasField(patch, "version") {
		out.Version = existing.Version
	}
	if patchHasField(patch, "displayArtist") {
		out.DisplayArtist = existing.DisplayArtist
	}
	if patchHasField(patch, "releaseDate") {
		out.ReleaseDate = existing.ReleaseDate
	}
	if patchHasField(patch, "originalReleaseDate") {
		out.OriginalReleaseDate = existing.OriginalReleaseDate
	}
	if patchHasField(patch, "releaseYear") {
		out.ReleaseYear = existing.ReleaseYear
	}
	if patchHasField(patch, "releaseType") {
		out.ReleaseType = existing.ReleaseType
	}
	if patchHasField(patch, "recordLabel") {
		out.RecordLabel = existing.RecordLabel
	}
	if patchHasField(patch, "catalogNumber") {
		out.CatalogNumber = existing.CatalogNumber
	}
	if patchHasField(patch, "barcode") {
		out.Barcode = existing.Barcode
	}
	if patchHasField(patch, "genres") {
		out.Genres = append([]string(nil), existing.Genres...)
	}
	if patchHasField(patch, "styles") {
		out.Styles = append([]string(nil), existing.Styles...)
	}
	if patchHasField(patch, "moods") {
		out.Moods = append([]string(nil), existing.Moods...)
	}
	if patchHasField(patch, "tags") {
		out.Tags = append([]string(nil), existing.Tags...)
	}
	if patchHasField(patch, "cover") {
		out.Images = append([]Image(nil), existing.Images...)
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return out, nil
}

func (idx *OverrideIndex) GuardMusicTrack(ctx context.Context, db *sql.DB, incoming MusicTrack) (MusicTrack, error) {
	patch := idx.Patch(OverrideKindMusicTrack, incoming.ID)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingMusicTrack(ctx, db, incoming.ID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "sortTitle") {
		out.SortTitle = existing.SortTitle
	}
	if patchHasField(patch, "subtitle") {
		out.Subtitle = existing.Subtitle
	}
	if patchHasField(patch, "displayArtist") {
		out.DisplayArtist = existing.DisplayArtist
	}
	if patchHasField(patch, "releaseDate") {
		out.ReleaseDate = existing.ReleaseDate
	}
	if patchHasField(patch, "releaseYear") {
		out.ReleaseYear = existing.ReleaseYear
	}
	if patchHasField(patch, "genres") {
		out.Genres = append([]string(nil), existing.Genres...)
	}
	if patchHasField(patch, "moods") {
		out.Moods = append([]string(nil), existing.Moods...)
	}
	if patchHasField(patch, "tags") {
		out.Tags = append([]string(nil), existing.Tags...)
	}
	if patchHasField(patch, "explicit") {
		out.Explicit = existing.Explicit
	}
	if patchHasField(patch, "cover") {
		out.Images = append([]Image(nil), existing.Images...)
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return out, nil
}

func (idx *OverrideIndex) GuardShelfItem(ctx context.Context, db *sql.DB, incoming ShelfItem) (ShelfItem, error) {
	patch := idx.CombinedShelfItemPatch(incoming.ID, incoming.MediaType)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingShelfItem(ctx, db, incoming.ID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "cover") {
		out.Cover = existing.Cover
		if out.Cover != nil {
			copied := *out.Cover
			out.Cover = &copied
		}
	}
	if patchHasField(patch, "tags") {
		out.Tags = append([]string(nil), existing.Tags...)
	}
	if patchHasField(patch, "genres") || patchHasField(patch, "categories") {
		out.Genres = append([]string(nil), existing.Genres...)
	}
	if incoming.MediaType == ShelfMediaTypePodcast {
		out.Podcast = guardPodcastMetadata(existing.Podcast, out.Podcast, patch)
	} else {
		out.Book = guardBookMetadata(existing.Book, out.Book, patch)
	}
	return out, nil
}

func guardBookMetadata(existing, incoming *BookMetadata, patch MetadataOverridePatch) *BookMetadata {
	if incoming == nil {
		incoming = &BookMetadata{}
	}
	if existing == nil {
		existing = &BookMetadata{}
	}
	out := *incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "subtitle") {
		out.Subtitle = existing.Subtitle
	}
	if patchHasField(patch, "sortTitle") {
		out.SortTitle = existing.SortTitle
	}
	if patchHasField(patch, "description") {
		out.Description = existing.Description
	}
	if patchHasField(patch, "publisher") {
		out.Publisher = existing.Publisher
	}
	if patchHasField(patch, "publishedDate") {
		out.PublishedDate = existing.PublishedDate
	}
	if patchHasField(patch, "publishedYear") {
		out.PublishedYear = existing.PublishedYear
	}
	if patchHasField(patch, "language") {
		out.Language = existing.Language
	}
	if patchHasField(patch, "genres") {
		out.Genres = append([]string(nil), existing.Genres...)
	}
	if patchHasField(patch, "tags") {
		out.Tags = append([]string(nil), existing.Tags...)
	}
	if patchHasField(patch, "explicit") {
		out.Explicit = existing.Explicit
	}
	if patchHasField(patch, "abridged") {
		out.Abridged = existing.Abridged
	}
	if patchHasField(patch, "authors") {
		out.Authors = append([]Contributor(nil), existing.Authors...)
	}
	if patchHasField(patch, "narrators") {
		out.Narrators = append([]Contributor(nil), existing.Narrators...)
	}
	if patchHasField(patch, "series") {
		out.Series = append([]SeriesRef(nil), existing.Series...)
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return &out
}

func guardPodcastMetadata(existing, incoming *PodcastMetadata, patch MetadataOverridePatch) *PodcastMetadata {
	if incoming == nil {
		incoming = &PodcastMetadata{}
	}
	if existing == nil {
		existing = &PodcastMetadata{}
	}
	out := *incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "description") {
		out.Description = existing.Description
	}
	if patchHasField(patch, "author") {
		out.Author = existing.Author
	}
	if patchHasField(patch, "siteUrl") {
		out.SiteURL = existing.SiteURL
	}
	if patchHasField(patch, "language") {
		out.Language = existing.Language
	}
	if patchHasField(patch, "categories") || patchHasField(patch, "genres") {
		out.Categories = append([]string(nil), existing.Categories...)
	}
	if patchHasField(patch, "explicit") {
		out.Explicit = existing.Explicit
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return &out
}

func (idx *OverrideIndex) GuardPodcastEpisode(ctx context.Context, db *sql.DB, incoming PodcastEpisode) (PodcastEpisode, error) {
	patch := idx.Patch(OverrideKindShelfEpisode, incoming.ID)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingPodcastEpisode(ctx, db, incoming.ID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "subtitle") {
		out.Subtitle = existing.Subtitle
	}
	if patchHasField(patch, "description") {
		out.Description = existing.Description
	}
	if patchHasField(patch, "publishedAt") {
		out.PublishedAt = existing.PublishedAt
	}
	if patchHasField(patch, "explicit") {
		out.Explicit = existing.Explicit
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return out, nil
}

// PodcastFeedWriteRow is the metadata subset written by RSS ingestion.
type PodcastFeedWriteRow struct {
	FeedID      string
	PodcastID   string
	Title       string
	Description string
	Author      string
	SiteURL     string
	ImageURL    string
	Language    string
	Explicit    bool
	Categories  []string
	Cover       *Image
	ExternalIDs ExternalIDs
}

func (idx *OverrideIndex) GuardPodcastFeedRow(ctx context.Context, db *sql.DB, incoming PodcastFeedWriteRow) (PodcastFeedWriteRow, error) {
	patch := idx.Patch(OverrideKindPodcastFeed, incoming.FeedID)
	if len(patch) == 0 {
		return incoming, nil
	}
	existing, found, err := loadExistingPodcastFeedRow(ctx, db, incoming.FeedID, incoming.PodcastID)
	if err != nil {
		return incoming, err
	}
	if !found {
		return incoming, nil
	}
	out := incoming
	if patchHasField(patch, "title") {
		out.Title = existing.Title
	}
	if patchHasField(patch, "description") {
		out.Description = existing.Description
	}
	if patchHasField(patch, "author") {
		out.Author = existing.Author
	}
	if patchHasField(patch, "siteUrl") {
		out.SiteURL = existing.SiteURL
	}
	if patchHasField(patch, "imageUrl") || patchHasField(patch, "cover") {
		out.ImageURL = existing.ImageURL
		out.Cover = existing.Cover
		if out.Cover != nil {
			copied := *out.Cover
			out.Cover = &copied
		}
	}
	if patchHasField(patch, "language") {
		out.Language = existing.Language
	}
	if patchHasField(patch, "categories") {
		out.Categories = append([]string(nil), existing.Categories...)
	}
	if patchHasField(patch, "explicit") {
		out.Explicit = existing.Explicit
	}
	if patchHasField(patch, "externalIds") {
		out.ExternalIDs = existing.ExternalIDs
	}
	return out, nil
}

func patchHasField(patch MetadataOverridePatch, field string) bool {
	if len(patch) == 0 {
		return false
	}
	_, ok := patch[field]
	return ok
}

func loadExistingMusicArtist(ctx context.Context, db *sql.DB, id string) (MusicArtist, bool, error) {
	var artist MusicArtist
	var genresJSON, moodsJSON, externalJSON string
	err := db.QueryRowContext(ctx, `
		SELECT name, sort_name, disambiguation, genres_json, moods_json, external_ids_json
		FROM music_artists WHERE id = ?`, id).
		Scan(&artist.Name, &artist.SortName, &artist.Disambiguation, &genresJSON, &moodsJSON, &externalJSON)
	if err == sql.ErrNoRows {
		return MusicArtist{}, false, nil
	}
	if err != nil {
		return MusicArtist{}, false, fmt.Errorf("load existing music artist: %w", err)
	}
	decodeJSONString(genresJSON, &artist.Genres)
	decodeJSONString(moodsJSON, &artist.Moods)
	decodeJSONString(externalJSON, &artist.ExternalIDs)
	artist.ID = id
	return artist, true, nil
}

func loadExistingMusicAlbum(ctx context.Context, db *sql.DB, id string) (MusicAlbum, bool, error) {
	var album MusicAlbum
	var genresJSON, stylesJSON, moodsJSON, tagsJSON, imagesJSON, externalJSON string
	err := db.QueryRowContext(ctx, `
		SELECT title, sort_title, version, display_artist, release_date, original_release_date, release_year,
		       release_type, record_label, catalog_number, barcode, genres_json, styles_json, moods_json,
		       tags_json, images_json, external_ids_json
		FROM music_albums WHERE id = ?`, id).
		Scan(&album.Title, &album.SortTitle, &album.Version, &album.DisplayArtist, &album.ReleaseDate,
			&album.OriginalReleaseDate, &album.ReleaseYear, &album.ReleaseType, &album.RecordLabel,
			&album.CatalogNumber, &album.Barcode, &genresJSON, &stylesJSON, &moodsJSON, &tagsJSON, &imagesJSON, &externalJSON)
	if err == sql.ErrNoRows {
		return MusicAlbum{}, false, nil
	}
	if err != nil {
		return MusicAlbum{}, false, fmt.Errorf("load existing music album: %w", err)
	}
	decodeJSONString(genresJSON, &album.Genres)
	decodeJSONString(stylesJSON, &album.Styles)
	decodeJSONString(moodsJSON, &album.Moods)
	decodeJSONString(tagsJSON, &album.Tags)
	decodeJSONString(imagesJSON, &album.Images)
	decodeJSONString(externalJSON, &album.ExternalIDs)
	album.ID = id
	return album, true, nil
}

func loadExistingMusicTrack(ctx context.Context, db *sql.DB, id string) (MusicTrack, bool, error) {
	var track MusicTrack
	var genresJSON, moodsJSON, tagsJSON, imagesJSON, externalJSON string
	var explicit int
	err := db.QueryRowContext(ctx, `
		SELECT title, sort_title, subtitle, display_artist, release_date, release_year, genres_json, moods_json,
		       tags_json, explicit, images_json, external_ids_json
		FROM music_tracks WHERE id = ?`, id).
		Scan(&track.Title, &track.SortTitle, &track.Subtitle, &track.DisplayArtist, &track.ReleaseDate,
			&track.ReleaseYear, &genresJSON, &moodsJSON, &tagsJSON, &explicit, &imagesJSON, &externalJSON)
	if err == sql.ErrNoRows {
		return MusicTrack{}, false, nil
	}
	if err != nil {
		return MusicTrack{}, false, fmt.Errorf("load existing music track: %w", err)
	}
	track.Explicit = explicit != 0
	decodeJSONString(genresJSON, &track.Genres)
	decodeJSONString(moodsJSON, &track.Moods)
	decodeJSONString(tagsJSON, &track.Tags)
	decodeJSONString(imagesJSON, &track.Images)
	decodeJSONString(externalJSON, &track.ExternalIDs)
	track.ID = id
	return track, true, nil
}

func loadExistingShelfItem(ctx context.Context, db *sql.DB, id string) (ShelfItem, bool, error) {
	var item ShelfItem
	var coverJSON, tagsJSON, genresJSON string
	var bookJSON, podcastJSON sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT media_type, cover_json, tags_json, genres_json, book_json, podcast_json
		FROM shelf_items WHERE id = ?`, id).
		Scan(&item.MediaType, &coverJSON, &tagsJSON, &genresJSON, &bookJSON, &podcastJSON)
	if err == sql.ErrNoRows {
		return ShelfItem{}, false, nil
	}
	if err != nil {
		return ShelfItem{}, false, fmt.Errorf("load existing shelf item: %w", err)
	}
	var cover Image
	decodeJSONString(coverJSON, &cover)
	if cover.ID != "" || cover.URL != "" || cover.Path != "" {
		item.Cover = &cover
	}
	decodeJSONString(tagsJSON, &item.Tags)
	decodeJSONString(genresJSON, &item.Genres)
	if bookJSON.Valid && bookJSON.String != "" {
		var book BookMetadata
		decodeJSONString(bookJSON.String, &book)
		item.Book = &book
	}
	if podcastJSON.Valid && podcastJSON.String != "" {
		var podcast PodcastMetadata
		decodeJSONString(podcastJSON.String, &podcast)
		item.Podcast = &podcast
	}
	item.ID = id
	return item, true, nil
}

func loadExistingPodcastEpisode(ctx context.Context, db *sql.DB, id string) (PodcastEpisode, bool, error) {
	var episode PodcastEpisode
	var explicit int
	var externalJSON string
	var publishedAt sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT title, subtitle, description, published_at, explicit, external_ids_json
		FROM podcast_episodes WHERE id = ?`, id).
		Scan(&episode.Title, &episode.Subtitle, &episode.Description, &publishedAt, &explicit, &externalJSON)
	if err == sql.ErrNoRows {
		return PodcastEpisode{}, false, nil
	}
	if err != nil {
		return PodcastEpisode{}, false, fmt.Errorf("load existing podcast episode: %w", err)
	}
	episode.Explicit = explicit != 0
	episode.PublishedAt = parseTimePtr(publishedAt)
	decodeJSONString(externalJSON, &episode.ExternalIDs)
	episode.ID = id
	return episode, true, nil
}

func loadExistingPodcastFeedRow(ctx context.Context, db *sql.DB, feedID, podcastID string) (PodcastFeedWriteRow, bool, error) {
	var row PodcastFeedWriteRow
	var explicit int
	var categoriesJSON string
	var coverJSON, podcastJSON sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT f.title, f.description, f.author, f.site_url, f.image_url, f.language, f.explicit, f.categories_json,
		       i.cover_json, i.podcast_json
		FROM podcast_feeds f
		LEFT JOIN shelf_items i ON i.id = f.podcast_id
		WHERE f.id = ?`, feedID).
		Scan(&row.Title, &row.Description, &row.Author, &row.SiteURL, &row.ImageURL, &row.Language, &explicit,
			&categoriesJSON, &coverJSON, &podcastJSON)
	if err == sql.ErrNoRows {
		return PodcastFeedWriteRow{}, false, nil
	}
	if err != nil {
		return PodcastFeedWriteRow{}, false, fmt.Errorf("load existing podcast feed: %w", err)
	}
	row.FeedID = feedID
	row.PodcastID = podcastID
	row.Explicit = explicit != 0
	decodeJSONString(categoriesJSON, &row.Categories)
	if coverJSON.Valid && coverJSON.String != "" {
		var cover Image
		decodeJSONString(coverJSON.String, &cover)
		if cover.URL != "" || cover.Path != "" || cover.ID != "" {
			row.Cover = &cover
			if row.ImageURL == "" {
				row.ImageURL = cover.URL
			}
		}
	}
	if podcastJSON.Valid && podcastJSON.String != "" {
		var podcast PodcastMetadata
		decodeJSONString(podcastJSON.String, &podcast)
		row.ExternalIDs = podcast.ExternalIDs
	}
	return row, true, nil
}

func decodeJSONString(value string, out any) {
	if value == "" || value == "null" {
		return
	}
	_ = json.Unmarshal([]byte(value), out)
}
