# samo Wrapped

Each listener's year in samo, at `/wrapped`: minutes listened, top artists,
songs and albums, when in the day they listen, the song they had on repeat,
their month, and a verdict — Baller, Shot caller, or Not-at-aller. It is a run
of full-screen slides made for a phone: tap the right of the screen for the
next, the left to go back, and press and hold to pause.

Open it from the player's menu (**samo Wrapped**) or from **Settings → General
→ samo Wrapped**. It is there all year: until December it is the year so far.

## Where the year comes from

**Last.fm, by default.** A listener who has linked Last.fm (Settings → General →
Scrobbling) gets a Wrapped read from their Last.fm history for the year, which
holds everything they scrobbled: samo, and any other app or service that
scrobbles to the same account. samo keeps nothing new for this. Reading a big
year is a few hundred requests to Last.fm, so the first time takes a minute and
the page shows how far it has got; after that it is kept and reread at most
every six hours (never, once the year is over).

**samo's own history, if the listener turns it on.** Settings → General →
samo Wrapped → **Keep my listening history on samo**. It is off unless turned on,
and each listener decides for themselves. With it on:

- samo logs the music they play, measured by the same rules as a Last.fm
  scrobble (at least 30 seconds and half the track heard; seeking and pausing
  do not count; one listen is logged once). See [lastfm.md](lastfm.md).
- their audiobooks (hours, books finished) and podcasts (episodes, shows) are
  added to the year, from what samo already records to resume them.
- a listener without Last.fm gets a Wrapped from samo's log alone, covering the
  time since they turned it on.

When both are available, the music comes from Last.fm, which has the whole year.
Turning the history off stops logging and keeps what was logged; **Delete samo's
history of my listening** erases it. Nobody can see or change another
listener's history or switch, admins included.

Only music listens count towards the minutes and the verdict. Radio, samo-radio
devices and internet radio are never counted.

## Numbers

- **Minutes** are real listening for samo's log. A Last.fm scrobble of a track
  samo has takes that track's length; one samo does not have (from another app)
  is counted as 3½ minutes, and the slide says "≈" when more than a quarter of
  the year was estimated that way.
- **Days and hours** are read in this machine's time zone, the same one radio
  schedules use (`SAMO_TIMEZONE`).
- **The verdict** is minutes per day of the year covered: 90 or more is a
  Baller, 30 or more a Shot caller, anything less a Not-at-aller. Per day, so a
  Wrapped opened in March, or from a history turned on in June, is not judged
  against a whole year.

## API

All routes act for the signed-in user (`Authorization: Bearer …`).

| Route | |
|---|---|
| `GET /api/v1/wrapped?year=2026` | The year. `202` with `{"building": {"pages", "totalPages"}}` while it is read from Last.fm; ask again. `404` with `"code": "no-source"` when there is nothing to build it from. `&refresh=1` rereads a Last.fm year more than ten minutes old. |
| `GET /api/v1/wrapped/settings` | `{"localHistory", "enabledAt", "listens"}` |
| `PUT /api/v1/wrapped/settings` | `{"localHistory": true}` turns samo's own history on (or off). |
| `DELETE /api/v1/wrapped/history` | Erases the listening samo has logged for the user. |

Tables (migration `0036_wrapped.sql`): `wrapped_settings`, `wrapped_plays`
(listen measurement, as for Last.fm and ListenBrainz), `wrapped_listens` (one
row per logged listen) and `wrapped_reports` (built years).
