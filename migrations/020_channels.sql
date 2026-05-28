-- Channels are Samo-native programmed radio: a 24/7 stream the user
-- composes from podcast subscriptions, local file pools, live-stream
-- cut-ins, and time-windowed scheduled blocks. Distinct from the
-- existing `radio_stations` loop concept — channels have a scheduler
-- that picks "what plays next" based on time and rules, not a fixed
-- rotation.

CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  -- Encoder output. Everything the scheduler picks gets transcoded to
  -- this format so podcast + commercial + live-stream can mux into a
  -- single continuous output.
  codec TEXT NOT NULL DEFAULT 'mp3',
  bitrate_kbps INTEGER NOT NULL DEFAULT 192,
  sample_rate_hz INTEGER NOT NULL DEFAULT 44100,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- A source is something the scheduler can pull from. Kind drives how
-- the source resolver materializes a playable item:
--   file-pool          → pick a file from a path / library / explicit list
--   podcast-subscription → pull latest unplayed episode from an RSS feed
--   live-stream        → proxy an internet radio station URL
--   scheduled-show     → reserved for future show-block use; today it
--                        behaves like file-pool but with a label
--
-- `config_json` holds kind-specific options (paths, feed id, station
-- id, etc.). `default_rotation` flags sources eligible for the
-- rotation pool when no schedule rule matches the current time.
CREATE TABLE IF NOT EXISTS channel_sources (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  config_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  weight INTEGER NOT NULL DEFAULT 1,
  default_rotation INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_channel_sources_channel
  ON channel_sources(channel_id, enabled);

-- A schedule rule pins a source to a time window. When the current
-- moment falls inside the window AND the rule is enabled, the
-- scheduler bypasses the rotation pool and plays from the rule's
-- source until the window ends.
--
-- `weekday_mask` is a 7-bit field: Sun=1, Mon=2, Tue=4, Wed=8, Thu=16,
-- Fri=32, Sat=64. `start_minute` and `end_minute` are minute-of-week
-- (0-10079) so cross-midnight windows can be modeled by setting
-- `start > end` or by adding two rows. Keep it as minute-of-day (0-1439)
-- and pair with weekday_mask — simpler to reason about.
CREATE TABLE IF NOT EXISTS channel_schedule_rules (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL REFERENCES channel_sources(id) ON DELETE CASCADE,
  label TEXT NOT NULL DEFAULT '',
  weekday_mask INTEGER NOT NULL DEFAULT 127,
  start_minute INTEGER NOT NULL,
  end_minute INTEGER NOT NULL,
  priority INTEGER NOT NULL DEFAULT 100,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_channel_schedule_rules_channel
  ON channel_schedule_rules(channel_id, enabled);

-- A play log row is written every time the scheduler hands an item to
-- the streamer. The scheduler reads recent rows to (a) avoid back-to-
-- back repeats and (b) power the "recently played" UI.
CREATE TABLE IF NOT EXISTS channel_play_log (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  source_id TEXT NOT NULL DEFAULT '',
  item_ref TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  artist TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL DEFAULT '',
  duration_seconds INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_channel_play_log_channel_started
  ON channel_play_log(channel_id, started_at DESC);
