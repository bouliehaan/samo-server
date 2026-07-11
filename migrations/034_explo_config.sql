-- Admin-configurable explo settings, so the drop folder (and optionally the
-- AcoustID API key) can be chosen from the server web UI instead of only via
-- SAMO_EXPLO_DIRS / SAMO_ACOUSTID_API_KEY environment variables. Single-row
-- table (id = 1), same pattern as lastfm_app_config. When no row exists the
-- environment values are used; a row overrides them. `enabled = 0` pauses the
-- feature even if env vars are set. The API key is stored only when entered in
-- the UI - leaving it blank keeps the env var as the source, so the secret
-- need not be copied into the database.
CREATE TABLE IF NOT EXISTS explo_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0,
  folder TEXT NOT NULL DEFAULT '',
  acoustid_api_key TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
