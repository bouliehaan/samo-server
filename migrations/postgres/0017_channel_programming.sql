-- What the station needs to reason about its own programming.
--
-- Two things were missing, and between them they produced fifteen hours of
-- talk radio:
--
-- 1. The play log recorded WHICH source aired but not WHAT KIND of listening
--    it was. Balance is a question about categories ("have we had too much
--    spoken word"), and it cannot be asked of a table that only knows source
--    ids. Stored on the row rather than joined from channel_sources because it
--    is a fact about the airing: re-labelling a source later should not rewrite
--    what the station actually sounded like last night.
--
-- 2. A channel had no idea when anybody is awake. "New episode" is only
--    meaningful relative to a listener's day — an episode aired at 03:00 to a
--    dark room has not reached anyone, and treating it as spent is how you wake
--    up to reruns while the overnight drops sit unheard.
ALTER TABLE channel_play_log ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';

-- Backfill from the roles we have, so the balance is not blind for the first
-- window after deploy. Anything not explicitly music is spoken word, which is
-- the same default CategoryOf applies.
UPDATE channel_play_log
SET category = CASE WHEN s.role = 'music' THEN 'music' ELSE 'talk' END
FROM channel_sources s
WHERE s.id = channel_play_log.source_id
  AND channel_play_log.category = '';

-- The listening day, as minute-of-day in the channel's own timezone.
-- Defaults to 08:00–23:00. Airings outside it still happen (the station stays
-- on air overnight) but they do not consume an episode's newness.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS day_start_minute BIGINT NOT NULL DEFAULT 480;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS day_end_minute BIGINT NOT NULL DEFAULT 1380;
