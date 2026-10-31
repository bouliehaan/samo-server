# Single sign-on and LDAP

samo can let people sign in through an identity provider it does not run:

- **OpenID Connect**: the login page goes straight to the provider, so
  someone already signed in there is simply in. Works with authentik's
  OAuth2/OpenID provider, Keycloak, Authelia, Pocket ID, Zitadel, and others.
- **LDAP**: the ordinary username and password form also accepts directory
  passwords. Works with authentik's LDAP outpost, OpenLDAP, lldap, and
  Active Directory.

Both end in the same samo account and the same bearer token a password
sign-in gives. Nothing downstream changes: playback, the Subsonic adapter,
stream tokens, and device pairing all work as before.

Configure both in **Settings → SIGN-IN** (admins only).

## What stays true

- **Local accounts always work.** A password is checked against samo's own
  accounts first, and only then against LDAP. The admin you made in setup can
  still sign in when the provider is down or misconfigured.
- **A group sync never demotes the last admin with a password.** That account
  is the way back in.
- **Accounts are tied to the provider's stable ID, not the username.**
  Someone renamed in authentik keeps their library. A new person given a freed
  username does not inherit the old one's.

## The login page

With OpenID Connect turned on, `/login` does not draw a form. It sends the
browser to the provider and brings it back to the page it was headed for,
the way Nextcloud's and Immich's auto-launch does. To get the form:

- **`/login?local=1`** shows the username and password form, with the
  provider's button under it. Use it to sign in with a local account (the
  setup admin, or anyone outside the provider), or when the provider is down.
- A sign-in that fails comes back to the form with the reason, never to the
  provider again, so a refusal cannot loop.
- Signing out, of the player or the admin dashboard, lands on the form, so
  you are not signed straight back in.

Apps, TVs and LDAP sign-ins never load `/login` and are unaffected. TV
pairing goes through it and gets the provider's sign-in.

## Account rules

| Setting | What it does |
| --- | --- |
| Public URL | Where browsers reach samo, e.g. `https://samo.example.com`. The OIDC redirect URI is built from it. Set it whenever samo sits behind a reverse proxy. |
| Create a samo account for anyone the provider signs in | Off: an admin creates the account first (or uses the next setting). |
| Adopt an existing samo account with the same username | Links a provider identity to the existing account of that name on its first sign-in. Use it once to bring existing users over, and only if people cannot pick their own usernames at the provider. |
| Admin group | Members become admins and everyone else becomes a user, re-checked on every sign-in. Leave it blank to manage roles on the Users page. |
| Required group | Anyone outside it is refused. You can also do this with authentik's application bindings. |

The Users page lists each account's linked identities. **UNLINK** there undoes
a wrong adoption.

## authentik: OpenID Connect

1. In authentik, go to **Applications → Providers → Create → OAuth2/OpenID
   Provider**.
   - Client type: **Confidential**.
   - Redirect URIs: copy the value from samo's SIGN-IN panel
     (`https://samo.example.com/auth/oidc/callback`). Strict matching is fine.
   - Signing key: any. samo verifies RS256, ES256 and PS256 against
     authentik's published keys. With no signing key set, it verifies the
     HS256 signature with the client secret.
   - Scopes: the defaults (`openid`, `profile`, `email`). authentik's
     `profile` scope already carries `groups`.
2. Create an **Application** that uses the provider, with slug `samo`. Bind
   users or groups to it if you want authentik to decide who gets in.
3. In samo's SIGN-IN panel:
   - Issuer URL: `https://auth.example.com/application/o/samo/`. This is the
     provider's "OpenID Configuration Issuer", trailing slash included.
   - Client ID and secret: from the provider.
   - Button label: `authentik`.
   - Tick **Offer "sign in with …"**, save, then press **TEST CONNECTION**.

### Certificates

samo talks to authentik directly (discovery, token, keys, userinfo), not just
through the browser. If authentik's certificate does not validate from the
samo box (a private CA, or a Cloudflare Origin certificate behind split-horizon
DNS), paste the issuing CA's PEM into **Extra CA certificate**. There is
deliberately no "skip verification" switch for OIDC: that channel carries the
keys that prove who signed in.

## authentik: LDAP

1. Create an **LDAP Provider**, an application for it, and an **LDAP outpost**
   running that provider.
2. Create a service account for samo to search with. Give it search access in
   the provider ("Search group", or the "Bind mode/Search mode" settings in
   newer releases).
3. In samo's SIGN-IN panel:
   - URL: `ldaps://outpost.example.com:6636` (or `ldap://…:3389` on a trusted
     LAN, optionally with StartTLS).
   - Base DN: the provider's base DN, e.g. `dc=ldap,dc=goauthentik,dc=io`.
   - Bind DN: `cn=<service account>,ou=users,dc=ldap,dc=goauthentik,dc=io`,
     plus its password.
   - The rest (user filter `(&(objectClass=user)(cn={username}))`, `cn`,
     `displayName`, `mail`, `memberOf`) are authentik's defaults. authentik's
     directory lists every account, so narrowing the filter to the samo group
     keeps the rest out of the search:
     `(&(objectClass=user)(cn={username})(memberOf=cn=samo-users,ou=groups,dc=ldap,dc=goauthentik,dc=io))`.
     For OpenLDAP or lldap, use `(uid={username})` and username attribute `uid`.
   - Tick **Accept directory passwords**, save, then **TEST CONNECTION** with
     a username to see what samo will read.

Run the outpost next to samo rather than exposing LDAP from wherever authentik
lives. It only connects out to authentik, and samo reaches it on
`ldap://127.0.0.1:3389`. Its image version must match the authentik server's.

The outpost's own certificate is self-signed by default. Paste it under
**Extra CA certificate**, or tick **Accept any certificate** on a LAN you
trust. That setting is exactly as safe as plain `ldap://`.

## Apps, TVs and Subsonic clients

- **Web app** (`/app`, `/listen/`): the login page goes straight to the
  provider; `/login?local=1` is the form.
- **TVs, the Android app, the desktop app, and the separately deployed web
  client**: these already sign in by device pairing (`/pair`). Pairing asks
  you to sign in on the server's own login page, which offers SSO, so they
  get SSO with no change. Any of them can also sign in with an LDAP password
  directly, through the ordinary form.
- **Subsonic clients**: the protocol only knows passwords. Generate a
  Subsonic app password under Settings → Account, which works for SSO and
  LDAP accounts alike. A provider-only account has no samo password to give a
  Subsonic client.

## Reverse proxies

samo does the sign-in itself, so the proxy in front of it (Caddy, Traefik,
nginx, Cloudflare Tunnel, authentik's embedded outpost) only passes traffic
through:

- Set **Public URL** to the address people use. samo builds the redirect URI
  from that, not from the `Host` header the proxy forwards.
- **Do not put authentik's forward-auth or proxy provider in front of samo.**
  It would put an authentik login page in front of `/api`, `/rest`, and the
  stream URLs. Apps, Subsonic clients and cast receivers cannot get past one,
  and they would all break. If you must, exempt `/api/`, `/rest/`,
  `/channels/`, `/radio/`, `/internet-radio/`, `/assets/` and `/health`, and
  let samo's own sign-in protect the rest.

## What it does not do (yet)

- **Sign-out at the provider** (RP-initiated logout). Signing out of samo
  retires samo's token, and the authentik session stays.
- **Revoking samo tokens when someone is disabled in authentik.** A disabled
  user cannot start a new sign-in, but tokens they already hold keep working
  until revoked. Delete the account on the Users page, or reset its password,
  to sign out all its devices.
