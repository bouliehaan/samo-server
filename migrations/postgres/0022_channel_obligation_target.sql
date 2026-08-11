-- How much credit settles an obligation, per episode.
--
-- It used to be a constant: one full airing in a block that counts, and the
-- episode was done. That is the right answer for most of a station's output and
-- the wrong one for the shows you actually tuned in for — miss two hours and a
-- single airing means you simply never hear it.
--
-- On the row rather than computed at read time, because the settle happens in
-- SQL (the streamer and the API are different connections, and a
-- read-modify-write there would lose an airing under concurrency). Existing
-- rows keep the old behaviour.
ALTER TABLE channel_obligations
    ADD COLUMN IF NOT EXISTS target DOUBLE PRECISION NOT NULL DEFAULT 1;
