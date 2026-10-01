-- Songs requested through Search for new. Explo holds its download jobs only
-- in memory; this is samo's record of each request from the download until the
-- song is in the library, so a request survives a restart of either program
-- and finishes whether or not anyone is watching the page.
CREATE TABLE IF NOT EXISTS explo_requests (
    recording_id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    artist TEXT NOT NULL DEFAULT '',
    album TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    requested_by TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'downloading',
    staged_file TEXT NOT NULL DEFAULT '',
    track_id TEXT NOT NULL DEFAULT '',
    library_track_id TEXT NOT NULL DEFAULT '',
    library_path TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
    updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE INDEX IF NOT EXISTS idx_explo_requests_state ON explo_requests (state);
