-- 0005_explo_silo_and_cover_retry.sql
--
-- Explo becomes a first-class silo and its cover pipeline becomes retryable.
--
-- 1) music_tracks.is_explo: per-track marker maintained by the explo
--    reconciler from the configured drop folder(s), in both directions, so
--    the catalog projection and every listing surface can exclude explo
--    content without joining on file paths. Partial index because the explo
--    set is a tiny fraction of the library.
--
-- 2) explo_tracks.musicbrainz_release_group_id: persists the release group
--    AcoustID/MusicBrainz already reported at identification time. The cover
--    backfill previously re-derived it with a throttled MusicBrainz lookup
--    per album — and could pick a different release group than the one that
--    identified the track.
--
-- 3) explo_tracks cover retry state: cover_status used to be one-shot
--    ('' -> 'done' whether or not art was actually found, so a Cover Art
--    Archive miss wrote the album off forever). It becomes a small state
--    machine ('' never tried, 'pending' retrying, 'placeholder' generated
--    tile applied while still retrying, 'done' verified art on disk) paced
--    by cover_attempts / cover_attempted_at.
--
-- 4) Reset the historical one-shot 'done' rows: many were written off after
--    a single CAA miss, some with an override pointing at a dead external
--    URL. The new backfill re-verifies each album — ones that already have
--    local art on disk are marked 'done' again without any network traffic.

ALTER TABLE music_tracks ADD COLUMN IF NOT EXISTS is_explo BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_music_tracks_is_explo ON music_tracks(is_explo) WHERE is_explo = 1;

ALTER TABLE explo_tracks ADD COLUMN IF NOT EXISTS musicbrainz_release_group_id TEXT NOT NULL DEFAULT '';
ALTER TABLE explo_tracks ADD COLUMN IF NOT EXISTS cover_attempts BIGINT NOT NULL DEFAULT 0;
ALTER TABLE explo_tracks ADD COLUMN IF NOT EXISTS cover_attempted_at TEXT NOT NULL DEFAULT '';

UPDATE explo_tracks SET cover_status = '' WHERE cover_status = 'done';
