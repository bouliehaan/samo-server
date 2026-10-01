-- YouTube Music playlists imported with Explo: the playlist is created under
-- its YouTube Music name with the songs the library already has, and every
-- other song is requested through Explo (one explo_requests row each, the
-- same as Search for new). As each request lands in the library, its track
-- joins the playlist where the YouTube Music playlist has it.
--
-- One import per samo playlist: importing the same playlist again reads it
-- afresh and asks only for what is still missing.
CREATE TABLE IF NOT EXISTS explo_playlist_imports (
    playlist_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    requested_by TEXT NOT NULL DEFAULT '',
    unavailable BIGINT NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
    updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

-- The playlist's tracks in YouTube Music's order. state is samo's side of
-- each: 'library' once its library track is in the playlist, 'queued' while
-- it waits to be handed to Explo (Explo was busy or away), 'requested' once
-- an explo_requests row (recording_id) follows the download, 'unavailable'
-- when Explo could not take it at all (message says why).
CREATE TABLE IF NOT EXISTS explo_playlist_import_tracks (
    playlist_id TEXT NOT NULL,
    position BIGINT NOT NULL,
    recording_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    artist TEXT NOT NULL DEFAULT '',
    album TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'queued',
    library_track_id TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (playlist_id, position)
);

CREATE INDEX IF NOT EXISTS idx_explo_playlist_import_tracks_open
    ON explo_playlist_import_tracks (state) WHERE state <> 'library';
