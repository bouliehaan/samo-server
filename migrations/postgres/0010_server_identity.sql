-- A stable identity for this server, minted once on first boot and never changed.
--
-- Clients previously keyed their on-device state -- the catalog mirror,
-- downloads, and playback progress -- by the server URL. That made the address
-- the primary key: reaching the same server over a LAN IP and over a tunnel
-- hostname looked like two entirely different servers, so switching between
-- them re-synced the whole catalog and orphaned every download.
--
-- With a server-issued ID the address becomes just a transport detail. Clients
-- key by this value, and a server can move between addresses (or be reachable
-- at several at once) without any client losing its local data.
CREATE TABLE IF NOT EXISTS server_identity (
  -- Single-row table: the CHECK keeps the primary key pinned to TRUE, so a
  -- second row is rejected by the database rather than by convention.
  singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  server_id  TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT to_char((now() AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
);
