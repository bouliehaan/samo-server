-- Roles become talk/music, because the balance between them is the split a
-- listener actually notices and nothing else can tell us which is which.
--
-- The scheduler no longer walks a priority ladder — it gives each source a
-- share of airtime and plays whoever is furthest behind. That needs to know
-- whether a source is spoken word or music, and a source's KIND cannot say: a
-- file pool is commercials or oldies, an internet station is BBC or a lofi
-- stream. So the role carries it.
--
-- 'podcast' and 'filler' are the old names. 'filler' meant "plays when there is
-- nothing newer", which in practice was music and old files, so it maps to
-- music; a mis-mapped source is one dropdown to fix and its share simply moves
-- between the two pools.
UPDATE channel_sources SET role = 'talk'  WHERE role = 'podcast';
UPDATE channel_sources SET role = 'music' WHERE role = 'filler';

-- A podcast subscription is spoken word whatever the old backfill guessed.
UPDATE channel_sources SET role = 'talk'
WHERE kind = 'podcast-subscription' AND role NOT IN ('show', 'commercial');

-- Playlists are music by definition.
UPDATE channel_sources SET role = 'music'
WHERE kind = 'music-playlist' AND role NOT IN ('show', 'commercial');
