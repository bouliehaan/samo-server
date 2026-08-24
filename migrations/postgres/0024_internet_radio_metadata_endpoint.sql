-- Where to ask a station what it is playing RIGHT NOW.
--
-- The ICY probe already records a station's now-playing line, but it is a
-- health check that happens to carry metadata: it runs every ten minutes and
-- every run opens a fresh stream connection. That is the wrong shape for a
-- now-playing card twice over — a three-minute song is over before the next
-- probe, and for a relay that is metered upstream (a SiriusXM bridge, say)
-- repeatedly pulling audio just to read a title is the exact traffic pattern
-- that gets an account noticed.
--
-- Some stations publish the same information as a plain JSON document that
-- costs the origin nothing to serve. When metadata_url names one, samo asks it
-- instead, often enough to be true, and never opens a second audio connection.
-- Empty means "no such endpoint" and the cached ICY line remains the answer,
-- which is what every station that predates this column keeps doing.
ALTER TABLE internet_radio_stations
  ADD COLUMN IF NOT EXISTS metadata_url TEXT NOT NULL DEFAULT '';

-- Where that station's CURRENT picture lives, when it is not in the JSON.
--
-- Separate from image_url, which is the station's own logo and never changes.
-- This one is a URL whose CONTENTS change with the track — a bridge that
-- re-serves the cover art of whatever is on air. Kept apart so losing the
-- per-track picture falls back to the logo rather than to nothing, and so a
-- station can have one without the other.
ALTER TABLE internet_radio_stations
  ADD COLUMN IF NOT EXISTS metadata_artwork_url TEXT NOT NULL DEFAULT '';
