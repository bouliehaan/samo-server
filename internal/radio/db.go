package radio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/media"
)

// ErrItemNotFound is returned when a station item lookup misses.
var ErrItemNotFound = errors.New("radio item not found")

// StationRecord is the DB-backed view of a station before hydration into a
// runnable schedule.
type StationRecord struct {
	ID          string
	Name        string
	Description string
	ContentType string
	Epoch       string
	Enabled     bool
	Source      string
	Items       []StationItem
}

// LoadStationsFromDB hydrates all stored stations and resolves their item
// references against music tracks, audiobooks, and podcast episodes so the
// runtime has playable file paths.
func LoadStationsFromDB(ctx context.Context, db *sql.DB) ([]StationRecord, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, content_type, epoch, enabled, source
		FROM radio_stations
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list radio stations: %w", err)
	}
	defer rows.Close()

	var stations []StationRecord
	for rows.Next() {
		var station StationRecord
		var enabled int
		if err := rows.Scan(&station.ID, &station.Name, &station.Description,
			&station.ContentType, &station.Epoch, &enabled, &station.Source); err != nil {
			return nil, fmt.Errorf("scan radio station: %w", err)
		}
		station.Enabled = enabled != 0
		stations = append(stations, station)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for index := range stations {
		items, err := loadStationItems(ctx, db, stations[index].ID)
		if err != nil {
			return nil, err
		}
		stations[index].Items = items
	}
	return stations, nil
}

// LoadStationByID returns the single station record with resolved items, or
// ErrStationNotFound when no row matches.
func LoadStationByID(ctx context.Context, db *sql.DB, id string) (StationRecord, error) {
	if db == nil {
		return StationRecord{}, ErrStationNotFound
	}
	id = strings.TrimSpace(id)
	row := db.QueryRowContext(ctx, `
		SELECT id, name, description, content_type, epoch, enabled, source
		FROM radio_stations
		WHERE id = ?`, id)
	var station StationRecord
	var enabled int
	if err := row.Scan(&station.ID, &station.Name, &station.Description,
		&station.ContentType, &station.Epoch, &enabled, &station.Source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StationRecord{}, ErrStationNotFound
		}
		return StationRecord{}, fmt.Errorf("load radio station: %w", err)
	}
	station.Enabled = enabled != 0
	items, err := loadStationItems(ctx, db, station.ID)
	if err != nil {
		return StationRecord{}, err
	}
	station.Items = items
	return station, nil
}

func loadStationItems(ctx context.Context, db *sql.DB, stationID string) ([]StationItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, position, source_kind, source_id, source_path, title, artist, album,
		       kind, duration_seconds, weight
		FROM radio_station_items
		WHERE station_id = ?
		ORDER BY position, id`, stationID)
	if err != nil {
		return nil, fmt.Errorf("list radio items: %w", err)
	}
	defer rows.Close()

	var items []StationItem
	for rows.Next() {
		var item StationItem
		if err := rows.Scan(&item.ID, &item.Position, &item.SourceKind, &item.SourceID,
			&item.SourcePath, &item.Title, &item.Artist, &item.Album, &item.Kind,
			&item.DurationSeconds, &item.Weight); err != nil {
			return nil, fmt.Errorf("scan radio item: %w", err)
		}
		item.StationID = stationID
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		if err := resolveItem(ctx, db, &items[index]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func resolveItem(ctx context.Context, db *sql.DB, item *StationItem) error {
	switch item.SourceKind {
	case ItemSourcePath:
		item.ResolvedPath = strings.TrimSpace(item.SourcePath)
		if item.ResolvedPath == "" {
			item.Missing = true
		}
		return nil
	case ItemSourceMusicTrack:
		return resolveMusicTrack(ctx, db, item)
	case ItemSourceAudiobook:
		return resolveAudiobook(ctx, db, item)
	case ItemSourcePodcastEpisode:
		return resolvePodcastEpisode(ctx, db, item)
	default:
		item.Missing = true
		return nil
	}
}

func resolveMusicTrack(ctx context.Context, db *sql.DB, item *StationItem) error {
	if item.SourceID == "" {
		item.Missing = true
		return nil
	}
	var path, title, artist, album sql.NullString
	var duration sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT mf.path, t.title, t.display_artist, a.title, mf.duration_seconds
		FROM music_tracks t
		LEFT JOIN music_albums a ON a.id = t.album_id
		LEFT JOIN media_files mf ON mf.track_id = t.id
		WHERE t.id = ?
		ORDER BY mf.relative_path, mf.id
		LIMIT 1`, item.SourceID).Scan(&path, &title, &artist, &album, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		item.Missing = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve music track %q: %w", item.SourceID, err)
	}
	if !path.Valid || strings.TrimSpace(path.String) == "" {
		item.Missing = true
		return nil
	}
	item.ResolvedPath = path.String
	if item.Title == "" && title.Valid {
		item.Title = title.String
	}
	if item.Artist == "" && artist.Valid {
		item.Artist = artist.String
	}
	if item.Album == "" && album.Valid {
		item.Album = album.String
	}
	if item.DurationSeconds <= 0 && duration.Valid {
		item.DurationSeconds = int(duration.Int64)
	}
	if item.Kind == "" || item.Kind == string(media.KindOther) {
		item.Kind = string(media.KindMusic)
	}
	return nil
}

