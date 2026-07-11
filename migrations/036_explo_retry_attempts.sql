-- Retryable explo identification. The ledger treated every outcome as
-- terminal: one attempt per track, ever. But explo drops are FRESH releases,
-- and AcoustID usually has no fingerprint for a song until days or weeks
-- after release — so the single attempt (run hours after the drop landed)
-- failed for most of the batch and the tracks stayed "Unknown Artist"
-- forever. Track the attempt count so unmatched/error rows can be retried on
-- a backoff until they match or exhaust their budget.
ALTER TABLE explo_tracks ADD COLUMN attempts INTEGER NOT NULL DEFAULT 1;
