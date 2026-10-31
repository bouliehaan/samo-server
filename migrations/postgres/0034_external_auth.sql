-- Signing in through an identity provider: OpenID Connect (authentik's
-- OAuth2/OpenID provider, or any other) and LDAP (authentik's LDAP outpost, or
-- any directory). A samo account is still what every token, play and setting
-- hangs off; these tables only say how a person proves they are one.
--
-- The provider settings are one row, kept as JSON so a new knob is not a new
-- migration. They hold the OIDC client secret and the LDAP bind password, the
-- same way lastfm_app_config holds the Last.fm shared secret: the database is
-- already the thing an attacker would need.
CREATE TABLE IF NOT EXISTS auth_provider_config (
    id         BIGINT PRIMARY KEY CHECK (id = 1),
    settings   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One row per external identity linked to a samo account. provider is
-- 'oidc:<issuer>' or 'ldap'; subject is what that provider promises never to
-- reuse (the OIDC sub claim, the directory's entryUUID or the entry's DN). The
-- username is never the key: a directory can rename someone, and a name freed
-- by one person can be taken by the next.
--
-- An account may have several (an OIDC identity and the same person's LDAP
-- entry); a given identity belongs to exactly one account.
CREATE TABLE IF NOT EXISTS user_identities (
    provider      TEXT        NOT NULL,
    subject       TEXT        NOT NULL,
    user_id       TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username      TEXT        NOT NULL DEFAULT '',
    email         TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);
