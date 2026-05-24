package bookmarks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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

// jsonText is unused by bookmarks today but kept to match the helpers
// surface used by tests that import the package.
var _ = jsonText

func jsonText(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// assertAudiobookExists is the bookmarks-package equivalent of the old
// assertAudiobookItem(media_type=book) check. Since the `audiobooks`
// table is by definition audiobook-only, the existence check is the
// only assertion we need.
func assertAudiobookExists(ctx context.Context, db *sql.DB, audiobookID string) error {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM audiobooks WHERE id = ?`, audiobookID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrNotAudiobook
	}
	if err != nil {
		return fmt.Errorf("verify audiobook: %w", err)
	}
	return nil
}
