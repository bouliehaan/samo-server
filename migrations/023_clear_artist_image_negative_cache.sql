-- Last.fm artist.getInfo now returns placeholder-only images for standard API
-- keys, which filled this table with empty cover_id rows and blocked retries.
DELETE FROM music_artist_external_images WHERE cover_id = '' OR cover_id IS NULL;
