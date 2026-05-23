ALTER TABLE lastfm_scrobble_queue ADD COLUMN track_id TEXT;

CREATE TABLE IF NOT EXISTS lastfm_submissions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  track_id TEXT,
  artist TEXT NOT NULL,
  track TEXT NOT NULL,
  album TEXT,
  duration_seconds INTEGER,
  played_seconds INTEGER,
  timestamp INTEGER NOT NULL,
  status TEXT NOT NULL,
  error TEXT,
  source TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_lastfm_submissions_created ON lastfm_submissions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_lastfm_scrobble_queue_track ON lastfm_scrobble_queue(track_id, kind, timestamp);
