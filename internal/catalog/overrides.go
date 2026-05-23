package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MetadataOverrideKey identifies a user-applied metadata override target.
type MetadataOverrideKey struct {
	TargetKind string
	TargetID   string
}

// MetadataOverridePatch stores field-level override values keyed by apply field name.
type MetadataOverridePatch map[string]json.RawMessage

func LoadMetadataOverrides(ctx context.Context, db *sql.DB) (map[MetadataOverrideKey]MetadataOverridePatch, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT target_kind, target_id, fields_json
		FROM metadata_overrides`)
	if err != nil {
		return nil, fmt.Errorf("load metadata overrides: %w", err)
	}
	defer rows.Close()

	out := map[MetadataOverrideKey]MetadataOverridePatch{}
	for rows.Next() {
		var kind, id, fieldsJSON string
		if err := rows.Scan(&kind, &id, &fieldsJSON); err != nil {
			return nil, fmt.Errorf("scan metadata override: %w", err)
		}
		patch := MetadataOverridePatch{}
		if strings.TrimSpace(fieldsJSON) != "" && fieldsJSON != "{}" {
			if err := json.Unmarshal([]byte(fieldsJSON), &patch); err != nil {
				return nil, fmt.Errorf("decode metadata override %s/%s: %w", kind, id, err)
			}
		}
		out[MetadataOverrideKey{TargetKind: kind, TargetID: id}] = patch
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func LoadPodcastFeedPodcastIDs(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, podcast_id FROM podcast_feeds`)
	if err != nil {
		return nil, fmt.Errorf("load podcast feed ids: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var feedID, podcastID string
		if err := rows.Scan(&feedID, &podcastID); err != nil {
			return nil, err
		}
		out[feedID] = podcastID
	}
	return out, rows.Err()
}

func UpsertMetadataOverride(ctx context.Context, db *sql.DB, kind, targetID string, patch MetadataOverridePatch) error {
	if len(patch) == 0 {
		return nil
	}
	key := MetadataOverrideKey{TargetKind: kind, TargetID: targetID}
	existing, err := LoadMetadataOverrides(ctx, db)
	if err != nil {
		return err
	}
	merged := existing[key]
	if merged == nil {
		merged = MetadataOverridePatch{}
	}
	for field, value := range patch {
		if field == "externalIds" {
			merged[field] = mergeOverrideExternalIDs(merged[field], value)
			continue
		}
		merged[field] = append(json.RawMessage(nil), value...)
	}
	fieldsJSON, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("encode metadata override: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO metadata_overrides (target_kind, target_id, fields_json, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(target_kind, target_id) DO UPDATE SET
		  fields_json = excluded.fields_json,
		  updated_at = excluded.updated_at`,
		kind, targetID, string(fieldsJSON), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("upsert metadata override: %w", err)
	}
	return nil
}

type MetadataOverrideRecord struct {
	TargetKind string                `json:"targetKind"`
	TargetID   string                `json:"targetId"`
	Fields     MetadataOverridePatch `json:"fields"`
	UpdatedAt  string                `json:"updatedAt,omitempty"`
}

func GetMetadataOverride(ctx context.Context, db *sql.DB, kind, targetID string) (MetadataOverrideRecord, error) {
	var fieldsJSON, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT fields_json, updated_at
		FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, kind, targetID).
		Scan(&fieldsJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return MetadataOverrideRecord{}, ErrMetadataOverrideNotFound
	}
	if err != nil {
		return MetadataOverrideRecord{}, fmt.Errorf("load metadata override: %w", err)
	}
	patch := MetadataOverridePatch{}
	if strings.TrimSpace(fieldsJSON) != "" && fieldsJSON != "{}" {
		if err := json.Unmarshal([]byte(fieldsJSON), &patch); err != nil {
			return MetadataOverrideRecord{}, fmt.Errorf("decode metadata override: %w", err)
		}
	}
	return MetadataOverrideRecord{
		TargetKind: kind,
		TargetID:   targetID,
		Fields:     patch,
		UpdatedAt:  updatedAt,
	}, nil
}

func DeleteMetadataOverride(ctx context.Context, db *sql.DB, kind, targetID string) error {
	result, err := db.ExecContext(ctx, `
		DELETE FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, kind, targetID)
	if err != nil {
		return fmt.Errorf("delete metadata override: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrMetadataOverrideNotFound
	}
	return nil
}

func ClearMetadataOverrideFields(ctx context.Context, db *sql.DB, kind, targetID string, fields []string) error {
	record, err := GetMetadataOverride(ctx, db, kind, targetID)
	if err != nil {
		return err
	}
	for _, field := range fields {
		delete(record.Fields, strings.TrimSpace(field))
	}
	if len(record.Fields) == 0 {
		return DeleteMetadataOverride(ctx, db, kind, targetID)
	}
	fieldsJSON, err := json.Marshal(record.Fields)
	if err != nil {
		return fmt.Errorf("encode metadata override: %w", err)
	}
	_, err = db.ExecContext(ctx, `
		UPDATE metadata_overrides
		SET fields_json = ?, updated_at = ?
		WHERE target_kind = ? AND target_id = ?`,
		string(fieldsJSON), time.Now().UTC().Format(time.RFC3339), kind, targetID)
	if err != nil {
		return fmt.Errorf("update metadata override: %w", err)
	}
	return nil
}

func DeleteMetadataOverridesForTarget(ctx context.Context, db *sql.DB, kind, targetID string) error {
	_, err := db.ExecContext(ctx, `
		DELETE FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, kind, targetID)
	return err
}

func PruneStaleMetadataOverrides(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`DELETE FROM metadata_overrides WHERE target_kind = 'music-track' AND target_id NOT IN (SELECT id FROM music_tracks)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'music-album' AND target_id NOT IN (SELECT id FROM music_albums)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'music-artist' AND target_id NOT IN (SELECT id FROM music_artists)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'shelf-item' AND target_id NOT IN (SELECT id FROM shelf_items)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'shelf-episode' AND target_id NOT IN (SELECT id FROM podcast_episodes)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'music-playlist' AND target_id NOT IN (SELECT id FROM music_playlists)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'podcast-feed' AND target_id NOT IN (SELECT id FROM podcast_feeds)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("prune stale metadata overrides: %w", err)
		}
	}
	return nil
}

