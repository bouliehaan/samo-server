-- How much of a channel should be spoken word, 0..1. Zero means the server
-- default (75%).
--
-- The rotation gives talk and music each a target share of airtime and plays
-- whichever is furthest behind, so this single number is what decides how the
-- station feels. It is the only dial on the algorithm, because it is the only
-- part that is taste rather than mechanism.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS talk_share DOUBLE PRECISION NOT NULL DEFAULT 0;
