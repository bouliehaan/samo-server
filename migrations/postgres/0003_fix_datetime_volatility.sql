-- The datetime() compatibility shim was declared IMMUTABLE but calls now(),
-- which is VOLATILE. IMMUTABLE allows the optimizer to constant-fold the
-- result within a query plan, so two rows in the same query batch can receive
-- the same cached timestamp from a single now() evaluation. STABLE is the
-- correct volatility: the result is consistent within a single SQL statement
-- but may differ across statements, which matches the semantics of now().
-- The pure-text-arithmetic ELSE branch IS immutable, but the function as a
-- whole must be at least STABLE because of the now() path.
CREATE OR REPLACE FUNCTION datetime(ts text, modifier text) RETURNS text
LANGUAGE sql STABLE AS $fn$
  SELECT to_char(
    ((CASE WHEN lower(ts) = 'now' THEN (now() AT TIME ZONE 'UTC')
           ELSE replace(replace(ts, 'T', ' '), 'Z', '')::timestamp END)
     + replace(modifier, '+', '')::interval),
    'YYYY-MM-DD"T"HH24:MI:SS"Z"')
$fn$;

-- Missing indexes for explo, Recently Added, and reverse artist lookups.
-- These are present in the Postgres consolidated schema from this changeset
-- onward, but existing Postgres installations need an incremental migration.
CREATE INDEX IF NOT EXISTS idx_explo_tracks_status
  ON explo_tracks(status, attempts);
CREATE INDEX IF NOT EXISTS idx_explo_tracks_cover_status
  ON explo_tracks(cover_status);
CREATE INDEX IF NOT EXISTS idx_music_albums_recently_added
  ON music_albums(hidden_from_recently_added, added_at DESC);
CREATE INDEX IF NOT EXISTS idx_music_tracks_added_at
  ON music_tracks(added_at);
CREATE INDEX IF NOT EXISTS idx_music_album_artists_artist
  ON music_album_artists(artist_id);
CREATE INDEX IF NOT EXISTS idx_music_track_artists_artist
  ON music_track_artists(artist_id);
