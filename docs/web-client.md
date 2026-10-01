# Browser access and user management

Listeners need only a web browser. Open the Samo Server URL and sign in; regular
users go straight to `/listen/`, where music, playlists, audiobooks, podcasts,
and radio use the browser's audio player. There is no client installation,
Electron process, extension, or separate client server. Browser-supported audio
formats and codecs still apply.

Admins default to `/app`. The **Users** navigation item lists and searches
accounts, creates users, edits usernames/display names/roles, resets passwords,
and deletes accounts. **Open player** opens the listener experience; admins can
return through **Server admin**. Pairing links retain their destination at login.

Account endpoints (admin bearer required):

- `GET /api/v1/users` and `POST /api/v1/users`
- `PATCH /api/v1/users/{id}`: optional `username`, `displayName`, `role`, `password`
- `DELETE /api/v1/users/{id}`

Users may edit their own display name/password at `/api/v1/users/me`, but cannot
change their own role or username through that endpoint without admin access.
The reserved server identity cannot be edited/deleted. Admins cannot delete or
demote themselves, and the last human administrator cannot be removed. Database
locks serialize concurrent account changes. Deletion cascades to credentials
and user records with foreign keys; shared catalog media is retained.
Administrative password resets revoke bearer tokens, temporary stream tokens,
and Subsonic app passwords. Editing one's own password preserves their session.

## Building the server's browser assets

The server binary embeds the browser build of the actual Samo renderer, including
its HTML5 audio engines. Checked-in assets let ordinary Go builds work without
Node or a desktop checkout. Nothing is fetched from a desktop installation when
a listener opens the site.

- `make ui` builds the administration/login/setup pages.
- `make ui-client SAMO_CLIENT_SOURCE=/path/to/samo` builds the listener assets.
  The source defaults to the sibling `../samo` checkout, with its dependencies
  already installed using that project's package manager.

`scripts/build-web-client.mjs` copies renderer sources to temporary staging,
adds `web/client/bootstrap.js` and the small session-control stylesheet, then
builds to `internal/api/web-client`. It does not modify the desktop checkout.
The bundle uses `/listen/` assets and hash navigation. PWA registration is omitted;
no service worker or installation is needed. The source repository, revision,
and dirty-checkout flag are recorded in `source.json`; rebuild from the same
source revision and local changes to reproduce a development bundle. The Samo
GPL license ships alongside the generated assets.

The bootstrap verifies the server login before loading any renderer stores,
binds the client to the current origin and user, and clears playback/query caches
when switching accounts. API authorization remains enforced by the server.
