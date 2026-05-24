CREATE TABLE IF NOT EXISTS lastfm_app_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0,
  api_key TEXT NOT NULL DEFAULT '',
  shared_secret TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
