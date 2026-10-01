-- Explo registers its acquisition API using its existing authenticated Samo
-- connection. Keep the callback credential server-side across restarts.
CREATE TABLE IF NOT EXISTS explo_connection (
    id BIGINT PRIMARY KEY CHECK (id = 1),
    base_url TEXT NOT NULL,
    token TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
