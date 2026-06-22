-- App-settable global podcast enclosure cache size limit. Single row (id=1);
-- an absent/zero row means "use the env default (SAMO_PODCAST_CACHE_MAX_BYTES)".
CREATE TABLE IF NOT EXISTS podcast_cache_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  max_bytes INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
