-- How much an airing counted toward reaching anybody, kept on the row.
--
-- The station already knows this at decision time: a block states its exposure,
-- or it falls back to the listening day, and the number is stamped onto the
-- item before it goes out. It was used once, to credit the obligation, and then
-- thrown away — which left the separation rules reading the play log as though
-- every row were an airing somebody heard.
--
-- That is how a new episode ends up both owed and blocked. On 2026-09-03 at
-- 09:12 the station passed over two S-tier episodes with the rejection "this
-- show aired 0s ago, needs 45m0s apart", while the same two episodes sat at the
-- top of the owed queue with credit 0% — nobody had heard them. Both statements
-- came from the same airing. itemSeparation and airingCap already scale
-- themselves by credit for exactly this reason; the source, creator and family
-- windows could not, because the fact they needed was not written down.
--
-- DEFAULT 1 so every existing row keeps binding exactly as it does today. An
-- airing whose exposure nobody recorded is an airing we must assume was heard.
ALTER TABLE channel_play_log
    ADD COLUMN IF NOT EXISTS exposure DOUBLE PRECISION NOT NULL DEFAULT 1;
