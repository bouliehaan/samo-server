-- Seed the reserved bootstrap user.
--
-- SQLite-era migration 008 seeded this row as DATA, but 0001_init.sql was
-- generated from the schema catalog only, so fresh Postgres databases never
-- received it (databases that came through the old SQLite importer got it via
-- the data copy). The legacy SAMO_API_TOKEN mapping and server-wide Last.fm
-- state hang off this row, and user_playback/lastfm_user_settings reference
-- it by foreign key.
INSERT INTO users (id, username, display_name, role, password_hash)
VALUES ('user-server', 'server', 'Server', 'admin', '')
ON CONFLICT (id) DO NOTHING;
