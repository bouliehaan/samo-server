-- Per-show (and global) podcast prewarm preferences. show_id = '' is the global
-- default applied to shows without an explicit override. prewarm_count is how
-- many of the newest episodes to keep warm in the on-disk enclosure cache.
CREATE TABLE IF NOT EXISTS podcast_prefs (
  show_id TEXT PRIMARY KEY,
  prewarm_count INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
