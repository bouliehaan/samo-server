-- 0006_fk_cascade_indexes_and_json_extract_guard.sql
--
-- Two robustness fixes to the core schema.
--
-- 1) Foreign-key cascade indexes.
--
--    Postgres does NOT auto-create an index for a foreign key's referencing
--    column. Without one, deleting a parent row seq-scans the child table to
--    find the rows to cascade-delete, and every FK integrity check on the
--    child scans too. Four single-column FKs — all ON DELETE CASCADE — were
--    only covered by a composite index whose LEADING column is something else
--    (so it can't serve the FK lookup):
--
--      bookmarks.audiobook_id          (idx leads with user_id)
--      collection_audiobooks.audiobook_id (idx/PK leads with collection_id)
--      channel_schedule_rules.source_id   (idx leads with channel_id)
--      podcast_episodes.library_id        (only indexed on podcast_id)
--
--    Deleting an audiobook scanned bookmarks and collection_audiobooks;
--    deleting a library scanned every podcast_episodes row; deleting a channel
--    source scanned channel_schedule_rules. These indexes turn those scans
--    into index lookups. All four target tables are small in practice, so the
--    plain (non-CONCURRENT) build — required because the migration runner
--    wraps each file in a transaction — locks them only briefly.
--
-- 2) json_extract() hardening.
--
--    The SQLite-compat json_extract(text, path) shim casts its first argument
--    to jsonb, which RAISES on an empty string ('' is not valid JSON). Today
--    every column it reads (media_files.embedded_tags_json) is NOT NULL
--    DEFAULT '{}' and only ever written valid JSON, so this never fires — but
--    a single '' slipping in (a manual fix, a future write path) would fail
--    the ENTIRE cross-library scan-match query, not just skip one row.
--    NULLIF(...,'') makes an empty string decode to NULL (no match), matching
--    both SQLite's leniency and the json_each shim's '' handling. Behavior for
--    every valid input is unchanged.

CREATE INDEX IF NOT EXISTS idx_bookmarks_audiobook
  ON bookmarks(audiobook_id);
CREATE INDEX IF NOT EXISTS idx_collection_audiobooks_audiobook
  ON collection_audiobooks(audiobook_id);
CREATE INDEX IF NOT EXISTS idx_channel_schedule_rules_source
  ON channel_schedule_rules(source_id);
CREATE INDEX IF NOT EXISTS idx_podcast_episodes_library
  ON podcast_episodes(library_id);

CREATE OR REPLACE FUNCTION json_extract(j text, path text) RETURNS text
LANGUAGE sql IMMUTABLE AS $fn$
  SELECT (NULLIF(j, '')::jsonb #>> string_to_array(regexp_replace(path, '^\$\.', ''), '.'))
$fn$;
