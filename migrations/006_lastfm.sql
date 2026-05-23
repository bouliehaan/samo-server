CREATE TABLE IF NOT EXISTS lastfm_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  username TEXT NOT NULL,
  session_key TEXT NOT NULL,
  connected_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS lastfm_track_sessions (
  track_id TEXT PRIMARY KEY,
  play_token TEXT NOT NULL,
  now_playing_sent INTEGER NOT NULL DEFAULT 0,
  scrobbled INTEGER NOT NULL DEFAULT 0,
  play_started_at INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS lastfm_scrobble_queue (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  artist TEXT NOT NULL,
  track TEXT NOT NULL,
  album TEXT,
  duration_seconds INTEGER,
  timestamp INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT
);

CREATE INDEX IF NOT EXISTS idx_lastfm_scrobble_queue_created ON lastfm_scrobble_queue(created_at);
