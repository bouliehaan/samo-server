package artistmeta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

var errCacheMiss = errors.New("artist external meta cache miss")

type cacheRow struct {
	Biography string
	Similar   []catalog.SimilarArtistRef
	FetchedAt time.Time
	Empty     bool // a negative-cache row: fetched, nothing found
}

func loadCacheRow(ctx context.Context, db *sql.DB, artistID string) (cacheRow, error) {
	var biography, similarJSON, fetchedAt string
	err := db.QueryRowContext(ctx, `
		SELECT biography, similar_json, fetched_at
		FROM music_artist_external_meta
		WHERE artist_id = ?`, artistID).Scan(&biography, &similarJSON, &fetchedAt)
	if err == sql.ErrNoRows {
		return cacheRow{}, errCacheMiss
	}
	if err != nil {
		return cacheRow{}, err
	}

	row := cacheRow{Biography: strings.TrimSpace(biography), FetchedAt: parseStoredTime(fetchedAt)}
	if trimmed := strings.TrimSpace(similarJSON); trimmed != "" && trimmed != "null" {
		_ = json.Unmarshal([]byte(trimmed), &row.Similar)
	}
	row.Empty = row.Biography == "" && len(row.Similar) == 0
	return row, nil
}

func saveCacheRow(ctx context.Context, db *sql.DB, artistID, biography string, similar []catalog.SimilarArtistRef, source string) error {
	if strings.TrimSpace(source) == "" {
		source = "external"
	}
	similarJSON := ""
	if len(similar) > 0 {
		encoded, err := json.Marshal(similar)
		if err != nil {
			return err
		}
		similarJSON = string(encoded)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO music_artist_external_meta (artist_id, biography, similar_json, source, fetched_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(artist_id) DO UPDATE SET
		  biography = excluded.biography,
		  similar_json = excluded.similar_json,
		  source = excluded.source,
		  fetched_at = CURRENT_TIMESTAMP`,
		artistID, strings.TrimSpace(biography), similarJSON, source,
	)
	return err
}

func parseStoredTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		parsed, err = time.Parse("2006-01-02 15:04:05", raw)
	}
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
