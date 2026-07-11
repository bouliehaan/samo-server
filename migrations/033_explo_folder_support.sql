-- Explo integration support: a weekly algorithmic playlist exporter drops
-- untagged files into a configured folder. The explo service fingerprints
-- and identifies them in the background, routes them into a system-managed
-- playlist, and keeps them out of the "Recently Added" shelves so they don't
-- flood in as "Unknown Artist". Files themselves are never touched.

ALTER TABLE music_albums ADD COLUMN hidden_from_recently_added INTEGER NOT NULL DEFAULT 0;
ALTER TABLE music_playlists ADD COLUMN system INTEGER NOT NULL DEFAULT 0;

-- One row per music_tracks.id that has been through the explo pipeline, so a
-- track is only ever fingerprinted/looked-up once regardless of match result.
CREATE TABLE IF NOT EXISTS explo_tracks (
  track_id TEXT PRIMARY KEY REFERENCES music_tracks(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending',
  acoustid_id TEXT NOT NULL DEFAULT '',
  musicbrainz_recording_id TEXT NOT NULL DEFAULT '',
  matched_title TEXT NOT NULL DEFAULT '',
  matched_artist TEXT NOT NULL DEFAULT '',
  score REAL NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  processed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
