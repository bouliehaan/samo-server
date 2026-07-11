-- Tracks whether the cover backfill has already tried to fetch album art for an
-- identified explo track, so it attempts each album once instead of re-querying
-- MusicBrainz + the Cover Art Archive on every scan. '' = pending (never tried),
-- 'done' = attempted (a cover was applied, or none was available). A transient
-- lookup failure leaves it pending so it retries.
ALTER TABLE explo_tracks ADD COLUMN cover_status TEXT NOT NULL DEFAULT '';
