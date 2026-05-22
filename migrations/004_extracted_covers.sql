CREATE TABLE IF NOT EXISTS extracted_covers (
  id TEXT PRIMARY KEY,
  source_path TEXT NOT NULL UNIQUE,
  source_checksum TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL,
  mime_type TEXT NOT NULL DEFAULT 'image/jpeg',
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  extracted_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_extracted_covers_source ON extracted_covers(source_path);
