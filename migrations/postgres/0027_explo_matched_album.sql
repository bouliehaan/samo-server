-- The identified album title, kept in the ledger next to the title and artist.
--
-- Identification already resolves it: bestReleaseGroup picks the release group
-- id and its title TOGETHER, and resolveAlbumTitle asks MusicBrainz for the
-- name when AcoustID reports an id without one. The id was persisted here
-- (0005), the title was not — it went into the album's metadata override and
-- was otherwise thrown away. So Keep, which files the copy under the album,
-- asked MusicBrainz again for a name the server had already been told,
-- synchronously, inside the request: from the phone that was "Keeping…" for
-- thirty seconds through a rate-limited API behind a VPN, then a timeout, and
-- then "Already in your library" on the second tap.
--
-- Rows from before this column are blank. ProcessNewTracks fills them in, one
-- throttled lookup each, on the passes it already runs; Keep reads only.
ALTER TABLE explo_tracks
    ADD COLUMN IF NOT EXISTS matched_album TEXT NOT NULL DEFAULT '';
