-- Audiobook chapter provenance.
--
-- Audible/Audnexus is now the AUTHORITATIVE source of chapter markers: when a
-- book's title + author + runtime verify against an Audible edition, its
-- authored chapters replace whatever the files carried. To make that auditable
-- — and to stop a failed external lookup from silently leaving a book on weak
-- file-derived chapters — record where each book's chapters came from, the ASIN
-- they were resolved from, and when the external sync happened.
ALTER TABLE audiobooks ADD COLUMN chapter_source TEXT NOT NULL DEFAULT '';
ALTER TABLE audiobooks ADD COLUMN chapter_asin TEXT NOT NULL DEFAULT '';
ALTER TABLE audiobooks ADD COLUMN chapter_synced_at TEXT;

-- Force a one-time re-probe + re-chapter of every audiobook so the Audible-first
-- logic actually runs against the existing library. Quick scans skip files whose
-- stored checksum still matches on disk; blanking the checksum on audiobook
-- media_files guarantees the next scan (startup or manual) re-probes each book
-- and rebuilds its chapters from Audible where a match verifies. This is
-- idempotent with 026 (which blanked them for the precision change) — re-blanking
-- already-blank checksums is harmless, and it covers the case where 026 already
-- ran and re-chaptered under the old fallback-only logic. Music and podcast files
-- are untouched.
UPDATE media_files SET checksum = '' WHERE audiobook_id IS NOT NULL;