// resolveAudiobook finds the on-disk path of an audiobook's primary media
// file. resolvePodcastEpisode is below — they used to share a query against
// shelf_items, but with the schema split they now hit distinct tables.
func resolveAudiobook(ctx context.Context, db *sql.DB, item *StationItem) error {
	if item.SourceID == "" {
		item.Missing = true
		return nil
	}
	var path sql.NullString
	var duration sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT mf.path, mf.duration_seconds
		FROM audiobooks a
		LEFT JOIN media_files mf ON mf.audiobook_id = a.id
		WHERE a.id = ?
		ORDER BY mf.relative_path, mf.id
		LIMIT 1`, item.SourceID).Scan(&path, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		item.Missing = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve audiobook %q: %w", item.SourceID, err)
	}
	if !path.Valid || strings.TrimSpace(path.String) == "" {
		item.Missing = true
		return nil
	}
	item.ResolvedPath = path.String
	if item.DurationSeconds <= 0 && duration.Valid {
		item.DurationSeconds = int(duration.Int64)
	}
	if item.Kind == "" || item.Kind == string(media.KindOther) {
		item.Kind = string(media.KindAudiobook)
	}
	return nil
}

func resolvePodcastEpisode(ctx context.Context, db *sql.DB, item *StationItem) error {
	if item.SourceID == "" {
		item.Missing = true
		return nil
	}
	var path sql.NullString
	var duration sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT mf.path, mf.duration_seconds
		FROM podcast_episodes pe
		LEFT JOIN media_files mf ON mf.episode_id = pe.id
		WHERE pe.id = ?
		ORDER BY mf.relative_path, mf.id
		LIMIT 1`, item.SourceID).Scan(&path, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		item.Missing = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve podcast episode %q: %w", item.SourceID, err)
	}
	if !path.Valid || strings.TrimSpace(path.String) == "" {
		item.Missing = true
		return nil
	}
	item.ResolvedPath = path.String
	if item.DurationSeconds <= 0 && duration.Valid {
		item.DurationSeconds = int(duration.Int64)
	}
	if item.Kind == "" || item.Kind == string(media.KindOther) {
		item.Kind = string(media.KindPodcast)
	}
	return nil
}

