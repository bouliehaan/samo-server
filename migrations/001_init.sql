CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS libraries (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  media_type TEXT,
  path TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  item_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_scan_at TEXT
);

CREATE TABLE IF NOT EXISTS media_files (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  item_id TEXT REFERENCES shelf_items(id) ON DELETE CASCADE,
  track_id TEXT REFERENCES music_tracks(id) ON DELETE CASCADE,
  episode_id TEXT REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  relative_path TEXT NOT NULL DEFAULT '',
  file_name TEXT NOT NULL DEFAULT '',
  inode TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  modified_at TEXT,
  container TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  codec TEXT NOT NULL DEFAULT '',
  codec_profile TEXT NOT NULL DEFAULT '',
  metadata_formats_json TEXT NOT NULL DEFAULT '[]',
  bitrate INTEGER NOT NULL DEFAULT 0,
  bit_depth INTEGER NOT NULL DEFAULT 0,
  sample_rate INTEGER NOT NULL DEFAULT 0,
  channels INTEGER NOT NULL DEFAULT 0,
  channel_layout TEXT NOT NULL DEFAULT '',
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  checksum TEXT NOT NULL DEFAULT '',
  embedded_tags_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS music_artists (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  sort_name TEXT NOT NULL DEFAULT '',
  disambiguation TEXT NOT NULL DEFAULT '',
  biography TEXT NOT NULL DEFAULT '',
  country TEXT NOT NULL DEFAULT '',
  genres_json TEXT NOT NULL DEFAULT '[]',
  styles_json TEXT NOT NULL DEFAULT '[]',
  moods_json TEXT NOT NULL DEFAULT '[]',
  links_json TEXT NOT NULL DEFAULT '[]',
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  album_count INTEGER NOT NULL DEFAULT 0,
  track_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS music_albums (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  display_artist TEXT NOT NULL DEFAULT '',
  release_date TEXT NOT NULL DEFAULT '',
  original_release_date TEXT NOT NULL DEFAULT '',
  release_year INTEGER NOT NULL DEFAULT 0,
  release_type TEXT NOT NULL DEFAULT '',
  release_status TEXT NOT NULL DEFAULT '',
  compilation INTEGER NOT NULL DEFAULT 0,
  record_label TEXT NOT NULL DEFAULT '',
  catalog_number TEXT NOT NULL DEFAULT '',
  barcode TEXT NOT NULL DEFAULT '',
  genres_json TEXT NOT NULL DEFAULT '[]',
  styles_json TEXT NOT NULL DEFAULT '[]',
  moods_json TEXT NOT NULL DEFAULT '[]',
  tags_json TEXT NOT NULL DEFAULT '[]',
  disc_count INTEGER NOT NULL DEFAULT 0,
  track_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS music_album_artists (
  album_id TEXT NOT NULL REFERENCES music_albums(id) ON DELETE CASCADE,
  artist_id TEXT NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (album_id, artist_id, position)
);

CREATE TABLE IF NOT EXISTS music_tracks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  subtitle TEXT NOT NULL DEFAULT '',
  display_artist TEXT NOT NULL DEFAULT '',
  album_id TEXT REFERENCES music_albums(id) ON DELETE SET NULL,
  album_title TEXT NOT NULL DEFAULT '',
  disc_number INTEGER NOT NULL DEFAULT 0,
  track_number INTEGER NOT NULL DEFAULT 0,
  total_discs INTEGER NOT NULL DEFAULT 0,
  total_tracks INTEGER NOT NULL DEFAULT 0,
  release_date TEXT NOT NULL DEFAULT '',
  release_year INTEGER NOT NULL DEFAULT 0,
  genres_json TEXT NOT NULL DEFAULT '[]',
  moods_json TEXT NOT NULL DEFAULT '[]',
  tags_json TEXT NOT NULL DEFAULT '[]',
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  explicit INTEGER NOT NULL DEFAULT 0,
  bpm INTEGER NOT NULL DEFAULT 0,
  musical_key TEXT NOT NULL DEFAULT '',
  comment TEXT NOT NULL DEFAULT '',
  lyrics_json TEXT NOT NULL DEFAULT '[]',
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS music_track_artists (
  track_id TEXT NOT NULL REFERENCES music_tracks(id) ON DELETE CASCADE,
  artist_id TEXT NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'artist',
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (track_id, artist_id, role, position)
);

CREATE TABLE IF NOT EXISTS music_playlists (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL DEFAULT '',
  public INTEGER NOT NULL DEFAULT 0,
  collaborative INTEGER NOT NULL DEFAULT 0,
  track_ids_json TEXT NOT NULL DEFAULT '[]',
  track_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  images_json TEXT NOT NULL DEFAULT '[]',
  playback_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS genres (
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT '',
  item_count INTEGER NOT NULL DEFAULT 0,
  track_count INTEGER NOT NULL DEFAULT 0,
  album_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (name, kind)
);

CREATE TABLE IF NOT EXISTS shelf_items (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  media_type TEXT NOT NULL,
  media_kind TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL UNIQUE,
  folder_id TEXT NOT NULL DEFAULT '',
  inode TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  missing INTEGER NOT NULL DEFAULT 0,
  invalid INTEGER NOT NULL DEFAULT 0,
  cover_json TEXT NOT NULL DEFAULT '{}',
  tags_json TEXT NOT NULL DEFAULT '[]',
  genres_json TEXT NOT NULL DEFAULT '[]',
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  progress_json TEXT NOT NULL DEFAULT '{}',
  book_json TEXT,
  podcast_json TEXT,
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_scan_at TEXT
);

CREATE TABLE IF NOT EXISTS shelf_authors (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  sort_name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  item_count INTEGER NOT NULL DEFAULT 0,
  series_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS shelf_item_authors (
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  author_id TEXT NOT NULL REFERENCES shelf_authors(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'author',
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, author_id, role, position)
);

CREATE TABLE IF NOT EXISTS shelf_series (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  authors_json TEXT NOT NULL DEFAULT '[]',
  item_ids_json TEXT NOT NULL DEFAULT '[]',
  item_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  external_ids_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS shelf_item_series (
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  series_id TEXT NOT NULL REFERENCES shelf_series(id) ON DELETE CASCADE,
  sequence REAL NOT NULL DEFAULT 0,
  sequence_text TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (item_id, series_id)
);

CREATE TABLE IF NOT EXISTS shelf_chapters (
  id TEXT PRIMARY KEY,
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  episode_id TEXT REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  chapter_index INTEGER NOT NULL,
  title TEXT NOT NULL,
  start_seconds INTEGER NOT NULL,
  end_seconds INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS podcast_episodes (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  podcast_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  subtitle TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  published_at TEXT,
  season INTEGER NOT NULL DEFAULT 0,
  episode INTEGER NOT NULL DEFAULT 0,
  episode_type TEXT NOT NULL DEFAULT '',
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  explicit INTEGER NOT NULL DEFAULT 0,
  enclosure_url TEXT NOT NULL DEFAULT '',
  enclosure_type TEXT NOT NULL DEFAULT '',
  enclosure_bytes INTEGER NOT NULL DEFAULT 0,
  progress_json TEXT NOT NULL DEFAULT '{}',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_media_files_library ON media_files(library_id);
CREATE INDEX IF NOT EXISTS idx_media_files_item ON media_files(item_id);
CREATE INDEX IF NOT EXISTS idx_media_files_track ON media_files(track_id);
CREATE INDEX IF NOT EXISTS idx_music_tracks_album ON music_tracks(album_id);
CREATE INDEX IF NOT EXISTS idx_shelf_items_library ON shelf_items(library_id);
CREATE INDEX IF NOT EXISTS idx_podcast_episodes_podcast ON podcast_episodes(podcast_id);
