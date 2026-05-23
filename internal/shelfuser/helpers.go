package shelfuser

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

func stableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte(strings.ToLower(strings.TrimSpace(part))))
		hash.Write([]byte{0})
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil)[:12])
}

func parseTimePtr(raw sql.NullString) *time.Time {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw.String))
	if err != nil {
		return nil
	}
	return &parsed
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func assertAudiobookItem(ctx context.Context, db *sql.DB, itemID string) error {
	var mediaType string
	err := db.QueryRowContext(ctx, `SELECT media_type FROM shelf_items WHERE id = ?`, itemID).Scan(&mediaType)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load shelf item: %w", err)
	}
	if catalog.ShelfMediaType(mediaType) != catalog.ShelfMediaTypeBook {
		return ErrNotAudiobook
	}
	return nil
}
