ALTER TABLE media_files ADD COLUMN missing INTEGER NOT NULL DEFAULT 0;
ALTER TABLE media_files ADD COLUMN missing_detected_at TEXT;

CREATE INDEX IF NOT EXISTS idx_media_files_missing ON media_files(library_id, missing);

ALTER TABLE scan_jobs ADD COLUMN files_marked INTEGER NOT NULL DEFAULT 0;
