-- Navidrome-style scan bookkeeping: per-folder content hashes for incremental
-- scans, and persistent track IDs for moved-file reconciliation.

CREATE TABLE IF NOT EXISTS scan_folders (
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_path TEXT NOT NULL,
  hash TEXT NOT NULL DEFAULT '',
  mod_time TEXT,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (library_id, folder_path)
);

CREATE INDEX IF NOT EXISTS idx_scan_folders_library ON scan_folders(library_id);

ALTER TABLE media_files ADD COLUMN track_pid TEXT NOT NULL DEFAULT '';
ALTER TABLE media_files ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_media_files_track_pid ON media_files(library_id, track_pid);
CREATE INDEX IF NOT EXISTS idx_media_files_missing_pid ON media_files(library_id, missing, track_pid);
