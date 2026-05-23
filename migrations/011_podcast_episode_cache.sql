CREATE TABLE IF NOT EXISTS podcast_episode_cache (
  episode_id TEXT PRIMARY KEY REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  enclosure_url TEXT NOT NULL,
  cache_path TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  downloaded_at TEXT NOT NULL,
  last_accessed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_podcast_episode_cache_accessed
  ON podcast_episode_cache(last_accessed_at);
