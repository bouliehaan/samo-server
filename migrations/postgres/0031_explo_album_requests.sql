-- Whole albums requested through Search for new. Each track is still its own
-- request, one row per recording; album_id ties a track to the album it was
-- asked for with (its MusicBrainz release group, '' for a song asked for on
-- its own), and the placement columns say where on that album it goes. The
-- album is what identification credits the track to and what Keep files it
-- under, so a whole album lands as that album rather than as each song's own
-- best guess.
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS album_id TEXT NOT NULL DEFAULT '';
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS album_artist TEXT NOT NULL DEFAULT '';
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS track_number BIGINT NOT NULL DEFAULT 0;
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS disc_number BIGINT NOT NULL DEFAULT 0;
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS disc_total BIGINT NOT NULL DEFAULT 0;
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS release_year BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_explo_requests_album ON explo_requests (album_id) WHERE album_id <> '';