// ImportConfigIfEmpty copies a JSON-loaded Config into the DB on first
// startup so legacy installs keep their stations. Subsequent runs treat the
// DB as source of truth.
func ImportConfigIfEmpty(ctx context.Context, db *sql.DB, cfg Config) error {
	if db == nil {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM radio_stations`).Scan(&count); err != nil {
		return fmt.Errorf("count radio stations: %w", err)
	}
	if count > 0 || len(cfg.Stations) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stationCfg := range cfg.Stations {
		id := normalizeID(stationCfg.ID)
		if id == "" {
			id = normalizeID(stationCfg.Name)
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(stationCfg.Name)
		if name == "" {
			name = id
		}
		contentType := strings.TrimSpace(stationCfg.ContentType)
		if contentType == "" {
			contentType = defaultContentType
		}
		epoch := strings.TrimSpace(stationCfg.Epoch)
		if epoch == "" {
			epoch = defaultEpoch
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO radio_stations (id, name, description, content_type, epoch, enabled, source)
			VALUES (?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(id) DO UPDATE SET
			  name = excluded.name,
			  description = excluded.description,
			  content_type = excluded.content_type,
			  epoch = excluded.epoch,
			  updated_at = CURRENT_TIMESTAMP`,
			id, name, strings.TrimSpace(stationCfg.Description), contentType, epoch, StationSourceFile); err != nil {
			return fmt.Errorf("import station %q: %w", id, err)
		}
		for index, mediaCfg := range stationCfg.Media {
			itemID := normalizeID(mediaCfg.ID)
			if itemID == "" {
				itemID = stationItemID(id, mediaCfg.Path, index)
			}
			kind := string(mediaCfg.Kind)
			if kind == "" {
				kind = string(media.KindOther)
			}
			weight := mediaCfg.Weight
			if weight <= 0 {
				weight = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO radio_station_items
				  (id, station_id, position, source_kind, source_path, title, artist, album, kind, duration_seconds, weight)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				itemID, id, index, ItemSourcePath, strings.TrimSpace(mediaCfg.Path),
				strings.TrimSpace(mediaCfg.Title), strings.TrimSpace(mediaCfg.Artist),
				strings.TrimSpace(mediaCfg.Album), kind, mediaCfg.DurationSeconds, weight); err != nil {
				return fmt.Errorf("import station item %q: %w", itemID, err)
			}
		}
	}
	return tx.Commit()
}

// StationToConfig converts the hydrated DB record back into a StationConfig
// that NewService can consume. Items without a resolved path are skipped so
// the schedule never tries to read a missing file.
func StationToConfig(record StationRecord) StationConfig {
	station := StationConfig{
		ID:          record.ID,
		Name:        record.Name,
		Description: record.Description,
		ContentType: record.ContentType,
		Epoch:       record.Epoch,
	}
	for _, item := range record.Items {
		if item.Missing || item.ResolvedPath == "" {
			continue
		}
		kind := media.Kind(item.Kind)
		if kind == "" {
			kind = media.KindOther
		}
		duration := item.DurationSeconds
		if duration <= 0 {
			continue
		}
		station.Media = append(station.Media, MediaItemConfig{
			ID:              item.ID,
			Title:           item.Title,
			Artist:          item.Artist,
			Album:           item.Album,
			Kind:            kind,
			Path:            item.ResolvedPath,
			DurationSeconds: duration,
			Weight:          item.Weight,
		})
	}
	return station
}

// CreateStationInput is the API write payload for a new station. Items can
// be added in the same call or via AddStationItem.
type CreateStationInput struct {
	ID          string                   `json:"id,omitempty"`
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	ContentType string                   `json:"contentType,omitempty"`
	Epoch       string                   `json:"epoch,omitempty"`
	Enabled     *bool                    `json:"enabled,omitempty"`
	Items       []CreateStationItemInput `json:"items,omitempty"`
}

