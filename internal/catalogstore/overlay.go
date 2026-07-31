package catalogstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// OverlayAudiobookOverride applies any stored metadata override for one audiobook
// onto the given item, returning the SAME enriched view the catalog/API serves
// (clean title, ASIN, authors). It reads the single metadata_overrides row, so it
// works anywhere with a DB handle — the chapter analysis pass and the inspector
// CLI both need it, because they read raw book_json and would otherwise identify
// a book by its folder name ("Eragon - Inheritance Book 01") instead of "Eragon"
// + its ASIN, and fail every Audible lookup. No override → item unchanged.
func OverlayAudiobookOverride(ctx context.Context, db *sql.DB, item catalog.AudiobookItem) (catalog.AudiobookItem, error) {
	var fieldsJSON string
	err := db.QueryRowContext(ctx,
		`SELECT COALESCE(fields_json,'{}') FROM metadata_overrides WHERE target_kind = 'audiobook' AND target_id = ?`,
		item.ID).Scan(&fieldsJSON)
	if err == sql.ErrNoRows {
		return item, nil
	}
	if err != nil {
		return item, err
	}
	patch := catalog.MetadataOverridePatch{}
	if s := strings.TrimSpace(fieldsJSON); s != "" && s != "{}" {
		if err := json.Unmarshal([]byte(fieldsJSON), &patch); err != nil {
			return item, err
		}
	}
	if len(patch) == 0 {
		return item, nil
	}
	return catalog.OverlayAudiobook(item, patch), nil
}
