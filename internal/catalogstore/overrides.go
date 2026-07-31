package catalogstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func LoadMetadataOverrides(ctx context.Context, db *sql.DB) (map[catalog.MetadataOverrideKey]catalog.MetadataOverridePatch, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT target_kind, target_id, fields_json
		FROM metadata_overrides`)
	if err != nil {
		return nil, fmt.Errorf("load metadata overrides: %w", err)
	}
	defer rows.Close()

	out := map[catalog.MetadataOverrideKey]catalog.MetadataOverridePatch{}
	for rows.Next() {
		var kind, id, fieldsJSON string
		if err := rows.Scan(&kind, &id, &fieldsJSON); err != nil {
			return nil, fmt.Errorf("scan metadata override: %w", err)
		}
		patch := catalog.MetadataOverridePatch{}
		if strings.TrimSpace(fieldsJSON) != "" && fieldsJSON != "{}" {
			if err := json.Unmarshal([]byte(fieldsJSON), &patch); err != nil {
				return nil, fmt.Errorf("decode metadata override %s/%s: %w", kind, id, err)
			}
		}
		out[catalog.MetadataOverrideKey{TargetKind: kind, TargetID: id}] = patch
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

func UpsertMetadataOverride(ctx context.Context, db *sql.DB, kind, targetID string, patch catalog.MetadataOverridePatch) error {
	if len(patch) == 0 {
		return nil
	}
	key := catalog.MetadataOverrideKey{TargetKind: kind, TargetID: targetID}
	existing, err := LoadMetadataOverrides(ctx, db)
	if err != nil {
		return err
	}
	merged := existing[key]
	if merged == nil {
		merged = catalog.MetadataOverridePatch{}
	}
	for field, value := range patch {
		if field == "externalIds" {
			merged[field] = catalog.MergeOverrideExternalIDs(merged[field], value)
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

func GetMetadataOverride(ctx context.Context, db *sql.DB, kind, targetID string) (catalog.MetadataOverrideRecord, error) {
	var fieldsJSON, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT fields_json, updated_at
		FROM metadata_overrides
		WHERE target_kind = ? AND target_id = ?`, kind, targetID).
		Scan(&fieldsJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return catalog.MetadataOverrideRecord{}, catalog.ErrMetadataOverrideNotFound
	}
	if err != nil {
		return catalog.MetadataOverrideRecord{}, fmt.Errorf("load metadata override: %w", err)
	}
	patch := catalog.MetadataOverridePatch{}
	if strings.TrimSpace(fieldsJSON) != "" && fieldsJSON != "{}" {
		if err := json.Unmarshal([]byte(fieldsJSON), &patch); err != nil {
			return catalog.MetadataOverrideRecord{}, fmt.Errorf("decode metadata override: %w", err)
		}
	}
	return catalog.MetadataOverrideRecord{
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
		return catalog.ErrMetadataOverrideNotFound
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
		`DELETE FROM metadata_overrides WHERE target_kind = 'audiobook' AND target_id NOT IN (SELECT id FROM audiobooks)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'podcast' AND target_id NOT IN (SELECT id FROM podcasts)`,
		`DELETE FROM metadata_overrides WHERE target_kind = 'podcast-episode' AND target_id NOT IN (SELECT id FROM podcast_episodes)`,
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
