-- Cached EBU R128 loudness analysis, so the radio can level everything it
-- plays without re-reading each file every time it airs.
--
-- Measuring an item means decoding all of it. That is cheap relative to
-- playing it, but it is not free, and a channel that re-measured every track
-- on every rotation would spend more CPU on analysis than on broadcasting.
-- Audio does not change, so one measurement per file is enough forever — this
-- table is what makes "measure once, apply a constant gain" practical.
--
-- What is deliberately NOT stored here is the gain. A gain is a function of
-- the measurement AND the current target level; keeping only the measurement
-- means retuning the station (or the ceiling, or the boost limits) takes
-- effect immediately instead of requiring the whole library to be re-analysed.
CREATE TABLE IF NOT EXISTS loudness_measurements (
    -- Stable identity for the thing measured: "file:<abs path>",
    -- "track:<id>", "episode:<id>", "station:<id>". Namespaced because a
    -- podcast episode id and a track id are both opaque strings and nothing
    -- else stops them colliding.
    cache_key       TEXT PRIMARY KEY,
    -- Changes when the bytes change, so a re-ripped or re-tagged file is
    -- re-measured instead of inheriting the old numbers. Size and mtime for
    -- local files; empty for remote content, which is treated as immutable.
    fingerprint     TEXT NOT NULL DEFAULT '',
    integrated_lufs DOUBLE PRECISION NOT NULL DEFAULT 0,
    true_peak_dbtp  DOUBLE PRECISION NOT NULL DEFAULT 0,
    loudness_range  DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- TRUE when measured from a window rather than the whole item, which is
    -- the only thing possible for a live stream. Partial measurements are
    -- trusted less: they get a tighter boost ceiling and they expire.
    partial         BOOLEAN NOT NULL DEFAULT FALSE,
    -- Non-empty means the last attempt failed. The row still exists so a file
    -- ffmpeg cannot read is not re-attempted on every single play; it is
    -- retried once the record goes stale.
    failure         TEXT NOT NULL DEFAULT '',
    measured_at     TEXT NOT NULL DEFAULT ''
);

-- Finding what still needs measuring — the backfill sweep's only query.
CREATE INDEX IF NOT EXISTS idx_loudness_measured_at ON loudness_measurements (measured_at);
