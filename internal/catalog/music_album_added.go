package catalog

import "time"

// timePtr returns a pointer to a copy of value. Shared by the audiobook/podcast
// AddedAt enrichers in library_added_at.go.
//
// Music album AddedAt is intentionally NOT derived from file mtime: it is
// persisted write-once in music_albums.added_at (set at first scan, preserved on
// re-scan) and loaded by loadMusicAlbums. Recomputing it live from the newest
// track's filesystem mtime made "Recently Added" mean "recently-touched-on-disk",
// so a copy/restore/sync that re-stamped old files pushed them to the top.
func timePtr(value time.Time) *time.Time {
	v := value
	return &v
}
