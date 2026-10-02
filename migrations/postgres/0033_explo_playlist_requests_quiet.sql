-- Songs a YouTube Music playlist import downloads are the playlist's, not
-- something someone added to the library by hand, so their albums stay off
-- Recently Added (and the Home heroes that follow it): a 40-song import was a
-- wall of 40 one-song albums on the shelf. Two facts decide it, per request:
--   for_playlist  - a playlist import asked for it (not Search for new);
--   library_copy  - library_track_id is the copy its Keep made, not a track
--                   the library already had.
-- An album whose every library track is such a copy is left out of Recently
-- Added when the catalog loads (catalogstore/load_seed.go).
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS for_playlist BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE explo_requests ADD COLUMN IF NOT EXISTS library_copy BOOLEAN NOT NULL DEFAULT FALSE;

-- Before this, only playlist imports asked for youtube-<video> ids, and a
-- request Keep copied said so in its message.
UPDATE explo_requests SET for_playlist = TRUE WHERE recording_id LIKE 'youtube-%';
UPDATE explo_requests SET library_copy = TRUE
WHERE state = 'in-library' AND library_track_id <> '' AND message IN ('Added to your library.', 'Kept from review.');

CREATE INDEX IF NOT EXISTS idx_explo_requests_playlist_copy
    ON explo_requests (library_track_id) WHERE for_playlist AND library_copy;

-- Albums already on the shelf from an import: touched, so clients that sync
-- by updated_at pick up that they are now left out.
UPDATE music_albums SET updated_at = to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
WHERE id IN (
    SELECT mt.album_id FROM music_tracks mt
    JOIN explo_requests er ON er.library_track_id = mt.id AND er.for_playlist AND er.library_copy
    WHERE mt.album_id IS NOT NULL);
