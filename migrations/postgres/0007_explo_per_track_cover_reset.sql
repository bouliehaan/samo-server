-- Explo covers moved from per-ALBUM to per-TRACK.
--
-- Every explo drop is a distinct release, but the untagged files share one
-- folder-derived album, so the old album-wide cover pass applied ONE cover to
-- that shared album — and every track in the Explore playlist rendered the same
-- art (and the playlist tile could never composite a real 2x2 grid). The cover
-- engine now resolves and applies art per track, into music_tracks — but only
-- for rows that are still due (cover_status in '', 'pending', 'placeholder').
--
-- Reset the cover state of every already-identified drop so the per-track engine
-- re-resolves art onto each track on the next pass (startup BackfillCovers, or
-- the next scan). Identification (title/artist) is deliberately untouched — this
-- only re-runs the cover pass. Runs once (migrations are keyed by filename).
UPDATE explo_tracks
SET cover_status = '',
    cover_attempts = 0,
    cover_attempted_at = ''
WHERE status IN ('matched', 'matched-fallback');
