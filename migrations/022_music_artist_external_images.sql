CREATE TABLE IF NOT EXISTS music_artist_external_images (
  artist_id TEXT PRIMARY KEY,
  cover_id TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'lastfm',
  fetched_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (artist_id) REFERENCES music_artists(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_music_artist_external_images_fetched
  ON music_artist_external_images(fetched_at);
