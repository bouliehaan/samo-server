-- The rest of where-in-the-plan-we-are.
--
-- 0019 stored the three fields the block state machine had at the time. It has
-- since grown a position in a repeating cycle, a queue of already-decided
-- programming, and a flag for whether the last item was part of a break — and
-- all three have to survive between decisions, not just within one.
--
-- The break flag in particular: without it, the rule "put a break between these
-- two things" re-fires on the break's own last item, so the station separates
-- the separator from the programming, for ever. That is not a subtle failure;
-- it is a station that plays nothing but breaks. It only shows up once the state
-- goes through the database, which is why it survived the tests.
--
-- Stored as one JSON document rather than a column per field: this is the
-- scheduler's own scratch state, it changes shape as the model grows, and
-- nothing outside the scheduler queries it. The existing columns stay as
-- readable copies for anybody looking at the table by hand.
ALTER TABLE channel_program_state
    ADD COLUMN IF NOT EXISTS state_json TEXT NOT NULL DEFAULT '';
