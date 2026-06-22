CREATE TABLE IF NOT EXISTS music_artist_external_meta (
  artist_id TEXT PRIMARY KEY,
  biography TEXT NOT NULL DEFAULT '',
  similar_json TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (artist_id) REFERENCES music_artists(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_music_artist_external_meta_fetched
  ON music_artist_external_meta(fetched_at);
