// Package bookmarks holds the audiobook-only user-state primitives:
// bookmarks (per-position notes), collections (curated lists), and
// listening sessions (scrobble-style listen events). It was previously
// named shelfuser; the rename mirrors the wider shelf -> audiobook
// terminology shift and the migration that retargeted these tables at
// the new audiobooks(id) primary key.
//
// Podcasts deliberately do NOT use this package. Podcast follow-state
// lives next to RSS ingestion (internal/sources) because the natural
// unit for podcasts is the show subscription, not a bookmark.
package bookmarks

import "database/sql"

type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service {
	return &Service{db: db}
}