// UpdateStationInput patches station-level fields. Items are managed through
// dedicated item routes.
type UpdateStationInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	ContentType *string `json:"contentType,omitempty"`
	Epoch       *string `json:"epoch,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}

// CreateStationItemInput describes a new item. Source kind decides which of
// SourceID or SourcePath is required.
type CreateStationItemInput struct {
	SourceKind      string `json:"sourceKind"`
	SourceID        string `json:"sourceId,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	Title           string `json:"title,omitempty"`
	Artist          string `json:"artist,omitempty"`
	Album           string `json:"album,omitempty"`
	Kind            string `json:"kind,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	Weight          int    `json:"weight,omitempty"`
}

// CreateStation persists a new station record. Item rows are inserted in the
// same transaction. Returns the resolved record so the caller can decide
// whether to rebuild the in-memory service.
func CreateStation(ctx context.Context, db *sql.DB, input CreateStationInput) (StationRecord, error) {
	if db == nil {
		return StationRecord{}, errors.New("nil database")
	}
	id := normalizeID(input.ID)
	if id == "" {
		id = normalizeID(input.Name)
	}
	if id == "" {
		return StationRecord{}, errors.New("station id or name is required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = id
	}
	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = defaultContentType
	}
	epoch := strings.TrimSpace(input.Epoch)
	if epoch == "" {
		epoch = defaultEpoch
	}
	if _, err := time.Parse(time.RFC3339, epoch); err != nil {
		return StationRecord{}, fmt.Errorf("epoch must be RFC3339: %w", err)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return StationRecord{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO radio_stations (id, name, description, content_type, epoch, enabled, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, strings.TrimSpace(input.Description), contentType, epoch, boolInt(enabled), StationSourceDatabase); err != nil {
		return StationRecord{}, fmt.Errorf("insert station: %w", err)
	}
	for index, raw := range input.Items {
		if err := insertStationItem(ctx, tx, id, index, raw); err != nil {
			return StationRecord{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return StationRecord{}, err
	}
	return LoadStationByID(ctx, db, id)
}

// UpdateStation patches station-level fields. Item changes are out of scope.
func UpdateStation(ctx context.Context, db *sql.DB, id string, input UpdateStationInput) (StationRecord, error) {
	if db == nil {
		return StationRecord{}, errors.New("nil database")
	}
	id = strings.TrimSpace(id)
	current, err := LoadStationByID(ctx, db, id)
	if err != nil {
		return StationRecord{}, err
	}

	name := current.Name
	if input.Name != nil {
		candidate := strings.TrimSpace(*input.Name)
		if candidate == "" {
			return StationRecord{}, errors.New("name cannot be empty")
		}
		name = candidate
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	contentType := current.ContentType
	if input.ContentType != nil {
		candidate := strings.TrimSpace(*input.ContentType)
		if candidate == "" {
			candidate = defaultContentType
		}
		contentType = candidate
	}
	epoch := current.Epoch
	if input.Epoch != nil {
		candidate := strings.TrimSpace(*input.Epoch)
		if candidate == "" {
			candidate = defaultEpoch
		}
		if _, err := time.Parse(time.RFC3339, candidate); err != nil {
			return StationRecord{}, fmt.Errorf("epoch must be RFC3339: %w", err)
		}
		epoch = candidate
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE radio_stations
		SET name = ?,
		    description = ?,
		    content_type = ?,
		    epoch = ?,
		    enabled = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`,
		name, description, contentType, epoch, boolInt(enabled), id); err != nil {
		return StationRecord{}, fmt.Errorf("update station: %w", err)
	}
	return LoadStationByID(ctx, db, id)
}

// DeleteStation removes a station and (via ON DELETE CASCADE) its items.
func DeleteStation(ctx context.Context, db *sql.DB, id string) error {
	if db == nil {
		return errors.New("nil database")
	}
	id = strings.TrimSpace(id)
	res, err := db.ExecContext(ctx, `DELETE FROM radio_stations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete station: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrStationNotFound
	}
	return nil
}

// AddStationItem appends an item to a station. Position defaults to the end
// of the existing item list.
func AddStationItem(ctx context.Context, db *sql.DB, stationID string, input CreateStationItemInput) (StationItem, error) {
	if db == nil {
		return StationItem{}, errors.New("nil database")
	}
	stationID = strings.TrimSpace(stationID)
	if _, err := LoadStationByID(ctx, db, stationID); err != nil {
		return StationItem{}, err
	}
	var maxPosition sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT MAX(position) FROM radio_station_items WHERE station_id = ?`, stationID).Scan(&maxPosition); err != nil {
		return StationItem{}, fmt.Errorf("query max position: %w", err)
	}
	position := 0
	if maxPosition.Valid {
		position = int(maxPosition.Int64) + 1
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return StationItem{}, err
	}
	defer tx.Rollback()
	if err := insertStationItem(ctx, tx, stationID, position, input); err != nil {
		return StationItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return StationItem{}, err
	}
	itemID := stationItemID(stationID, input.SourcePath, position)
	if normalized := normalizeID(input.SourceID); normalized != "" && input.SourceKind != ItemSourcePath {
		itemID = stationItemID(stationID, input.SourceKind+":"+normalized, position)
	}
	return loadStationItemByID(ctx, db, itemID)
}

