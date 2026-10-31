-- Stream tokens (the ?stream_token= credential in <audio src> and cover URLs)
-- used to live only in the server's memory, so every restart invalidated every
-- token a client was holding. Clients cache a token for up to 25 minutes and
-- never re-mint on a 401, so after each deploy an open player could not start
-- a single track until its cached token aged out.
--
-- Only the SHA-256 of the token is stored, the same way user_tokens stores API
-- tokens: the table is not a list of working credentials.
CREATE TABLE IF NOT EXISTS stream_tokens (
    token_hash TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_stream_tokens_user ON stream_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_stream_tokens_expires ON stream_tokens(expires_at);
