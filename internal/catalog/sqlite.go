package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jakedebus/samo-server/internal/media"
)

func LoadSeedFromDB(ctx context.Context, db *sql.DB) (Seed, error) {
	artists, err := loadMusicArtists(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	albums, err := loadMusicAlbums(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	tracks, err := loadMusicTracks(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	playlists, err := loadMusicPlaylists(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	genres, err := loadGenres(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	libraries, err := loadShelfLibraries(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	items, err := loadShelfItems(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	authors, err := loadShelfAuthors(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	series, err := loadShelfSeries(ctx, db)
	if err != nil {
		return Seed{}, err
	}
	episodes, err := loadPodcastEpisodes(ctx, db)
	if err != nil {
		return Seed{}, err
	}

	return Seed{
		MusicArtists:    artists,
		MusicAlbums:     albums,
		MusicTracks:     tracks,
		MusicPlaylists:  playlists,
		Genres:          genres,
		ShelfLibraries:  libraries,
		ShelfItems:      items,
		ShelfAuthors:    authors,
		ShelfSeries:     series,
		PodcastEpisodes: episodes,
	}, nil
}

func loadMusicArtists(ctx context.Context, db *sql.DB) ([]MusicArtist, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, sort_name, disambiguation, biography, country, genres_json, styles_json, moods_json,
		       links_json, images_json, external_ids_json, album_count, track_count, duration_seconds,
		       playback_json, added_at, updated_at
		FROM music_artists`)
	if err != nil {
		return nil, fmt.Errorf("load music artists: %w", err)
	}
	defer rows.Close()

	var items []MusicArtist
	for rows.Next() {
		var item MusicArtist
		var genresJSON, stylesJSON, moodsJSON, linksJSON, imagesJSON, externalJSON, playbackJSON string
		var addedAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.SortName, &item.Disambiguation, &item.Biography, &item.Country,
			&genresJSON, &stylesJSON, &moodsJSON, &linksJSON, &imagesJSON, &externalJSON, &item.AlbumCount,
			&item.TrackCount, &item.DurationSeconds, &playbackJSON, &addedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan music artist: %w", err)
		}
		decodeJSON(genresJSON, &item.Genres)
		decodeJSON(stylesJSON, &item.Styles)
		decodeJSON(moodsJSON, &item.Moods)
		decodeJSON(linksJSON, &item.Links)
		decodeJSON(imagesJSON, &item.Images)
		decodeJSON(externalJSON, &item.ExternalIDs)
		decodeJSON(playbackJSON, &item.Playback)
		item.AddedAt = parseTimePtr(addedAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadMusicAlbums(ctx context.Context, db *sql.DB) ([]MusicAlbum, error) {
	artistRefs, err := loadAlbumArtistRefs(ctx, db)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, title, sort_title, version, display_artist, release_date, original_release_date, release_year,
		       release_type, release_status, compilation, record_label, catalog_number, barcode, genres_json, styles_json,
		       moods_json, tags_json, disc_count, track_count, duration_seconds, images_json, external_ids_json,
		       playback_json, added_at, updated_at
		FROM music_albums`)
	if err != nil {
		return nil, fmt.Errorf("load music albums: %w", err)
	}
	defer rows.Close()

	var items []MusicAlbum
	for rows.Next() {
		var item MusicAlbum
		var compilation int
		var genresJSON, stylesJSON, moodsJSON, tagsJSON, imagesJSON, externalJSON, playbackJSON string
		var addedAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.SortTitle, &item.Version, &item.DisplayArtist, &item.ReleaseDate,
			&item.OriginalReleaseDate, &item.ReleaseYear, &item.ReleaseType, &item.ReleaseStatus,
			&compilation, &item.RecordLabel, &item.CatalogNumber, &item.Barcode, &genresJSON, &stylesJSON, &moodsJSON,
			&tagsJSON, &item.DiscCount, &item.TrackCount, &item.DurationSeconds, &imagesJSON, &externalJSON,
			&playbackJSON, &addedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan music album: %w", err)
		}
		item.Compilation = compilation != 0
		decodeJSON(genresJSON, &item.Genres)
		decodeJSON(stylesJSON, &item.Styles)
		decodeJSON(moodsJSON, &item.Moods)
		decodeJSON(tagsJSON, &item.Tags)
		decodeJSON(imagesJSON, &item.Images)
		decodeJSON(externalJSON, &item.ExternalIDs)
		decodeJSON(playbackJSON, &item.Playback)
		item.AlbumArtistIDs = artistRefs[item.ID].ids
		item.AlbumArtistNames = artistRefs[item.ID].names
		item.AddedAt = parseTimePtr(addedAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadMusicTracks(ctx context.Context, db *sql.DB) ([]MusicTrack, error) {
	artistRefs, err := loadTrackArtistRefs(ctx, db)
	if err != nil {
		return nil, err
	}
	files, err := loadAudioFiles(ctx, db, "track_id")
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, title, sort_title, subtitle, display_artist, album_id, album_title, disc_number, track_number, total_discs,
		       total_tracks, release_date, release_year, genres_json, moods_json, tags_json, duration_seconds,
		       explicit, bpm, musical_key, comment, lyrics_json, images_json, external_ids_json, playback_json,
		       added_at, updated_at
		FROM music_tracks`)
	if err != nil {
		return nil, fmt.Errorf("load music tracks: %w", err)
	}
	defer rows.Close()

	var items []MusicTrack
	for rows.Next() {
		var item MusicTrack
		var albumID sql.NullString
		var genresJSON, moodsJSON, tagsJSON, lyricsJSON, imagesJSON, externalJSON, playbackJSON string
		var explicit int
		var addedAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.SortTitle, &item.Subtitle, &item.DisplayArtist, &albumID, &item.AlbumTitle,
			&item.DiscNumber, &item.TrackNumber, &item.TotalDiscs, &item.TotalTracks, &item.ReleaseDate,
			&item.ReleaseYear, &genresJSON, &moodsJSON, &tagsJSON, &item.DurationSeconds, &explicit,
			&item.BPM, &item.Key, &item.Comment, &lyricsJSON, &imagesJSON, &externalJSON, &playbackJSON,
			&addedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan music track: %w", err)
		}
		item.AlbumID = albumID.String
		item.Explicit = explicit != 0
		decodeJSON(genresJSON, &item.Genres)
		decodeJSON(moodsJSON, &item.Moods)
		decodeJSON(tagsJSON, &item.Tags)
		decodeJSON(lyricsJSON, &item.Lyrics)
		decodeJSON(imagesJSON, &item.Images)
		decodeJSON(externalJSON, &item.ExternalIDs)
		decodeJSON(playbackJSON, &item.Playback)
		item.ArtistIDs = artistRefs[item.ID].ids
		item.ArtistNames = artistRefs[item.ID].names
		item.AudioFiles = files[item.ID]
		item.AddedAt = parseTimePtr(addedAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadMusicPlaylists(ctx context.Context, db *sql.DB) ([]MusicPlaylist, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, owner_id, public, collaborative, track_ids_json, track_count,
		       duration_seconds, images_json, playback_json, created_at, updated_at
		FROM music_playlists`)
	if err != nil {
		return nil, fmt.Errorf("load music playlists: %w", err)
	}
	defer rows.Close()

	var items []MusicPlaylist
	for rows.Next() {
		var item MusicPlaylist
		var public, collaborative int
		var trackIDsJSON, imagesJSON, playbackJSON string
		var createdAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.OwnerID, &public, &collaborative,
			&trackIDsJSON, &item.TrackCount, &item.DurationSeconds, &imagesJSON, &playbackJSON,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan music playlist: %w", err)
		}
		item.Public = public != 0
		item.Collaborative = collaborative != 0
		decodeJSON(trackIDsJSON, &item.TrackIDs)
		decodeJSON(imagesJSON, &item.Images)
		decodeJSON(playbackJSON, &item.Playback)
		item.CreatedAt = parseTimePtr(createdAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadGenres(ctx context.Context, db *sql.DB) ([]GenreSummary, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, kind, item_count, track_count, album_count FROM genres`)
	if err != nil {
		return nil, fmt.Errorf("load genres: %w", err)
	}
	defer rows.Close()

	var items []GenreSummary
	for rows.Next() {
		var item GenreSummary
		var kind string
		if err := rows.Scan(&item.Name, &kind, &item.ItemCount, &item.TrackCount, &item.AlbumCount); err != nil {
			return nil, fmt.Errorf("scan genre: %w", err)
		}
		item.Kind = media.Kind(kind)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadShelfLibraries(ctx context.Context, db *sql.DB) ([]ShelfLibrary, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, media_type, path, description, item_count, created_at, updated_at FROM libraries WHERE kind = 'shelf'`)
	if err != nil {
		return nil, fmt.Errorf("load shelf libraries: %w", err)
	}
	defer rows.Close()

	var items []ShelfLibrary
	for rows.Next() {
		var item ShelfLibrary
		var createdAt, updatedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.MediaType, &item.Path, &item.Description, &item.ItemCount, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan shelf library: %w", err)
		}
		item.CreatedAt = parseTimePtr(createdAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadShelfItems(ctx context.Context, db *sql.DB) ([]ShelfItem, error) {
	files, err := loadAudioFiles(ctx, db, "item_id")
	if err != nil {
		return nil, err
	}
	chapters, err := loadChapters(ctx, db, false)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, library_id, media_type, media_kind, path, folder_id, inode, size_bytes, missing, invalid,
		       cover_json, tags_json, genres_json, duration_seconds, progress_json, book_json, podcast_json,
		       added_at, updated_at, last_scan_at
		FROM shelf_items`)
	if err != nil {
		return nil, fmt.Errorf("load shelf items: %w", err)
	}
	defer rows.Close()

	var items []ShelfItem
	for rows.Next() {
		var item ShelfItem
		var missing, invalid int
		var coverJSON, tagsJSON, genresJSON, progressJSON string
		var bookJSON, podcastJSON sql.NullString
		var addedAt, updatedAt, lastScanAt sql.NullString
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.MediaType, &item.MediaKind, &item.Path, &item.FolderID,
			&item.Inode, &item.SizeBytes, &missing, &invalid, &coverJSON, &tagsJSON, &genresJSON,
			&item.DurationSeconds, &progressJSON, &bookJSON, &podcastJSON, &addedAt, &updatedAt, &lastScanAt); err != nil {
			return nil, fmt.Errorf("scan shelf item: %w", err)
		}
		item.Missing = missing != 0
		item.Invalid = invalid != 0
		var cover Image
		decodeJSON(coverJSON, &cover)
		if cover.ID != "" || cover.URL != "" || cover.Path != "" {
			item.Cover = &cover
		}
		decodeJSON(tagsJSON, &item.Tags)
		decodeJSON(genresJSON, &item.Genres)
		decodeJSON(progressJSON, &item.Progress)
		if bookJSON.Valid && bookJSON.String != "" {
			var book BookMetadata
			decodeJSON(bookJSON.String, &book)
			item.Book = &book
		}
		if podcastJSON.Valid && podcastJSON.String != "" {
			var podcast PodcastMetadata
			decodeJSON(podcastJSON.String, &podcast)
			item.Podcast = &podcast
		}
		item.AudioFiles = files[item.ID]
		item.Chapters = chapters[item.ID]
		item.AddedAt = parseTimePtr(addedAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		item.LastScanAt = parseTimePtr(lastScanAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadShelfAuthors(ctx context.Context, db *sql.DB) ([]ShelfAuthor, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, sort_name, description, images_json, external_ids_json, item_count, series_count, duration_seconds FROM shelf_authors`)
	if err != nil {
		return nil, fmt.Errorf("load shelf authors: %w", err)
	}
	defer rows.Close()

	var items []ShelfAuthor
	for rows.Next() {
		var item ShelfAuthor
		var imagesJSON, externalJSON string
		if err := rows.Scan(&item.ID, &item.Name, &item.SortName, &item.Description, &imagesJSON, &externalJSON, &item.ItemCount, &item.SeriesCount, &item.DurationSeconds); err != nil {
			return nil, fmt.Errorf("scan shelf author: %w", err)
		}
		decodeJSON(imagesJSON, &item.Images)
		decodeJSON(externalJSON, &item.ExternalIDs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadShelfSeries(ctx context.Context, db *sql.DB) ([]ShelfSeries, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, description, authors_json, item_ids_json, item_count, duration_seconds, external_ids_json FROM shelf_series`)
	if err != nil {
		return nil, fmt.Errorf("load shelf series: %w", err)
	}
	defer rows.Close()

	var items []ShelfSeries
	for rows.Next() {
		var item ShelfSeries
		var authorsJSON, itemIDsJSON, externalJSON string
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &authorsJSON, &itemIDsJSON, &item.ItemCount, &item.DurationSeconds, &externalJSON); err != nil {
			return nil, fmt.Errorf("scan shelf series: %w", err)
		}
		decodeJSON(authorsJSON, &item.Authors)
		decodeJSON(itemIDsJSON, &item.ItemIDs)
		decodeJSON(externalJSON, &item.ExternalIDs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadPodcastEpisodes(ctx context.Context, db *sql.DB) ([]PodcastEpisode, error) {
	files, err := loadAudioFiles(ctx, db, "episode_id")
	if err != nil {
		return nil, err
	}
	chapters, err := loadChapters(ctx, db, true)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, library_id, podcast_id, title, subtitle, description, published_at, season, episode,
		       episode_type, duration_seconds, explicit, enclosure_url, enclosure_type, enclosure_bytes,
		       progress_json, external_ids_json, added_at, updated_at
		FROM podcast_episodes`)
	if err != nil {
		return nil, fmt.Errorf("load podcast episodes: %w", err)
	}
	defer rows.Close()

	var items []PodcastEpisode
	for rows.Next() {
		var item PodcastEpisode
		var publishedAt, addedAt, updatedAt sql.NullString
		var explicit int
		var progressJSON, externalJSON string
		if err := rows.Scan(&item.ID, &item.LibraryID, &item.PodcastID, &item.Title, &item.Subtitle, &item.Description,
			&publishedAt, &item.Season, &item.Episode, &item.EpisodeType, &item.DurationSeconds, &explicit,
			&item.EnclosureURL, &item.EnclosureType, &item.EnclosureBytes, &progressJSON, &externalJSON,
			&addedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan podcast episode: %w", err)
		}
		item.Explicit = explicit != 0
		decodeJSON(progressJSON, &item.Progress)
		decodeJSON(externalJSON, &item.ExternalIDs)
		item.PublishedAt = parseTimePtr(publishedAt)
		item.AddedAt = parseTimePtr(addedAt)
		item.UpdatedAt = parseTimePtr(updatedAt)
		item.AudioFiles = files[item.ID]
		item.Chapters = chapters[item.ID]
		items = append(items, item)
	}
	return items, rows.Err()
}

type namedRefs struct {
	ids   []string
	names []string
}

func loadAlbumArtistRefs(ctx context.Context, db *sql.DB) (map[string]namedRefs, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT aa.album_id, a.id, a.name
		FROM music_album_artists aa
		JOIN music_artists a ON a.id = aa.artist_id
		ORDER BY aa.position`)
	if err != nil {
		return nil, fmt.Errorf("load album artist refs: %w", err)
	}
	defer rows.Close()

	refs := map[string]namedRefs{}
	for rows.Next() {
		var ownerID, id, name string
		if err := rows.Scan(&ownerID, &id, &name); err != nil {
			return nil, fmt.Errorf("scan album artist ref: %w", err)
		}
		ref := refs[ownerID]
		ref.ids = append(ref.ids, id)
		ref.names = append(ref.names, name)
		refs[ownerID] = ref
	}
	return refs, rows.Err()
}

func loadTrackArtistRefs(ctx context.Context, db *sql.DB) (map[string]namedRefs, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ta.track_id, a.id, a.name
		FROM music_track_artists ta
		JOIN music_artists a ON a.id = ta.artist_id
		ORDER BY ta.position`)
	if err != nil {
		return nil, fmt.Errorf("load track artist refs: %w", err)
	}
	defer rows.Close()

	refs := map[string]namedRefs{}
	for rows.Next() {
		var ownerID, id, name string
		if err := rows.Scan(&ownerID, &id, &name); err != nil {
			return nil, fmt.Errorf("scan track artist ref: %w", err)
		}
		ref := refs[ownerID]
		ref.ids = append(ref.ids, id)
		ref.names = append(ref.names, name)
		refs[ownerID] = ref
	}
	return refs, rows.Err()
}

func loadAudioFiles(ctx context.Context, db *sql.DB, ownerColumn string) (map[string][]AudioFile, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s, id, path, relative_path, file_name, container, mime_type, codec, codec_profile, metadata_formats_json, bitrate,
		       bit_depth, sample_rate, channels, channel_layout, duration_seconds, size_bytes, modified_at,
		       checksum, embedded_tags_json
		FROM media_files
		WHERE %s IS NOT NULL
		ORDER BY relative_path, file_name`, ownerColumn, ownerColumn))
	if err != nil {
		return nil, fmt.Errorf("load audio files: %w", err)
	}
	defer rows.Close()

	files := map[string][]AudioFile{}
	for rows.Next() {
		var ownerID string
		var item AudioFile
		var modifiedAt sql.NullString
		var metadataFormatsJSON, embeddedTagsJSON string
		if err := rows.Scan(&ownerID, &item.ID, &item.Path, &item.RelativePath, &item.FileName, &item.Container,
			&item.MimeType, &item.Codec, &item.CodecProfile, &metadataFormatsJSON, &item.Bitrate, &item.BitDepth, &item.SampleRate,
			&item.Channels, &item.ChannelLayout, &item.DurationSeconds, &item.SizeBytes, &modifiedAt,
			&item.Checksum, &embeddedTagsJSON); err != nil {
			return nil, fmt.Errorf("scan audio file: %w", err)
		}
		decodeJSON(metadataFormatsJSON, &item.MetadataFormats)
		decodeJSON(embeddedTagsJSON, &item.EmbeddedTags)
		item.ModifiedAt = parseTimePtr(modifiedAt)
		files[ownerID] = append(files[ownerID], item)
	}
	return files, rows.Err()
}

func loadChapters(ctx context.Context, db *sql.DB, episode bool) (map[string][]AudioChapter, error) {
	ownerColumn := "item_id"
	where := "episode_id IS NULL"
	if episode {
		ownerColumn = "episode_id"
		where = "episode_id IS NOT NULL"
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s, id, chapter_index, title, start_seconds, end_seconds
		FROM shelf_chapters
		WHERE %s
		ORDER BY chapter_index`, ownerColumn, where))
	if err != nil {
		return nil, fmt.Errorf("load chapters: %w", err)
	}
	defer rows.Close()

	chapters := map[string][]AudioChapter{}
	for rows.Next() {
		var ownerID string
		var item AudioChapter
		if err := rows.Scan(&ownerID, &item.ID, &item.Index, &item.Title, &item.StartSeconds, &item.EndSeconds); err != nil {
			return nil, fmt.Errorf("scan chapter: %w", err)
		}
		chapters[ownerID] = append(chapters[ownerID], item)
	}
	return chapters, rows.Err()
}

func decodeJSON(value string, out any) {
	if value == "" || value == "null" {
		return
	}
	_ = json.Unmarshal([]byte(value), out)
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"}
	for _, format := range formats {
		parsed, err := time.Parse(format, value.String)
		if err == nil {
			return &parsed
		}
	}
	return nil
}
