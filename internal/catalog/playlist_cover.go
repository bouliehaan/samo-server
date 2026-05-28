package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SetMusicPlaylistCover stores a user-uploaded cover for a playlist. The
// image is written to metadata_overrides and images_json so clients and
// rescans preserve the artwork.
func SetMusicPlaylistCover(ctx context.Context, db *sql.DB, playlistID string, cover Image) error {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return ErrNotFound
	}
	if db == nil {
		return fmt.Errorf("nil database")
	}
	if strings.TrimSpace(cover.ID) == "" && strings.TrimSpace(cover.Path) == "" && strings.TrimSpace(cover.URL) == "" {
		return fmt.Errorf("cover image is required")
	}

	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_playlists WHERE id = ?`, playlistID).Scan(&exists); err != nil {
		return fmt.Errorf("lookup playlist: %w", err)
	}
	if exists == 0 {
		return ErrNotFound
	}

	coverRaw, err := json.Marshal([]Image{cover})
	if err != nil {
		return fmt.Errorf("encode cover: %w", err)
	}
	if err := UpsertMetadataOverride(ctx, db, OverrideKindMusicPlaylist, playlistID, MetadataOverridePatch{
		"images": coverRaw,
	}); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `
		UPDATE music_playlists
		SET images_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, string(coverRaw), playlistID)
	if err != nil {
		return fmt.Errorf("update playlist cover: %w", err)
	}
	return nil
}