// RemoveStationItem deletes a single item row by ID.
func RemoveStationItem(ctx context.Context, db *sql.DB, itemID string) error {
	if db == nil {
		return errors.New("nil database")
	}
	itemID = strings.TrimSpace(itemID)
	res, err := db.ExecContext(ctx, `DELETE FROM radio_station_items WHERE id = ?`, itemID)
	if err != nil {
		return fmt.Errorf("delete radio item: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrItemNotFound
	}
	return nil
}

func insertStationItem(ctx context.Context, tx *sql.Tx, stationID string, position int, input CreateStationItemInput) error {
	kind := strings.TrimSpace(input.SourceKind)
	switch kind {
	case ItemSourcePath:
		if strings.TrimSpace(input.SourcePath) == "" {
			return errors.New("source path is required for path items")
		}
	case ItemSourceMusicTrack, ItemSourceAudiobook, ItemSourcePodcastEpisode:
		if strings.TrimSpace(input.SourceID) == "" {
			return fmt.Errorf("source id required for %s items", kind)
		}
	default:
		return fmt.Errorf("unsupported source kind %q", input.SourceKind)
	}
	weight := input.Weight
	if weight <= 0 {
		weight = 1
	}
	itemKind := strings.TrimSpace(input.Kind)
	if itemKind == "" {
		itemKind = string(media.KindOther)
	}
	itemID := stationItemID(stationID, input.SourcePath, position)
	if normalized := normalizeID(input.SourceID); normalized != "" && kind != ItemSourcePath {
		itemID = stationItemID(stationID, kind+":"+normalized, position)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO radio_station_items
		  (id, station_id, position, source_kind, source_id, source_path, title, artist, album, kind, duration_seconds, weight)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, stationID, position, kind, strings.TrimSpace(input.SourceID),
		strings.TrimSpace(input.SourcePath), strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Artist), strings.TrimSpace(input.Album), itemKind,
		input.DurationSeconds, weight); err != nil {
		return fmt.Errorf("insert station item: %w", err)
	}
	return nil
}

func loadStationItemByID(ctx context.Context, db *sql.DB, itemID string) (StationItem, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, station_id, position, source_kind, source_id, source_path, title, artist, album,
		       kind, duration_seconds, weight
		FROM radio_station_items
		WHERE id = ?`, itemID)
	var item StationItem
	if err := row.Scan(&item.ID, &item.StationID, &item.Position, &item.SourceKind, &item.SourceID,
		&item.SourcePath, &item.Title, &item.Artist, &item.Album, &item.Kind,
		&item.DurationSeconds, &item.Weight); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StationItem{}, ErrItemNotFound
		}
		return StationItem{}, fmt.Errorf("scan station item: %w", err)
	}
	if err := resolveItem(ctx, db, &item); err != nil {
		return StationItem{}, err
	}
	return item, nil
}

func stationItemID(stationID, anchor string, position int) string {
	hash := sha256.New()
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(stationID))))
	hash.Write([]byte{0})
	hash.Write([]byte(strings.ToLower(strings.TrimSpace(anchor))))
	hash.Write([]byte{0})
	hash.Write([]byte(fmt.Sprintf("%d", position)))
	return "ritem_" + hex.EncodeToString(hash.Sum(nil)[:12])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
