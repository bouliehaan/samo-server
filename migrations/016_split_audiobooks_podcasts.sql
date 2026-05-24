-- migration 016: split shelf concept into independent audiobook + podcast domains
--
-- Before this migration the schema imitated audiobookshelf: a single
-- `shelf_items` table held both audiobooks and podcasts with a `media_type`
-- discriminator. That was inherited shape, not Samo's. Samo treats music,
-- audiobooks, podcasts, and radio as four independent first-class domains.
--
-- This migration:
--   1. Creates `audiobooks` and `podcasts` top-level tables.
--   2. Renames the audiobook-people table `shelf_authors` to `contributors`
--      and its join to `audiobook_contributors`.
--   3. Renames `shelf_series` to `series` and its join to `audiobook_series`.
--   4. Splits `shelf_chapters` into `audiobook_chapters` + `episode_chapters`.
--   5. Renames the user-facing audiobook tables (bookmarks / collections /
--      listening_sessions) to drop the `shelf_` prefix.
--   6. Re-points `media_files`, `podcast_episodes`, `podcast_feeds`, and
--      `podcast_episode_cache` foreign keys at the new tables.
--   7. Rewrites the `libraries.kind` enum: `shelf`+`book` becomes
--      `audiobook`; `shelf`+`podcast` becomes `podcast`. `media_type`
--      becomes redundant but kept (NULL) for forward-compat.
--   8. Re-keys `metadata_overrides`, `user_playback`, and
--      `radio_station_items` target/source kinds so existing rows keep
--      pointing at the right thing under the new vocabulary.
--   9. Drops all `shelf_*` tables.
--
-- The whole migration runs in a single transaction (storage.go wraps each
-- file in BEGIN/COMMIT). Foreign-key checks are deferred to the commit so
-- intermediate states where rows reference tables that haven't been
-- recreated yet are safe.

PRAGMA defer_foreign_keys = ON;

-- ---------------------------------------------------------------------------
-- 1. Audiobook entity tables
-- ---------------------------------------------------------------------------

CREATE TABLE audiobooks (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
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
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_scan_at TEXT
);

CREATE TABLE podcasts (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
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
  podcast_json TEXT,
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_scan_at TEXT
);

