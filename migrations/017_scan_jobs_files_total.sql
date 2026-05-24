-- migration 017: scan progress total file count
--
-- scan_jobs has tracked files_seen since 003. The dashboard surfaces it as
-- "N files indexed" but there was no way to render a "N of TOTAL" progress
-- indicator because nothing recorded how many files the scanner expected to
-- visit. files_total lets the UI show real progress instead of a number
-- that climbs forever.
--
-- The libraries service pre-walks each library to count audio files before
-- handing off to the scanner; the result lands here. Older jobs keep
-- files_total=0 which the UI treats as "unknown total".

ALTER TABLE scan_jobs ADD COLUMN files_total INTEGER NOT NULL DEFAULT 0;