var ErrMetadataOverrideNotFound = errors.New("metadata override not found")

func mergeOverrideExternalIDs(current, incoming json.RawMessage) json.RawMessage {
	var left, right ExternalIDs
	decodeOverrideJSON(current, &left)
	decodeOverrideJSON(incoming, &right)
	merged := mergeExternalIDsOverride(left, right)
	data, err := json.Marshal(merged)
	if err != nil {
		return incoming
	}
	return data
}

func mergeExternalIDsOverride(current, incoming ExternalIDs) ExternalIDs {
	merged := current
	if incoming.MusicBrainzArtistID != "" {
		merged.MusicBrainzArtistID = incoming.MusicBrainzArtistID
	}
	if incoming.MusicBrainzReleaseGroupID != "" {
		merged.MusicBrainzReleaseGroupID = incoming.MusicBrainzReleaseGroupID
	}
	if incoming.MusicBrainzReleaseID != "" {
		merged.MusicBrainzReleaseID = incoming.MusicBrainzReleaseID
	}
	if incoming.MusicBrainzRecordingID != "" {
		merged.MusicBrainzRecordingID = incoming.MusicBrainzRecordingID
	}
	if incoming.MusicBrainzTrackID != "" {
		merged.MusicBrainzTrackID = incoming.MusicBrainzTrackID
	}
	if incoming.MusicBrainzWorkID != "" {
		merged.MusicBrainzWorkID = incoming.MusicBrainzWorkID
	}
	if incoming.DiscogsID != "" {
		merged.DiscogsID = incoming.DiscogsID
	}
	if incoming.SpotifyID != "" {
		merged.SpotifyID = incoming.SpotifyID
	}
	if incoming.AppleMusicID != "" {
		merged.AppleMusicID = incoming.AppleMusicID
	}
	if incoming.ISRC != "" {
		merged.ISRC = incoming.ISRC
	}
	if incoming.ISBN10 != "" {
		merged.ISBN10 = incoming.ISBN10
	}
	if incoming.ISBN13 != "" {
		merged.ISBN13 = incoming.ISBN13
	}
	if incoming.ASIN != "" {
		merged.ASIN = incoming.ASIN
	}
	if incoming.AudibleASIN != "" {
		merged.AudibleASIN = incoming.AudibleASIN
	}
	if incoming.GoogleBooksID != "" {
		merged.GoogleBooksID = incoming.GoogleBooksID
	}
	if incoming.OpenLibraryID != "" {
		merged.OpenLibraryID = incoming.OpenLibraryID
	}
	if incoming.ITunesID != "" {
		merged.ITunesID = incoming.ITunesID
	}
	if incoming.FeedGUID != "" {
		merged.FeedGUID = incoming.FeedGUID
	}
	merged.URLs = mergeStringSlicesOverride(current.URLs, incoming.URLs)
	return merged
}

func mergeStringSlicesOverride(current, incoming []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(current)+len(incoming))
	for _, value := range append(current, incoming...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func decodeOverrideJSON(value json.RawMessage, out any) {
	if len(value) == 0 {
		return
	}
	_ = json.Unmarshal(value, out)
}

func decodePatchString(patch MetadataOverridePatch, field string) (string, bool) {
	raw, ok := patch[field]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func decodePatchBool(patch MetadataOverridePatch, field string) (bool, bool) {
	raw, ok := patch[field]
	if !ok {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}

func decodePatchInt(patch MetadataOverridePatch, field string) (int, bool) {
	raw, ok := patch[field]
	if !ok {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

func decodePatchStringSlice(patch MetadataOverridePatch, field string) ([]string, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func decodePatchContributors(patch MetadataOverridePatch, field string) ([]Contributor, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []Contributor
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func decodePatchSeries(patch MetadataOverridePatch, field string) ([]SeriesRef, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []SeriesRef
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func decodePatchExternalIDs(patch MetadataOverridePatch, field string) (ExternalIDs, bool) {
	raw, ok := patch[field]
	if !ok {
		return ExternalIDs{}, false
	}
	var value ExternalIDs
	if err := json.Unmarshal(raw, &value); err != nil {
		return ExternalIDs{}, false
	}
	return value, true
}

func decodePatchImage(patch MetadataOverridePatch, field string) (*Image, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value Image
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return &value, true
}

func decodePatchImages(patch MetadataOverridePatch, field string) ([]Image, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value []Image
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func decodePatchTime(patch MetadataOverridePatch, field string) (*time.Time, bool) {
	raw, ok := patch[field]
	if !ok {
		return nil, false
	}
	var value time.Time
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	value = value.UTC()
	return &value, true
}