CREATE TABLE contributors (
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

CREATE TABLE audiobook_contributors (
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  contributor_id TEXT NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'author',
  position INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (audiobook_id, contributor_id, role, position)
);

CREATE TABLE series (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  authors_json TEXT NOT NULL DEFAULT '[]',
  item_ids_json TEXT NOT NULL DEFAULT '[]',
  item_count INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  external_ids_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE audiobook_series (
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  sequence REAL NOT NULL DEFAULT 0,
  sequence_text TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (audiobook_id, series_id)
);

CREATE TABLE audiobook_chapters (
  id TEXT PRIMARY KEY,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  chapter_index INTEGER NOT NULL,
  title TEXT NOT NULL,
  start_seconds INTEGER NOT NULL,
  end_seconds INTEGER NOT NULL DEFAULT 0
);

-- episode_chapters references podcast_episodes which we recreate below;
-- defer_foreign_keys keeps the constraint valid until commit.
CREATE TABLE episode_chapters (
  id TEXT PRIMARY KEY,
  episode_id TEXT NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  chapter_index INTEGER NOT NULL,
  title TEXT NOT NULL,
  start_seconds INTEGER NOT NULL,
  end_seconds INTEGER NOT NULL DEFAULT 0
);

-- ---------------------------------------------------------------------------
-- 2. User-facing audiobook tables (bookmarks / collections / sessions)
-- ---------------------------------------------------------------------------

CREATE TABLE bookmarks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  position_seconds INTEGER NOT NULL DEFAULT 0,
  chapter_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE collections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  public INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE collection_audiobooks (
  collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  position INTEGER NOT NULL DEFAULT 0,
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (collection_id, audiobook_id)
);

CREATE TABLE listening_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL,
  start_position_seconds INTEGER NOT NULL DEFAULT 0,
  end_position_seconds INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  completed INTEGER NOT NULL DEFAULT 0
);

-- ---------------------------------------------------------------------------
-- 3. Copy data from the old shelf_* tables.
--    audiobooks <- shelf_items WHERE media_type='book'
--    podcasts   <- shelf_items WHERE media_type='podcast'
-- ---------------------------------------------------------------------------

INSERT INTO audiobooks (
  id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
  cover_json, tags_json, genres_json, duration_seconds, progress_json,
  book_json, added_at, updated_at, last_scan_at
)
SELECT id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
  cover_json, tags_json, genres_json, duration_seconds, progress_json,
  book_json, added_at, updated_at, last_scan_at
FROM shelf_items
WHERE media_type = 'book';

INSERT INTO podcasts (
  id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
  cover_json, tags_json, genres_json, duration_seconds, progress_json,
  podcast_json, added_at, updated_at, last_scan_at
)
SELECT id, library_id, path, folder_id, inode, size_bytes, missing, invalid,
  cover_json, tags_json, genres_json, duration_seconds, progress_json,
  podcast_json, added_at, updated_at, last_scan_at
FROM shelf_items
WHERE media_type = 'podcast';

INSERT INTO contributors (
  id, name, sort_name, description, images_json, external_ids_json,
  item_count, series_count, duration_seconds
)
SELECT id, name, sort_name, description, images_json, external_ids_json,
  item_count, series_count, duration_seconds
FROM shelf_authors;

INSERT INTO series (
  id, name, description, authors_json, item_ids_json, item_count,
  duration_seconds, external_ids_json
)
SELECT id, name, description, authors_json, item_ids_json, item_count,
  duration_seconds, external_ids_json
FROM shelf_series;

-- Only audiobook contributor/series rows survive; podcast items in the old
-- world never had author/series joins, but filter defensively anyway.
INSERT INTO audiobook_contributors (audiobook_id, contributor_id, role, position)
SELECT sia.item_id, sia.author_id, sia.role, sia.position
FROM shelf_item_authors sia
WHERE sia.item_id IN (SELECT id FROM audiobooks);

INSERT INTO audiobook_series (audiobook_id, series_id, sequence, sequence_text)
SELECT sis.item_id, sis.series_id, sis.sequence, sis.sequence_text
FROM shelf_item_series sis
WHERE sis.item_id IN (SELECT id FROM audiobooks);

INSERT INTO audiobook_chapters (id, audiobook_id, chapter_index, title, start_seconds, end_seconds)
SELECT id, item_id, chapter_index, title, start_seconds, end_seconds
FROM shelf_chapters
WHERE episode_id IS NULL
  AND item_id IN (SELECT id FROM audiobooks);

INSERT INTO episode_chapters (id, episode_id, chapter_index, title, start_seconds, end_seconds)
SELECT id, episode_id, chapter_index, title, start_seconds, end_seconds
FROM shelf_chapters
WHERE episode_id IS NOT NULL;

INSERT INTO bookmarks (
  id, user_id, audiobook_id, title, note, position_seconds, chapter_id,
  created_at, updated_at
)
SELECT id, user_id, item_id, title, note, position_seconds, chapter_id,
  created_at, updated_at
FROM shelf_bookmarks
WHERE item_id IN (SELECT id FROM audiobooks);

INSERT INTO collections (id, user_id, name, description, public, created_at, updated_at)
SELECT id, user_id, name, description, public, created_at, updated_at
FROM shelf_collections;

INSERT INTO collection_audiobooks (collection_id, audiobook_id, position, added_at)
SELECT collection_id, item_id, position, added_at
FROM shelf_collection_items
WHERE item_id IN (SELECT id FROM audiobooks);

INSERT INTO listening_sessions (
  id, user_id, audiobook_id, started_at, ended_at,
  start_position_seconds, end_position_seconds, duration_seconds, completed
)
SELECT id, user_id, item_id, started_at, ended_at,
  start_position_seconds, end_position_seconds, duration_seconds, completed
FROM shelf_listening_sessions
WHERE item_id IN (SELECT id FROM audiobooks);

-- ---------------------------------------------------------------------------
-- 4. Rebuild media_files with audiobook_id + podcast_id columns and drop
--    the old item_id (which FK'd to shelf_items).
-- ---------------------------------------------------------------------------

CREATE TABLE media_files_new (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  audiobook_id TEXT REFERENCES audiobooks(id) ON DELETE CASCADE,
  podcast_id TEXT REFERENCES podcasts(id) ON DELETE CASCADE,
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

INSERT INTO media_files_new (
  id, library_id, audiobook_id, podcast_id, track_id, episode_id, path,
  relative_path, file_name, inode, size_bytes, modified_at, container,
  mime_type, codec, codec_profile, metadata_formats_json, bitrate, bit_depth,
  sample_rate, channels, channel_layout, duration_seconds, checksum,
  embedded_tags_json, created_at, updated_at
)
SELECT
  id,
  library_id,
  CASE WHEN item_id IN (SELECT id FROM audiobooks) THEN item_id END,
  CASE WHEN item_id IN (SELECT id FROM podcasts) THEN item_id END,
  track_id,
  episode_id,
  path, relative_path, file_name, inode, size_bytes, modified_at, container,
  mime_type, codec, codec_profile, metadata_formats_json, bitrate, bit_depth,
  sample_rate, channels, channel_layout, duration_seconds, checksum,
  embedded_tags_json, created_at, updated_at
FROM media_files;

DROP TABLE media_files;
ALTER TABLE media_files_new RENAME TO media_files;

-- ---------------------------------------------------------------------------
-- 5. Rebuild podcast_episodes with FK -> podcasts (was -> shelf_items).
-- ---------------------------------------------------------------------------

CREATE TABLE podcast_episodes_new (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  podcast_id TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
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

INSERT INTO podcast_episodes_new
SELECT id, library_id, podcast_id, title, subtitle, description, published_at,
  season, episode, episode_type, duration_seconds, explicit, enclosure_url,
  enclosure_type, enclosure_bytes, progress_json, external_ids_json,
  added_at, updated_at
FROM podcast_episodes;

DROP TABLE podcast_episodes;
ALTER TABLE podcast_episodes_new RENAME TO podcast_episodes;

-- ---------------------------------------------------------------------------
-- 6. Rebuild podcast_feeds with FK -> podcasts (was -> shelf_items).
-- ---------------------------------------------------------------------------

CREATE TABLE podcast_feeds_new (
  id TEXT PRIMARY KEY,
  podcast_id TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  site_url TEXT NOT NULL DEFAULT '',
  image_url TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '',
  explicit INTEGER NOT NULL DEFAULT 0,
  categories_json TEXT NOT NULL DEFAULT '[]',
  owner_name TEXT NOT NULL DEFAULT '',
  owner_email TEXT NOT NULL DEFAULT '',
  episode_count INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'ok',
  last_error TEXT NOT NULL DEFAULT '',
  last_fetched_at TEXT,
  poll_enabled INTEGER NOT NULL DEFAULT 1,
  poll_interval_seconds INTEGER NOT NULL DEFAULT 3600,
  next_poll_at TEXT,
  last_poll_started_at TEXT,
  last_poll_finished_at TEXT,
  consecutive_errors INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO podcast_feeds_new
SELECT id, podcast_id, feed_url, title, description, author, site_url,
  image_url, language, explicit, categories_json, owner_name, owner_email,
  episode_count, status, last_error, last_fetched_at,
  poll_enabled, poll_interval_seconds, next_poll_at,
  last_poll_started_at, last_poll_finished_at, consecutive_errors,
  created_at, updated_at
FROM podcast_feeds;

DROP TABLE podcast_feeds;
ALTER TABLE podcast_feeds_new RENAME TO podcast_feeds;

CREATE INDEX IF NOT EXISTS idx_podcast_feeds_next_poll ON podcast_feeds(poll_enabled, next_poll_at);

-- ---------------------------------------------------------------------------
-- 7. Re-key cross-domain dispatch values that used to spell "shelf-*".
-- ---------------------------------------------------------------------------

-- metadata_overrides: shelf-item rows split by which table the target landed in
UPDATE metadata_overrides
SET target_kind = 'audiobook'
WHERE target_kind = 'shelf-item' AND target_id IN (SELECT id FROM audiobooks);

UPDATE metadata_overrides
SET target_kind = 'podcast'
WHERE target_kind = 'shelf-item' AND target_id IN (SELECT id FROM podcasts);

UPDATE metadata_overrides
SET target_kind = 'podcast-episode'
WHERE target_kind = 'shelf-episode';

-- user_playback: same split
UPDATE user_playback
SET target_kind = 'audiobook'
WHERE target_kind = 'shelf-item' AND target_id IN (SELECT id FROM audiobooks);

UPDATE user_playback
SET target_kind = 'podcast'
WHERE target_kind = 'shelf-item' AND target_id IN (SELECT id FROM podcasts);

UPDATE user_playback
SET target_kind = 'podcast-episode'
WHERE target_kind = 'shelf-episode';

-- radio station programming
UPDATE radio_station_items
SET source_kind = 'audiobook'
WHERE source_kind = 'shelf-item' AND source_id IN (SELECT id FROM audiobooks);

UPDATE radio_station_items
SET source_kind = 'podcast'
WHERE source_kind = 'shelf-item' AND source_id IN (SELECT id FROM podcasts);

UPDATE radio_station_items
SET source_kind = 'podcast-episode'
WHERE source_kind = 'shelf-episode';

-- ---------------------------------------------------------------------------
-- 8. Library kind enum: shelf + media_type collapse into audiobook/podcast.
-- ---------------------------------------------------------------------------

UPDATE libraries
SET kind = 'audiobook', media_type = NULL
WHERE kind = 'shelf' AND media_type = 'book';

UPDATE libraries
SET kind = 'podcast', media_type = NULL
WHERE kind = 'shelf' AND media_type = 'podcast';

-- Anything still marked 'shelf' (no media_type) defaults to audiobook so
-- the row stays valid under the new enum. Real installs won't hit this
-- branch — shelf libraries always set media_type — but we don't want a
-- migration crash to leave a hung database either.
UPDATE libraries
SET kind = 'audiobook', media_type = NULL
WHERE kind = 'shelf';

-- ---------------------------------------------------------------------------
-- 9. Drop the old shelf_* tables (FK dependents have been recreated).
-- ---------------------------------------------------------------------------

DROP TABLE shelf_listening_sessions;
DROP TABLE shelf_collection_items;
DROP TABLE shelf_collections;
DROP TABLE shelf_bookmarks;
DROP TABLE shelf_chapters;
DROP TABLE shelf_item_series;
DROP TABLE shelf_item_authors;
DROP TABLE shelf_series;
DROP TABLE shelf_authors;
DROP TABLE shelf_items;

-- ---------------------------------------------------------------------------
-- 10. Indexes.
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_audiobooks_library ON audiobooks(library_id);
CREATE INDEX IF NOT EXISTS idx_podcasts_library ON podcasts(library_id);
CREATE INDEX IF NOT EXISTS idx_audiobook_contributors_contributor
  ON audiobook_contributors(contributor_id);
CREATE INDEX IF NOT EXISTS idx_audiobook_series_series
  ON audiobook_series(series_id);
CREATE INDEX IF NOT EXISTS idx_audiobook_chapters_audiobook
  ON audiobook_chapters(audiobook_id, chapter_index);
CREATE INDEX IF NOT EXISTS idx_episode_chapters_episode
  ON episode_chapters(episode_id, chapter_index);

CREATE INDEX IF NOT EXISTS idx_media_files_library ON media_files(library_id);
CREATE INDEX IF NOT EXISTS idx_media_files_track ON media_files(track_id);
CREATE INDEX IF NOT EXISTS idx_media_files_audiobook ON media_files(audiobook_id);
CREATE INDEX IF NOT EXISTS idx_media_files_podcast ON media_files(podcast_id);
CREATE INDEX IF NOT EXISTS idx_media_files_episode ON media_files(episode_id);

CREATE INDEX IF NOT EXISTS idx_podcast_episodes_podcast
  ON podcast_episodes(podcast_id);
CREATE INDEX IF NOT EXISTS idx_podcast_feeds_podcast
  ON podcast_feeds(podcast_id);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user_audiobook
  ON bookmarks(user_id, audiobook_id);
CREATE INDEX IF NOT EXISTS idx_collections_user ON collections(user_id);
CREATE INDEX IF NOT EXISTS idx_collection_audiobooks_collection
  ON collection_audiobooks(collection_id, position);
CREATE INDEX IF NOT EXISTS idx_listening_sessions_user_started
  ON listening_sessions(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_listening_sessions_audiobook
  ON listening_sessions(audiobook_id);
