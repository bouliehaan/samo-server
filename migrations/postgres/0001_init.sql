-- FROZEN. Existing databases have already applied this file; it must never
-- change again (the migration ledger records it as done and will not re-run
-- it). Schema changes go in a NEW numbered migration file next to this one.
--
-- Historical note: this consolidated schema was originally generated from the
-- cumulative SQLite migrations when samo-server moved to Postgres. The SQLite
-- lineage (and the generator) has since been deleted; this lineage is now the
-- only one, and it is hand-written and append-only from 0002 onward.

CREATE COLLATION IF NOT EXISTS nocase (provider = icu, locale = 'und-u-ks-level2', deterministic = false);

CREATE OR REPLACE FUNCTION json_extract(j text, path text) RETURNS text
LANGUAGE sql IMMUTABLE AS $fn$
  SELECT (j::jsonb #>> string_to_array(regexp_replace(path, '^\$\.', ''), '.'))
$fn$;

CREATE OR REPLACE FUNCTION datetime(ts text, modifier text) RETURNS text
LANGUAGE sql STABLE AS $fn$
  SELECT to_char(
    ((CASE WHEN lower(ts) = 'now' THEN (now() AT TIME ZONE 'UTC')
           ELSE replace(replace(ts, 'T', ' '), 'Z', '')::timestamp END)
     + replace(modifier, '+', '')::interval),
    'YYYY-MM-DD"T"HH24:MI:SS"Z"')
$fn$;

CREATE OR REPLACE FUNCTION json_each(j text) RETURNS TABLE(value text)
LANGUAGE sql IMMUTABLE AS $fn$
  SELECT jsonb_array_elements_text((CASE WHEN j = '' THEN '[]' ELSE j END)::jsonb);
$fn$;

CREATE TABLE libraries (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  media_type TEXT,
  path TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  item_count BIGINT NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  last_scan_at TEXT
);

CREATE TABLE audiobooks (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  folder_id TEXT NOT NULL DEFAULT '',
  inode TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  missing BIGINT NOT NULL DEFAULT 0,
  invalid BIGINT NOT NULL DEFAULT 0,
  cover_json TEXT NOT NULL DEFAULT '{}',
  tags_json TEXT NOT NULL DEFAULT '[]',
  genres_json TEXT NOT NULL DEFAULT '[]',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  progress_json TEXT NOT NULL DEFAULT '{}',
  book_json TEXT,
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  last_scan_at TEXT
, chapter_source TEXT NOT NULL DEFAULT '', chapter_asin TEXT NOT NULL DEFAULT '', chapter_synced_at TEXT, chapter_confidence DOUBLE PRECISION NOT NULL DEFAULT 0, chapter_audio_sig TEXT NOT NULL DEFAULT '');

CREATE TABLE audiobook_chapters (
  id TEXT PRIMARY KEY,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  chapter_index BIGINT NOT NULL,
  title TEXT NOT NULL,
  start_seconds BIGINT NOT NULL,
  end_seconds BIGINT NOT NULL DEFAULT 0
, start_ms BIGINT NOT NULL DEFAULT 0, end_ms BIGINT NOT NULL DEFAULT 0);

CREATE TABLE contributors (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  sort_name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  item_count BIGINT NOT NULL DEFAULT 0,
  series_count BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE audiobook_contributors (
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  contributor_id TEXT NOT NULL REFERENCES contributors(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'author',
  position BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (audiobook_id, contributor_id, role, position)
);

CREATE TABLE series (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  authors_json TEXT NOT NULL DEFAULT '[]',
  item_ids_json TEXT NOT NULL DEFAULT '[]',
  item_count BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  external_ids_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE audiobook_series (
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  sequence DOUBLE PRECISION NOT NULL DEFAULT 0,
  sequence_text TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (audiobook_id, series_id)
);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  username TEXT COLLATE nocase NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'user',
  password_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE bookmarks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  position_seconds BIGINT NOT NULL DEFAULT 0,
  chapter_id TEXT,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  -- Encoder output. Everything the scheduler picks gets transcoded to
  -- this format so podcast + commercial + live-stream can mux into a
  -- single continuous output.
  codec TEXT NOT NULL DEFAULT 'mp3',
  bitrate_kbps BIGINT NOT NULL DEFAULT 192,
  sample_rate_hz BIGINT NOT NULL DEFAULT 44100,
  enabled BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE channel_play_log (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL DEFAULT '',
  item_ref TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  artist TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL DEFAULT '',
  duration_seconds BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE channel_sources (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  config_json TEXT NOT NULL DEFAULT '{}',
  enabled BIGINT NOT NULL DEFAULT 1,
  weight BIGINT NOT NULL DEFAULT 1,
  default_rotation BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE channel_schedule_rules (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL REFERENCES channel_sources(id) ON DELETE CASCADE,
  label TEXT NOT NULL DEFAULT '',
  weekday_mask BIGINT NOT NULL DEFAULT 127,
  start_minute BIGINT NOT NULL,
  end_minute BIGINT NOT NULL,
  priority BIGINT NOT NULL DEFAULT 100,
  enabled BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE collections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  public BIGINT NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE collection_audiobooks (
  collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  position BIGINT NOT NULL DEFAULT 0,
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  PRIMARY KEY (collection_id, audiobook_id)
);

CREATE TABLE podcasts (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE,
  folder_id TEXT NOT NULL DEFAULT '',
  inode TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  missing BIGINT NOT NULL DEFAULT 0,
  invalid BIGINT NOT NULL DEFAULT 0,
  cover_json TEXT NOT NULL DEFAULT '{}',
  tags_json TEXT NOT NULL DEFAULT '[]',
  genres_json TEXT NOT NULL DEFAULT '[]',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  progress_json TEXT NOT NULL DEFAULT '{}',
  podcast_json TEXT,
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  last_scan_at TEXT
);

CREATE TABLE "podcast_episodes" (
  id TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  podcast_id TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  subtitle TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  published_at TEXT,
  season BIGINT NOT NULL DEFAULT 0,
  episode BIGINT NOT NULL DEFAULT 0,
  episode_type TEXT NOT NULL DEFAULT '',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  explicit BIGINT NOT NULL DEFAULT 0,
  enclosure_url TEXT NOT NULL DEFAULT '',
  enclosure_type TEXT NOT NULL DEFAULT '',
  enclosure_bytes BIGINT NOT NULL DEFAULT 0,
  progress_json TEXT NOT NULL DEFAULT '{}',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE episode_chapters (
  id TEXT PRIMARY KEY,
  episode_id TEXT NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  chapter_index BIGINT NOT NULL,
  title TEXT NOT NULL,
  start_seconds BIGINT NOT NULL,
  end_seconds BIGINT NOT NULL DEFAULT 0
, start_ms BIGINT NOT NULL DEFAULT 0, end_ms BIGINT NOT NULL DEFAULT 0);

CREATE TABLE explo_config (
  id BIGINT PRIMARY KEY CHECK (id = 1),
  enabled BIGINT NOT NULL DEFAULT 0,
  folder TEXT NOT NULL DEFAULT '',
  acoustid_api_key TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE music_albums (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  display_artist TEXT NOT NULL DEFAULT '',
  release_date TEXT NOT NULL DEFAULT '',
  original_release_date TEXT NOT NULL DEFAULT '',
  release_year BIGINT NOT NULL DEFAULT 0,
  release_type TEXT NOT NULL DEFAULT '',
  release_status TEXT NOT NULL DEFAULT '',
  compilation BIGINT NOT NULL DEFAULT 0,
  record_label TEXT NOT NULL DEFAULT '',
  catalog_number TEXT NOT NULL DEFAULT '',
  barcode TEXT NOT NULL DEFAULT '',
  genres_json TEXT NOT NULL DEFAULT '[]',
  styles_json TEXT NOT NULL DEFAULT '[]',
  moods_json TEXT NOT NULL DEFAULT '[]',
  tags_json TEXT NOT NULL DEFAULT '[]',
  disc_count BIGINT NOT NULL DEFAULT 0,
  track_count BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, hidden_from_recently_added BIGINT NOT NULL DEFAULT 0);

CREATE TABLE music_tracks (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  sort_title TEXT NOT NULL DEFAULT '',
  subtitle TEXT NOT NULL DEFAULT '',
  display_artist TEXT NOT NULL DEFAULT '',
  album_id TEXT REFERENCES music_albums(id) ON DELETE SET NULL,
  album_title TEXT NOT NULL DEFAULT '',
  disc_number BIGINT NOT NULL DEFAULT 0,
  track_number BIGINT NOT NULL DEFAULT 0,
  total_discs BIGINT NOT NULL DEFAULT 0,
  total_tracks BIGINT NOT NULL DEFAULT 0,
  release_date TEXT NOT NULL DEFAULT '',
  release_year BIGINT NOT NULL DEFAULT 0,
  genres_json TEXT NOT NULL DEFAULT '[]',
  moods_json TEXT NOT NULL DEFAULT '[]',
  tags_json TEXT NOT NULL DEFAULT '[]',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  explicit BIGINT NOT NULL DEFAULT 0,
  bpm BIGINT NOT NULL DEFAULT 0,
  musical_key TEXT NOT NULL DEFAULT '',
  comment TEXT NOT NULL DEFAULT '',
  lyrics_json TEXT NOT NULL DEFAULT '[]',
  images_json TEXT NOT NULL DEFAULT '[]',
  external_ids_json TEXT NOT NULL DEFAULT '{}',
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE explo_tracks (
  track_id TEXT PRIMARY KEY REFERENCES music_tracks(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending',
  acoustid_id TEXT NOT NULL DEFAULT '',
  musicbrainz_recording_id TEXT NOT NULL DEFAULT '',
  matched_title TEXT NOT NULL DEFAULT '',
  matched_artist TEXT NOT NULL DEFAULT '',
  score DOUBLE PRECISION NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  processed_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, cover_status TEXT NOT NULL DEFAULT '', attempts BIGINT NOT NULL DEFAULT 1);

CREATE TABLE extracted_covers (
  id TEXT PRIMARY KEY,
  source_path TEXT NOT NULL UNIQUE,
  source_checksum TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT 'image/jpeg',
  width BIGINT NOT NULL DEFAULT 0,
  height BIGINT NOT NULL DEFAULT 0,
  extracted_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE genres (
  name TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT '',
  item_count BIGINT NOT NULL DEFAULT 0,
  track_count BIGINT NOT NULL DEFAULT 0,
  album_count BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (name, kind)
);

CREATE TABLE internet_radio_stations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  stream_url TEXT NOT NULL UNIQUE,
  homepage_url TEXT NOT NULL DEFAULT '',
  image_url TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT '',
  codec TEXT NOT NULL DEFAULT '',
  bitrate BIGINT NOT NULL DEFAULT 0,
  country TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '',
  tags_json TEXT NOT NULL DEFAULT '[]',
  enabled BIGINT NOT NULL DEFAULT 1,
  last_checked_at TEXT,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, now_playing TEXT NOT NULL DEFAULT '', now_playing_artist TEXT NOT NULL DEFAULT '', now_playing_title TEXT NOT NULL DEFAULT '', now_playing_updated_at TEXT, probe_enabled BIGINT NOT NULL DEFAULT 1, probe_interval_seconds BIGINT NOT NULL DEFAULT 600, next_probe_at TEXT, last_probe_started_at TEXT, last_probe_finished_at TEXT, last_probe_error TEXT NOT NULL DEFAULT '', consecutive_probe_errors BIGINT NOT NULL DEFAULT 0, probe_status TEXT NOT NULL DEFAULT '', cover_id TEXT NOT NULL DEFAULT '');

CREATE TABLE lastfm_app_config (
  id BIGINT PRIMARY KEY CHECK (id = 1),
  enabled BIGINT NOT NULL DEFAULT 0,
  api_key TEXT NOT NULL DEFAULT '',
  shared_secret TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);

CREATE TABLE lastfm_scrobble_queue (
  id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  kind TEXT NOT NULL,
  artist TEXT NOT NULL,
  track TEXT NOT NULL,
  album TEXT,
  duration_seconds BIGINT,
  timestamp BIGINT NOT NULL,
  created_at TEXT NOT NULL,
  attempts BIGINT NOT NULL DEFAULT 0,
  last_error TEXT
, track_id TEXT, user_id TEXT NOT NULL DEFAULT 'user-server');

CREATE TABLE lastfm_submissions (
  id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  kind TEXT NOT NULL,
  track_id TEXT,
  artist TEXT NOT NULL,
  track TEXT NOT NULL,
  album TEXT,
  duration_seconds BIGINT,
  played_seconds BIGINT,
  timestamp BIGINT NOT NULL,
  status TEXT NOT NULL,
  error TEXT,
  source TEXT,
  created_at TEXT NOT NULL
, user_id TEXT NOT NULL DEFAULT 'user-server');

CREATE TABLE "lastfm_track_sessions" (
  user_id TEXT NOT NULL,
  track_id TEXT NOT NULL,
  play_token TEXT NOT NULL,
  now_playing_sent BIGINT NOT NULL DEFAULT 0,
  scrobbled BIGINT NOT NULL DEFAULT 0,
  play_started_at BIGINT NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, track_id)
);

CREATE TABLE lastfm_user_settings (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  lastfm_username TEXT NOT NULL,
  session_key TEXT NOT NULL,
  connected_at TEXT NOT NULL
);

CREATE TABLE listening_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audiobook_id TEXT NOT NULL REFERENCES audiobooks(id) ON DELETE CASCADE,
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL,
  start_position_seconds BIGINT NOT NULL DEFAULT 0,
  end_position_seconds BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  completed BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE "media_files" (
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
  size_bytes BIGINT NOT NULL DEFAULT 0,
  modified_at TEXT,
  container TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  codec TEXT NOT NULL DEFAULT '',
  codec_profile TEXT NOT NULL DEFAULT '',
  metadata_formats_json TEXT NOT NULL DEFAULT '[]',
  bitrate BIGINT NOT NULL DEFAULT 0,
  bit_depth BIGINT NOT NULL DEFAULT 0,
  sample_rate BIGINT NOT NULL DEFAULT 0,
  channels BIGINT NOT NULL DEFAULT 0,
  channel_layout TEXT NOT NULL DEFAULT '',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  checksum TEXT NOT NULL DEFAULT '',
  embedded_tags_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, missing BIGINT NOT NULL DEFAULT 0, missing_detected_at TEXT, track_pid TEXT NOT NULL DEFAULT '', content_hash TEXT NOT NULL DEFAULT '', duration_ms BIGINT NOT NULL DEFAULT 0);

CREATE TABLE metadata_overrides (
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  fields_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  PRIMARY KEY (target_kind, target_id)
);

CREATE TABLE music_artists (
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
  album_count BIGINT NOT NULL DEFAULT 0,
  track_count BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  playback_json TEXT NOT NULL DEFAULT '{}',
  added_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE music_album_artists (
  album_id TEXT NOT NULL REFERENCES music_albums(id) ON DELETE CASCADE,
  artist_id TEXT NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
  position BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (album_id, artist_id, position)
);

CREATE TABLE music_artist_external_images (
  artist_id TEXT PRIMARY KEY,
  cover_id TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT 'lastfm',
  fetched_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  FOREIGN KEY (artist_id) REFERENCES music_artists(id) ON DELETE CASCADE
);

CREATE TABLE music_artist_external_meta (
  artist_id TEXT PRIMARY KEY,
  biography TEXT NOT NULL DEFAULT '',
  similar_json TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  FOREIGN KEY (artist_id) REFERENCES music_artists(id) ON DELETE CASCADE
);

CREATE TABLE music_playlists (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL DEFAULT '',
  public BIGINT NOT NULL DEFAULT 0,
  collaborative BIGINT NOT NULL DEFAULT 0,
  track_ids_json TEXT NOT NULL DEFAULT '[]',
  track_count BIGINT NOT NULL DEFAULT 0,
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  images_json TEXT NOT NULL DEFAULT '[]',
  playback_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, system BIGINT NOT NULL DEFAULT 0);

CREATE TABLE music_track_artists (
  track_id TEXT NOT NULL REFERENCES music_tracks(id) ON DELETE CASCADE,
  artist_id TEXT NOT NULL REFERENCES music_artists(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'artist',
  position BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (track_id, artist_id, role, position)
);

CREATE TABLE podcast_cache_settings (
  id BIGINT PRIMARY KEY CHECK (id = 1),
  max_bytes BIGINT NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE podcast_episode_cache (
  episode_id TEXT PRIMARY KEY REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  enclosure_url TEXT NOT NULL,
  cache_path TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT '',
  size_bytes BIGINT NOT NULL DEFAULT 0,
  downloaded_at TEXT NOT NULL,
  last_accessed_at TEXT NOT NULL
);

CREATE TABLE "podcast_feeds" (
  id TEXT PRIMARY KEY,
  podcast_id TEXT NOT NULL REFERENCES podcasts(id) ON DELETE CASCADE,
  feed_url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  site_url TEXT NOT NULL DEFAULT '',
  image_url TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '',
  explicit BIGINT NOT NULL DEFAULT 0,
  categories_json TEXT NOT NULL DEFAULT '[]',
  owner_name TEXT NOT NULL DEFAULT '',
  owner_email TEXT NOT NULL DEFAULT '',
  episode_count BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'ok',
  last_error TEXT NOT NULL DEFAULT '',
  last_fetched_at TEXT,
  poll_enabled BIGINT NOT NULL DEFAULT 1,
  poll_interval_seconds BIGINT NOT NULL DEFAULT 3600,
  next_poll_at TEXT,
  last_poll_started_at TEXT,
  last_poll_finished_at TEXT,
  consecutive_errors BIGINT NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, auto_download_enabled BIGINT NOT NULL DEFAULT 0);

CREATE TABLE podcast_prefs (
  show_id TEXT PRIMARY KEY,
  prewarm_count BIGINT NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE radio_stations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  content_type TEXT NOT NULL DEFAULT 'audio/mpeg',
  epoch TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z',
  enabled BIGINT NOT NULL DEFAULT 1,
  source TEXT NOT NULL DEFAULT 'database',
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE radio_station_items (
  id TEXT PRIMARY KEY,
  station_id TEXT NOT NULL REFERENCES radio_stations(id) ON DELETE CASCADE,
  position BIGINT NOT NULL,
  source_kind TEXT NOT NULL,
  source_id TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  artist TEXT NOT NULL DEFAULT '',
  album TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT 'other',
  duration_seconds BIGINT NOT NULL DEFAULT 0,
  weight BIGINT NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

CREATE TABLE scan_folders (
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_path TEXT NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  mod_time TEXT,
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  PRIMARY KEY (library_id, folder_path)
);

CREATE TABLE scan_jobs (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'all',
  library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
  trigger_source TEXT NOT NULL DEFAULT 'api',
  started_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  finished_at TEXT,
  error TEXT NOT NULL DEFAULT '',
  files_seen BIGINT NOT NULL DEFAULT 0,
  files_pruned BIGINT NOT NULL DEFAULT 0,
  items_pruned BIGINT NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
, files_total BIGINT NOT NULL DEFAULT 0, scan_mode TEXT NOT NULL DEFAULT 'full', files_marked BIGINT NOT NULL DEFAULT 0);

CREATE TABLE user_playback (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  state_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  PRIMARY KEY (user_id, target_kind, target_id)
);

CREATE TABLE user_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
  last_used_at TEXT
);

CREATE INDEX idx_audiobook_chapters_audiobook
  ON audiobook_chapters(audiobook_id, chapter_index);
CREATE INDEX idx_audiobook_contributors_contributor
  ON audiobook_contributors(contributor_id);
CREATE INDEX idx_audiobook_series_series
  ON audiobook_series(series_id);
CREATE INDEX idx_audiobooks_library ON audiobooks(library_id);
CREATE INDEX idx_bookmarks_user_audiobook
  ON bookmarks(user_id, audiobook_id);
CREATE INDEX idx_channel_play_log_channel_started
  ON channel_play_log(channel_id, started_at DESC);
CREATE INDEX idx_channel_schedule_rules_channel
  ON channel_schedule_rules(channel_id, enabled);
CREATE INDEX idx_channel_sources_channel
  ON channel_sources(channel_id, enabled);
CREATE INDEX idx_collection_audiobooks_collection
  ON collection_audiobooks(collection_id, position);
CREATE INDEX idx_collections_user ON collections(user_id);
CREATE INDEX idx_episode_chapters_episode
  ON episode_chapters(episode_id, chapter_index);
CREATE INDEX idx_extracted_covers_source ON extracted_covers(source_path);
CREATE INDEX idx_internet_radio_next_probe
  ON internet_radio_stations(probe_enabled, next_probe_at);
CREATE INDEX idx_internet_radio_stations_name ON internet_radio_stations(name);
CREATE INDEX idx_lastfm_queue_user ON lastfm_scrobble_queue(user_id, created_at);
CREATE INDEX idx_lastfm_scrobble_queue_created ON lastfm_scrobble_queue(created_at);
CREATE INDEX idx_lastfm_scrobble_queue_track ON lastfm_scrobble_queue(track_id, kind, timestamp);
CREATE INDEX idx_lastfm_submissions_created ON lastfm_submissions(created_at DESC);
CREATE INDEX idx_lastfm_submissions_user ON lastfm_submissions(user_id, created_at DESC);
CREATE INDEX idx_listening_sessions_audiobook
  ON listening_sessions(audiobook_id);
CREATE INDEX idx_listening_sessions_user_started
  ON listening_sessions(user_id, started_at DESC);
CREATE INDEX idx_media_files_audiobook ON media_files(audiobook_id);
CREATE INDEX idx_media_files_episode ON media_files(episode_id);
CREATE INDEX idx_media_files_library ON media_files(library_id);
CREATE INDEX idx_media_files_missing ON media_files(library_id, missing);
CREATE INDEX idx_media_files_missing_pid ON media_files(library_id, missing, track_pid);
CREATE INDEX idx_media_files_podcast ON media_files(podcast_id);
CREATE INDEX idx_media_files_track ON media_files(track_id);
CREATE INDEX idx_media_files_track_pid ON media_files(library_id, track_pid);
CREATE INDEX idx_music_artist_external_images_fetched
  ON music_artist_external_images(fetched_at);
CREATE INDEX idx_music_artist_external_meta_fetched
  ON music_artist_external_meta(fetched_at);
CREATE INDEX idx_music_tracks_album ON music_tracks(album_id);
CREATE INDEX idx_podcast_episode_cache_accessed
  ON podcast_episode_cache(last_accessed_at);
CREATE INDEX idx_podcast_episodes_podcast
  ON podcast_episodes(podcast_id);
CREATE INDEX idx_podcast_feeds_next_poll ON podcast_feeds(poll_enabled, next_poll_at);
CREATE INDEX idx_podcast_feeds_podcast
  ON podcast_feeds(podcast_id);
CREATE INDEX idx_podcasts_library ON podcasts(library_id);
CREATE INDEX idx_radio_station_items_station
  ON radio_station_items(station_id, position);
CREATE INDEX idx_scan_folders_library ON scan_folders(library_id);
CREATE INDEX idx_scan_jobs_library ON scan_jobs(library_id);
CREATE INDEX idx_scan_jobs_status ON scan_jobs(status);
CREATE INDEX idx_user_playback_target ON user_playback(target_kind, target_id);
CREATE INDEX idx_user_tokens_user ON user_tokens(user_id);
CREATE INDEX idx_explo_tracks_status ON explo_tracks(status, attempts);
CREATE INDEX idx_explo_tracks_cover_status ON explo_tracks(cover_status);
CREATE INDEX idx_music_albums_recently_added
  ON music_albums(hidden_from_recently_added, added_at DESC);
CREATE INDEX idx_music_tracks_added_at ON music_tracks(added_at);
CREATE INDEX idx_music_album_artists_artist ON music_album_artists(artist_id);
CREATE INDEX idx_music_track_artists_artist ON music_track_artists(artist_id);
