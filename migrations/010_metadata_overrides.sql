CREATE TABLE IF NOT EXISTS metadata_overrides (
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  fields_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (target_kind, target_id)
);
