CREATE TABLE IF NOT EXISTS scan_jobs (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'all',
  library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
  trigger_source TEXT NOT NULL DEFAULT 'api',
  started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at TEXT,
  error TEXT NOT NULL DEFAULT '',
  files_seen INTEGER NOT NULL DEFAULT 0,
  files_pruned INTEGER NOT NULL DEFAULT 0,
  items_pruned INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_scan_jobs_status ON scan_jobs(status);
CREATE INDEX IF NOT EXISTS idx_scan_jobs_library ON scan_jobs(library_id);
