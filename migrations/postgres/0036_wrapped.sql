-- samo Wrapped.
--
-- A listener's year comes from their Last.fm account by default, and samo
-- keeps nothing new. These tables are for the listener who turns on samo's
-- own history in the player's settings: from then on samo logs the music they
-- play itself, so their Wrapped no longer depends on Last.fm.
--
-- The plays table is the same shape as lastfm_plays and listenbrainz_plays,
-- because a listen here is measured by the same shared engine
-- (internal/scrobble): it has to be earned by audio that advanced, under the
-- Last.fm rules, before it counts.

CREATE TABLE IF NOT EXISTS wrapped_settings (
    user_id       TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- Off unless the listener turns it on.
    local_history BOOLEAN     NOT NULL DEFAULT FALSE,
    -- When it was last turned on; a Wrapped built from the local log starts here.
    enabled_at    TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Measured listening, one row per (user, track) play in progress.
CREATE TABLE IF NOT EXISTS wrapped_plays (
    user_id          TEXT   NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id         TEXT   NOT NULL,
    play_id          TEXT   NOT NULL DEFAULT '',
    started_at       BIGINT NOT NULL DEFAULT 0,
    last_position    BIGINT NOT NULL DEFAULT 0,
    last_observed_at BIGINT NOT NULL DEFAULT 0,
    last_advance_at  BIGINT NOT NULL DEFAULT 0,
    listened_seconds BIGINT NOT NULL DEFAULT 0,
    duration_seconds BIGINT NOT NULL DEFAULT 0,
    scrobbled        BIGINT NOT NULL DEFAULT 0,
    closed           BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, track_id)
);

-- One row per earned listen. Names are copied rather than joined so a year's
-- Wrapped survives a track being rescanned, retagged or deleted.
CREATE TABLE IF NOT EXISTS wrapped_listens (
    user_id          TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dedupe_key       TEXT        NOT NULL,
    track_id         TEXT        NOT NULL DEFAULT '',
    album_id         TEXT        NOT NULL DEFAULT '',
    artist           TEXT        NOT NULL DEFAULT '',
    album            TEXT        NOT NULL DEFAULT '',
    title            TEXT        NOT NULL DEFAULT '',
    started_at       TIMESTAMPTZ NOT NULL,
    listened_seconds BIGINT      NOT NULL DEFAULT 0,
    duration_seconds BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, dedupe_key)
);

CREATE INDEX IF NOT EXISTS wrapped_listens_by_time
    ON wrapped_listens (user_id, started_at);

-- A built Wrapped, kept so opening it again does not re-read a year of
-- Last.fm history. Rebuilt when stale.
CREATE TABLE IF NOT EXISTS wrapped_reports (
    user_id     TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year        INT         NOT NULL,
    source      TEXT        NOT NULL,
    report_json TEXT        NOT NULL,
    built_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, year, source)
);
