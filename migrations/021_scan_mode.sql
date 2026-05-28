-- Track whether a scan job was a full metadata walk or a quick
-- checksum-only rescan. The dashboard uses this to distinguish startup
-- rescans from operator-triggered full library scans.
ALTER TABLE scan_jobs ADD COLUMN scan_mode TEXT NOT NULL DEFAULT 'full';
