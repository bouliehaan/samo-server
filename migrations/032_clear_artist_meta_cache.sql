-- The artistmeta cache previously stored only library-matched similar artists,
-- so most rows collapsed to 1-2 entries. The enrichment service now also keeps
-- EXTERNAL similar artists (with provider images). Clear the cache once so every
-- artist re-resolves into the richer shape on next read; the service self-heals
-- in the background (bounded by externalFetchLimit) and the negative cache keeps
-- it from re-hammering providers.
DELETE FROM music_artist_external_meta;
