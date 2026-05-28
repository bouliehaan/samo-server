package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SetPodcastCover stores a user-uploaded cover for a podcast show. The image
// is written to metadata_overrides (so rescans do not clobber it) and to
// podcasts.cover_json (so scanner guard preserves the same artwork).
func SetPodcastCover(ctx context.Context, db *sql.DB, podcastID string, cover Image) error {
	podcastID = strings.TrimSpace(podcastID)
	if podcastID == "" {
		return ErrNotFound
	}
	if db == nil {
		return fmt.Errorf("nil database")
	}
	if strings.TrimSpace(cover.ID) == "" && strings.TrimSpace(cover.Path) == "" && strings.TrimSpace(cover.URL) == "" {
		return fmt.Errorf("cover image is required")
	}

	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM podcasts WHERE id = ?`, podcastID).Scan(&exists); err != nil {
		return fmt.Errorf("lookup podcast: %w", err)
	}
	if exists == 0 {
		return ErrNotFound
	}

	coverRaw, err := json.Marshal(cover)
	if err != nil {
		return fmt.Errorf("encode cover: %w", err)
	}
	if err := UpsertMetadataOverride(ctx, db, OverrideKindPodcast, podcastID, MetadataOverridePatch{
		"cover": coverRaw,
	}); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		UPDATE podcasts
		SET cover_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, string(coverRaw), podcastID)
	if err != nil {
		return fmt.Errorf("update podcast cover: %w", err)
	}
	return nil
}
