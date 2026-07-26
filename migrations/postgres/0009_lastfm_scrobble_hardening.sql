-- Rebuilds Last.fm scrobbling around measured listening and exactly-once
-- delivery.
--
-- The old model kept one latch row per (user, track) in lastfm_track_sessions
-- ("now_playing_sent", "scrobbled") and decided everything from the position a
-- client happened to report. Production logs show what that produced:
--
--   * pressing play on a track whose SAVED RESUME POSITION was near the end
--     scrobbled it instantly, before a single second was heard — most of the
--     scrobbles in the logs are these, e.g.
--       `scrobbling: track="Let Me Down" progress=111/111 source=stream`
--     immediately followed ~90s later by the real one at progress=59/111.
--   * the latch never cleared on a fresh play from 0, so the SECOND time you
--     played a track it silently never scrobbled again.
--   * garbage positions (progress=20853670 for a 257s track) were taken at
--     face value and scrobbled with timestamps months in the past.
--
-- The replacement measures how much audio actually advanced (lastfm_plays),
-- and makes delivery a write-ahead queue guarded by an idempotency ledger
-- (lastfm_scrobble_ledger) so a scrobble is durable before Last.fm is called
-- and can never be submitted twice.

CREATE TABLE IF NOT EXISTS lastfm_plays (
    user_id          TEXT   NOT NULL,
    track_id         TEXT   NOT NULL,
    play_id          TEXT   NOT NULL DEFAULT '',
    -- Fixed when the play begins; the scrobble timestamp Last.fm receives.
    started_at       BIGINT NOT NULL DEFAULT 0,
    last_position    BIGINT NOT NULL DEFAULT 0,
    last_observed_at BIGINT NOT NULL DEFAULT 0,
    -- Last observation that credited real listening. Distinguishes a track
    -- that is genuinely playing from one merely prefetched or paused.
    last_advance_at  BIGINT NOT NULL DEFAULT 0,
    listened_seconds BIGINT NOT NULL DEFAULT 0,
    duration_seconds BIGINT NOT NULL DEFAULT 0,
    scrobbled        BIGINT NOT NULL DEFAULT 0,
    closed           BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, track_id)
);

CREATE INDEX IF NOT EXISTS lastfm_plays_advance
    ON lastfm_plays (user_id, last_advance_at);
CREATE INDEX IF NOT EXISTS lastfm_plays_updated
    ON lastfm_plays (updated_at);

-- One "now playing" per user, because that is what Last.fm models. Keeps a
-- gapless client's PREFETCH of the next track from announcing the wrong song
-- a minute before it actually starts.
CREATE TABLE IF NOT EXISTS lastfm_now_playing (
    user_id    TEXT PRIMARY KEY,
    track_id   TEXT   NOT NULL DEFAULT '',
    play_id    TEXT   NOT NULL DEFAULT '',
    sent_at    BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotency ledger: one row per scrobble ever accepted for delivery. Claimed
-- in the same transaction that writes the queue row, so racing goroutines, a
-- retried request, or a crash-and-replay can all try to scrobble the same play
-- and exactly one wins.
CREATE TABLE IF NOT EXISTS lastfm_scrobble_ledger (
    user_id    TEXT   NOT NULL,
    dedupe_key TEXT   NOT NULL,
    track_id   TEXT   NOT NULL DEFAULT '',
    artist     TEXT   NOT NULL DEFAULT '',
    track      TEXT   NOT NULL DEFAULT '',
    timestamp  BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, dedupe_key)
);

CREATE INDEX IF NOT EXISTS lastfm_scrobble_ledger_created
    ON lastfm_scrobble_ledger (created_at);

-- Queue becomes a durable write-ahead log with real backoff.
ALTER TABLE lastfm_scrobble_queue ADD COLUMN IF NOT EXISTS next_attempt_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lastfm_scrobble_queue ADD COLUMN IF NOT EXISTS played_seconds  BIGINT NOT NULL DEFAULT 0;
ALTER TABLE lastfm_scrobble_queue ADD COLUMN IF NOT EXISTS mbid            TEXT   NOT NULL DEFAULT '';
ALTER TABLE lastfm_scrobble_queue ADD COLUMN IF NOT EXISTS dedupe_key      TEXT   NOT NULL DEFAULT '';
ALTER TABLE lastfm_scrobble_queue ADD COLUMN IF NOT EXISTS source          TEXT   NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS lastfm_scrobble_queue_dedupe
    ON lastfm_scrobble_queue (user_id, dedupe_key) WHERE dedupe_key <> '';
CREATE INDEX IF NOT EXISTS lastfm_scrobble_queue_due
    ON lastfm_scrobble_queue (next_attempt_at, id);

ALTER TABLE lastfm_submissions ADD COLUMN IF NOT EXISTS dedupe_key TEXT NOT NULL DEFAULT '';

-- "Now playing" is ephemeral: replaying an hour-old one announces the wrong
-- song. It is never queued again, so drop what the old code persisted.
DELETE FROM lastfm_scrobble_queue WHERE kind = 'now_playing';

-- The latch table this migration replaces. Its contents ARE the bug: every row
-- says "already scrobbled" for a track that may be played again tomorrow.
DROP TABLE IF EXISTS lastfm_track_sessions;
