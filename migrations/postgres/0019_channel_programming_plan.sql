-- The station's programming plan, where it currently is in it, and why it
-- played what it played.
--
-- Everything the old scheduler knew about what a radio station IS lived in Go:
-- that talk and music split the hour, that ninety minutes is long enough to
-- talk for, that eight in the morning is when somebody wakes up. That makes
-- exactly one station, and not one its owner can change. These three tables are
-- what let the shape of a day be edited instead of deployed.
--
-- All additive. A channel with no row in any of them behaves exactly as it did
-- before, because the engine derives an equivalent plan from the sources and
-- booked slots the channel already has.

-- The plan document. One per channel, stored whole rather than shredded into
-- rows: it is edited, versioned and validated as a unit, and every attempt to
-- normalise a document like this ends up with a join for every field somebody
-- adds later.
CREATE TABLE IF NOT EXISTS channel_programming_plan (
    channel_id TEXT PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    plan_json  TEXT NOT NULL,
    version    BIGINT NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

-- Where in the plan the station is right now.
--
-- Persisted rather than re-derived because a block is a state, not a formula: a
-- restart in the middle of a sequence should resume the sequence. Without this,
-- every deploy would put the station back to whatever the clock alone allows,
-- which for a block that runs until its pool is exhausted means starting the
-- morning over at four in the afternoon.
CREATE TABLE IF NOT EXISTS channel_program_state (
    channel_id TEXT PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    block_id   TEXT NOT NULL DEFAULT '',
    -- When the station entered this block, so duration-limited blocks know how
    -- much of themselves is left.
    entered_at TEXT NOT NULL DEFAULT '',
    -- How many items this block has aired, for count-limited blocks.
    item_count BIGINT NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);

-- The record of each choice: which block was on and why, what was booked next,
-- what was considered, what was ruled out and for what reason, and what won.
--
-- This exists because "why the hell did it play that" had, until now, as many
-- possible answers as the algorithm had rules and no way to tell them apart
-- from the listening end. Bounded to a few hundred rows per channel by the
-- writer: it is diagnostics with a shelf life of about a day, not an archive.
CREATE TABLE IF NOT EXISTS channel_decisions (
    id            TEXT PRIMARY KEY,
    channel_id    TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    decided_at    TEXT NOT NULL,
    block_id      TEXT NOT NULL DEFAULT '',
    selected_ref  TEXT NOT NULL DEFAULT '',
    decision_json TEXT NOT NULL
);

-- The only way this table is ever read: newest first, for one channel. Also the
-- index the retention delete depends on.
CREATE INDEX IF NOT EXISTS idx_channel_decisions_channel_time
    ON channel_decisions (channel_id, decided_at DESC);
