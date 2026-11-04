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

## On a phone

Below 768px wide (or a phone on its side), `/listen/` is laid out like the
Android app rather than the desktop: a top bar with back, the unified search
and a menu; the page; a mini player; and the Android app's five tabs — Home,
Podcasts, Audiobooks, Playlists, Radio. The menu has the rest: Playlists,
Audiobooks, Podcasts and Internet Radio; Albums, Artists, Favorites and All
Music (which leads on to songs and genres); then settings, server admin and
sign out.

- **Tap plays.** A tap on a song plays it and a tap on an album opens it; the
  desktop's single click only selected. Each row has a ⋯ for the rest, and a
  long press opens the same menu — on iPhones too, where Safari never sends a
  right click. Menus open as sheets along the bottom of the screen. Drag and
  drop is off on touch screens, where a resting finger would pick a cover up
  instead of scrolling.
- **Lock screen.** Title, artist and cover show on the lock screen and in
  Control Center, with play/pause, next/previous and seeking. On iOS samo
  plays through a plain `<audio>` element rather than Web Audio, which WebKit
  silences when the screen locks (ReplayGain is not applied there as a result).
- **Add to Home Screen.** On an iPhone, Safari's Share → Add to Home Screen
  gives samo its own icon and a full-screen window with no browser bars, over
  plain HTTP as well. A home-screen app keeps its own storage there, so it asks
  you to sign in once more the first time it opens. Chrome on Android installs
  it as an app only from an HTTPS address; from a plain `http://` LAN address
  its Add to Home screen makes a shortcut that opens in a browser tab.

The layout is in `web/client/mobile`, copied into the renderer as
`src/renderer/server-mobile`; `scripts/web-client-patches.mjs` holds the hooks
into the renderer for it and the touch fixes that belong in the renderer's own
components. As with the other patches, a build fails rather than ship one
that no longer applies to the renderer source.
