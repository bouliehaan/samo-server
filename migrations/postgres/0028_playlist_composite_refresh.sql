-- Composite IDs change with their source covers, but older servers saved every
-- revision under the unique source_path 'composite:<playlist-id>'. The second
-- revision failed to persist and the cover endpoint returned a single album.
-- The compositor now versions both keys and preserves the old image records.
--
-- Bump the artwork URL's existing updatedAt stamp once so mobile disk caches
-- and desktop caches discard those successful-but-wrong single-cover responses.
-- Delta sync picks this up without a client rebuild or a library-wide cache wipe.
UPDATE music_playlists AS playlist
SET updated_at = to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
WHERE EXISTS (
    SELECT 1 FROM extracted_covers AS cover
    WHERE cover.source_path = 'composite:' || playlist.id
);
