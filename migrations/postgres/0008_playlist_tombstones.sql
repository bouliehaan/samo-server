-- Deliberate playlist deletions the filesystem auto-import must not undo.
-- Scan imports derive playlist identity from sha(owner, name), and the owner
-- has changed across eras (bootstrap account -> first human admin), so the
-- normalized NAME is the only stable identity an on-disk .m3u carries.
-- A row here means "an owner or admin deleted this playlist on purpose":
-- the scanner skips re-importing a matching .m3u, while a manual import via
-- the API (an explicit user action) clears the row and proceeds.
CREATE TABLE IF NOT EXISTS playlist_tombstones (
    name_key   TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
