-- What the station OWES the listener.
--
-- A new episode used to be "another candidate with a good freshness score",
-- which meant there was nothing to report, nothing to simulate, and no way to
-- say that airing it at three in the morning did not really count. Worse: an
-- airing was a boolean, so a five-minute preemption or an early skip burned the
-- episode outright — it had "been on air", so it was never offered again.
--
-- As a row, all of that becomes arithmetic. Credit accumulates
-- (played fraction x how much the block it aired in counts for), and an episode
-- is only satisfied once it has actually reached somebody.
CREATE TABLE IF NOT EXISTS channel_obligations (
    channel_id   TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    -- The item this is owed for: "episode:<id>". Namespaced the same way the
    -- play log's item_ref is, because they are joined by eye constantly.
    item_ref     TEXT NOT NULL,
    source_id    TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    -- S..F, copied from the source AT THE MOMENT THE EPISODE WAS NOTICED.
    -- Deliberately a copy rather than a join: re-rating a show tomorrow should
    -- not silently re-order a queue the listener has already half heard.
    tier         TEXT NOT NULL DEFAULT 'C',
    -- An obligation's life runs from publication, not from when the station
    -- happened to look, so a feed that was down for a day does not hand its
    -- episodes a fresh 72 hours.
    published_at TEXT NOT NULL DEFAULT '',
    noticed_at   TEXT NOT NULL DEFAULT '',
    expires_at   TEXT NOT NULL DEFAULT '',
    -- Accumulated exposure, 0..1+. Not a boolean: "did this reach anybody" is a
    -- spectrum.
    credit       DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- pending | satisfied | expired. Expiry is settled on read because it is a
    -- pure function of the clock, and a sweeper that has not run yet is one
    -- more way for the station to behave differently from what the table says.
    state        TEXT NOT NULL DEFAULT 'pending',
    airings      BIGINT NOT NULL DEFAULT 0,
    updated_at   TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
    PRIMARY KEY (channel_id, item_ref)
);

-- The only query that runs on every decision: what is still owed, newest first.
CREATE INDEX IF NOT EXISTS idx_channel_obligations_state
    ON channel_obligations (channel_id, state, published_at DESC);
