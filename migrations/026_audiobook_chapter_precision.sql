-- Audiobook chapter + duration precision.
--
-- Chapters and per-file durations used to be whole integer seconds. Multi-file
-- books accumulated each file's rounded duration as the next file's offset, so
-- chapter boundaries drifted by up to a second per file — deep chapters landed
-- in the wrong place. We now keep millisecond precision end-to-end. These
-- columns are the canonical storage; the API still exposes the same
-- `startSeconds`/`durationSeconds` JSON keys (now fractional) so existing
-- clients keep working with no contract change.

-- 1. Exact per-file duration (ffprobe reports fractional seconds).
ALTER TABLE media_files ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;

-- 2. Exact chapter boundaries for audiobooks and podcast episodes.
ALTER TABLE audiobook_chapters ADD COLUMN start_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE audiobook_chapters ADD COLUMN end_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE episode_chapters ADD COLUMN start_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE episode_chapters ADD COLUMN end_ms INTEGER NOT NULL DEFAULT 0;

-- 3. Backfill millisecond columns from the existing whole-second values so any
--    row that is NOT re-scanned (podcasts, music) keeps a correct duration and
--    chapters keep working immediately, just at the old second granularity.
UPDATE media_files SET duration_ms = duration_seconds * 1000 WHERE duration_ms = 0;
UPDATE audiobook_chapters SET start_ms = start_seconds * 1000, end_ms = end_seconds * 1000;
UPDATE episode_chapters SET start_ms = start_seconds * 1000, end_ms = end_seconds * 1000;

-- 4. Force a one-time re-probe + re-chapter of every audiobook. Quick scans skip
--    files whose stored checksum still matches what's on disk; blanking the
--    checksum on audiobook files guarantees the next scan (startup or manual)
--    re-probes each book and rebuilds its chapters with millisecond precision
--    and the Audnexus fallback. Music and podcast files are untouched.
UPDATE media_files SET checksum = '' WHERE audiobook_id IS NOT NULL;
