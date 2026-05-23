ALTER TABLE internet_radio_stations ADD COLUMN now_playing TEXT NOT NULL DEFAULT '';
ALTER TABLE internet_radio_stations ADD COLUMN now_playing_artist TEXT NOT NULL DEFAULT '';
ALTER TABLE internet_radio_stations ADD COLUMN now_playing_title TEXT NOT NULL DEFAULT '';
ALTER TABLE internet_radio_stations ADD COLUMN now_playing_updated_at TEXT;
ALTER TABLE internet_radio_stations ADD COLUMN probe_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE internet_radio_stations ADD COLUMN probe_interval_seconds INTEGER NOT NULL DEFAULT 600;
ALTER TABLE internet_radio_stations ADD COLUMN next_probe_at TEXT;
ALTER TABLE internet_radio_stations ADD COLUMN last_probe_started_at TEXT;
ALTER TABLE internet_radio_stations ADD COLUMN last_probe_finished_at TEXT;
ALTER TABLE internet_radio_stations ADD COLUMN last_probe_error TEXT NOT NULL DEFAULT '';
ALTER TABLE internet_radio_stations ADD COLUMN consecutive_probe_errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE internet_radio_stations ADD COLUMN probe_status TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_internet_radio_next_probe
  ON internet_radio_stations(probe_enabled, next_probe_at);
