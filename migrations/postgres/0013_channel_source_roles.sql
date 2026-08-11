-- Channel source roles: what a piece of content IS, so the scheduler can order
-- itself instead of asking the operator to.
--
-- Channels used to program by weighted random selection over a rotation pool.
-- That models a bag of dice, and what a radio station actually is is a ladder:
-- a scheduled show beats a new podcast episode, which beats filler. Expressing
-- that as weights meant the operator had to encode the ordering by hand, and
-- getting it slightly wrong put a music playlist on top of a scheduled news
-- block. The order is not a preference — it is what the product is — so it now
-- lives in the engine and the operator only says what each source is.
--
--   show       only ever plays when a schedule rule calls for it
--   podcast    supplies fresh unheard episodes, and reruns when there are none
--   filler     music and files that play when there is nothing newer
--   commercial padding between items, never a thing the channel "plays"
--
-- weight and default_rotation are left in place: weight still breaks ties
-- inside a tier, and default_rotation is what this backfill is derived from.
ALTER TABLE channel_sources ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT '';

-- Backfill preserves today's behaviour as closely as the old model allows.
-- default_rotation = 0 meant "only fires on a schedule rule", which is exactly
-- the new `show` role. Everything else was in the rotation bag: podcast
-- subscriptions become `podcast`, the rest becomes `filler`.
--
-- Note default_rotation and enabled are BIGINT 0/1 in this schema, not boolean.
UPDATE channel_sources
SET role = CASE
    WHEN default_rotation = 0 THEN 'show'
    WHEN kind = 'podcast-subscription' THEN 'podcast'
    ELSE 'filler'
END
WHERE role = '';

-- The scheduler filters by role on every pick, per channel.
CREATE INDEX IF NOT EXISTS idx_channel_sources_channel_role
    ON channel_sources (channel_id, role);
