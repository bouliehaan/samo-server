package artistimages

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var errCacheMiss = errors.New("artist external image cache miss")

type cacheRow struct {
	CoverID   string
	FetchedAt time.Time
}

func loadCacheRow(ctx context.Context, db *sql.DB, artistID string) (cacheRow, error) {
	var coverID string
	var fetchedAt string
	err := db.QueryRowContext(ctx, `
		SELECT cover_id, fetched_at
		FROM music_artist_external_images
		WHERE artist_id = ?`, artistID).Scan(&coverID, &fetchedAt)
	if err == sql.ErrNoRows {
		return cacheRow{}, errCacheMiss
	}
	if err != nil {
		return cacheRow{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, fetchedAt)
	if err != nil {
		parsed, err = time.Parse("2006-01-02 15:04:05", fetchedAt)
	}
	if err != nil {
		parsed = time.Time{}
	}
	return cacheRow{CoverID: coverID, FetchedAt: parsed.UTC()}, nil
}

func saveCacheRow(ctx context.Context, db *sql.DB, artistID, coverID, source string) error {
	if strings.TrimSpace(source) == "" {
		source = "external"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO music_artist_external_images (artist_id, cover_id, source, fetched_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(artist_id) DO UPDATE SET
		  cover_id = excluded.cover_id,
		  source = excluded.source,
		  fetched_at = CURRENT_TIMESTAMP`,
		artistID, coverID, source,
	)
	return err
}
