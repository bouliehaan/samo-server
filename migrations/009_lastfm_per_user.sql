CREATE TABLE IF NOT EXISTS lastfm_user_settings (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  lastfm_username TEXT NOT NULL,
  session_key TEXT NOT NULL,
  connected_at TEXT NOT NULL
);

INSERT OR IGNORE INTO lastfm_user_settings (user_id, lastfm_username, session_key, connected_at)
SELECT 'user-server', username, session_key, connected_at
FROM lastfm_settings
WHERE id = 1;

DROP TABLE IF EXISTS lastfm_settings;

CREATE TABLE IF NOT EXISTS lastfm_track_sessions_v2 (
  user_id TEXT NOT NULL,
  track_id TEXT NOT NULL,
  play_token TEXT NOT NULL,
  now_playing_sent INTEGER NOT NULL DEFAULT 0,
  scrobbled INTEGER NOT NULL DEFAULT 0,
  play_started_at INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, track_id)
);

INSERT OR IGNORE INTO lastfm_track_sessions_v2 (
  user_id, track_id, play_token, now_playing_sent, scrobbled, play_started_at, updated_at
)
SELECT 'user-server', track_id, play_token, now_playing_sent, scrobbled, play_started_at, updated_at
FROM lastfm_track_sessions;

DROP TABLE IF EXISTS lastfm_track_sessions;

ALTER TABLE lastfm_track_sessions_v2 RENAME TO lastfm_track_sessions;

ALTER TABLE lastfm_scrobble_queue ADD COLUMN user_id TEXT NOT NULL DEFAULT 'user-server';
ALTER TABLE lastfm_submissions ADD COLUMN user_id TEXT NOT NULL DEFAULT 'user-server';

CREATE INDEX IF NOT EXISTS idx_lastfm_queue_user ON lastfm_scrobble_queue(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_lastfm_submissions_user ON lastfm_submissions(user_id, created_at DESC);
