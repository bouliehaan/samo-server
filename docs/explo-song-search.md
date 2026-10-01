# Search for new songs and albums through samo-explo

The admin `/app` Search page defaults to **My library**. **Search for new**
appears only when Samo's existing Explo import pipeline is enabled and a live,
authenticated samo-explo connection reports configured download providers.
Availability is checked every ten seconds (two seconds with active downloads) and again before
accepting a search or download request. Administrators see a connection reason
when Explo is configured but its song-search connection is unavailable.

## Connection

Both programs need the updated song-search integration. With Explo's web service
running (`WEB_UI=true`), its existing `EXPLO_SYSTEM=samo`, `SYSTEM_URL` and
admin `API_KEY` settings establish the connection automatically. No additional
URL or shared secret is required for directly reachable services.

Explo registers its listening port and a purpose-specific credential with Samo.
Samo verifies the authenticated callback using the peer address of the request
before persisting it. Explo retries every 30 seconds, including after the setup
wizard saves its configuration. Samo retains the connection across restarts.
Credentials are never returned to the browser. Settings → Explo also reports
connection availability.

Automatic callbacks require Samo to reach Explo at that peer address and port.
Reverse proxies or translated container ports may require explicit overrides:
`SAMO_EXPLO_URL` and `SAMO_EXPLO_TOKEN` on Samo, with the matching
`SAMO_INTEGRATION_TOKEN` on Explo. Explicit settings take precedence. Use a
trusted private network for automatic HTTP callbacks, or HTTPS with an explicit
URL. These overrides are optional network configuration, not the normal setup.

## Whole albums

**Search for new** has **Songs** and **Albums**, once the connected Explo
reports it can search albums (`albums` in the status). An album search finds
MusicBrainz release groups: the album the words name comes first, ahead of its
bootlegs and of albums that merely share a word, and among same-named albums
the one with the most editions. **Tracks** shows what downloading it would get.

A release group holds every edition ever pressed, so Explo picks one: an
official release with the track count most editions share (not the 23-track
reissue or the box set), on CD or digital rather than vinyl sides, the earliest
of those. Its audio discs are the track list; DVDs and video tracks are left out.

**Add album** asks Explo for the tracks one at a time, through the same queue
as a song. Each track is its own request (one `explo_requests` row per
recording, with `album_id` and its place on the album; migration 0031), and
follows the steps below like any song, with three differences:

- **Already have it.** A track the library already holds on an album of that
  name, by recording id or by title, artist and length, is recorded as in your
  library and not downloaded. A song requested on its own earlier joins the
  album instead of downloading twice.
- **Identified as the album's track.** Identification gives each song its own
  best album, which would scatter a soundtrack or a live album across every
  record its songs first came out on. When the audio is the song that was asked
  for, it is credited to the requested album instead, so every track gets that
  album's cover. A wrong download keeps its real identity and stops for review.
- **Kept at its place.** The copy is filed under the album and its album
  artist, numbered from the edition's track list (`2-01 - Title` on the second
  disc of a multi-disc album), and the library twin check only counts the same
  song on the same album, so an album is completed even when one of its songs is
  already in the library on a single or a collection.

YouTube downloads of album tracks are tagged with the album, album artist,
track and disc numbers and the MusicBrainz release ids, so their staged files
group as one album. Asking for an album again resumes it: tracks in the library
or still on their way are left alone and only failed ones are asked for again.

Results show covers: the release group's front cover from the Cover Art
Archive, or a Deezer album's own, fetched by samo (so it leaves through samo's
own route: the VPN on the samo box, or the egress proxy for Deezer's image CDN
when one is configured) and held in memory for a day. A song's cover and album are those of
its original album rather than a compilation or bootleg it is also on, and among
recordings with the same name the officially released one is listed first.

## Two catalogs

MusicBrainz is a volunteer database. An independent artist can be in it with
not one release listed (Quangou was, on 2026-10-01), and then nothing of theirs
can be found there. So Explo searches Deezer alongside it, for songs and albums
both, and lists what only Deezer has, marked **Deezer**. An album or song both
catalogs list appears once, as MusicBrainz's (an edition suffix such as
"(Deluxe)" does not make it a different album). A query that is exactly an
artist's name also lists that artist's Deezer discography, and for songs their
top tracks or, for an artist too small to have any, the tracks of their latest
records. When either catalog is down the other still answers; MusicBrainz is
asked once more after a 503 from its rate limiter.

Deezer ids are `deezer-<n>`. Neither program ever writes one where a
MusicBrainz id belongs: not into a download's tags, not as identification
evidence, not into the ledger or a kept file. A Deezer album has no release
group, so the cover pass finds its art by artist and album name.

MusicBrainz does not know a Deezer-only song, so neither AcoustID nor the text
searches can name the download. Once the identify pass has had its go, whatever
came of it, samo takes the identity from the Deezer listing the song was
chosen from: its title, artist and album become the track's, as identification
would have written them. The one check left is length: a download more than
seven seconds or 4% away from Deezer's length stops at **Needs review**. A song
both catalogs know is identified by its audio as before.

Length alone let one wrong download through: YouTube's only upload from
Quangou's *sloth in motion EP* was track 1, titled with the EP's name, and the
title track "sloth in motion" was downloaded from it too, seven seconds short.
So a Deezer album track is also compared with the album's other tracks by
fingerprint (on the box, never sent anywhere) before its listing is taken. Two
tracks of one album are never one recording: when two came down as the same
audio, the one whose listed length the audio fits worse stops at **Needs
review**, naming the track it duplicates. The comparison waits until none of
the album's tracks is still downloading, so the wrong one is not kept for
arriving first. Explo's YouTube matcher no longer lets an album's name stand
for its title track either: an upload has to name the song outside the album's
name.

## From download to library

Requests use Explo's normal staging configuration resolver for its default
weekly exploration run. Interactive **Auto** downloads try installed YouTube
(yt-dlp + ffmpeg) first, then configured Soulseek. The provider selector can
restrict a request to either provider; the weekly exploration settings are unchanged.
`DOWNLOAD_DIR`, `USE_SUBDIRECTORY`, path templates,
renaming and metadata settings keep their existing meaning.

A completed download lands in the Explo drop folder like any weekly drop, and
Explo reports where (`file`, relative to the drop folder) before it triggers
Samo's usual scan of that folder. From there Samo follows the request
(`explo_requests`, see `internal/explo/requests.go`):

1. **Identifying.** The staged file is found by that name, or, if Explo lost the
   job, by the requested recording id its tags carry. The normal identify pass
   runs with the requested recording as evidence, and with the requested title
   and artist as a last text search behind the usual duration gate.
2. **Cover.** The cover pass runs as for any drop. The request waits up to an
   hour for real art, so the library copy carries it: a generated placeholder
   does not end the wait, since a copy kept with one carries no art for good.
   An album wears one cover. Each of its tracks asks the sources only until one
   track gets through; that track's cover is then the album's, and it goes at
   once, with no network, to every track of the album that has no real art —
   tracks the source refused a moment earlier (placeholders until then),
   tracks not tried yet, and library copies kept before the album had any art.
   A track of an album requested whole wears the album's cover even over art
   of its own (a sharer's embedded picture). The album is the one the track
   was requested with, once identification credited it there; otherwise the
   album it was identified as (the same release group, or the same album and
   artist). A track the library already had keeps its own art.
3. **In library.** Samo keeps the track exactly as **Keep in Library** would:
   a tagged copy filed under `<album artist>/<album>/` in the music library,
   with the identified album, never the compilation a search result named.
   The drop original rotates out with its week.

A request stops at **Needs review** instead of keeping when the identified
audio is a different song from the one asked for (a wrong YouTube upload), when
it could not be identified within the usual retry budget, or when Keep refuses
it (no album settled within a day, or an unwritable library). The file stays in
Explore, where Keep still works by hand. Weekly drops are never kept: only a
download someone asked for by name leaves the silo.

Requesting a download is admin-only, like Keep, because it writes to the
library. Requests advance around every identify pass: after each scan, every 30
minutes, and at boot, so they finish without anyone watching the page.

The existing shared folders and downloader prerequisites still apply: Soulseek
needs its configured download migration into staging; YouTube needs yt-dlp and
ffmpeg. Readiness validates configuration and executables, not whether a source
currently has a particular recording. Search results come from MusicBrainz and
Deezer (see **Two catalogs**). Search requires each word to match the
title or artist (accents aside), handles straight/curly apostrophes, and ranks exact
title + artist matches ahead of partial matches. It uses a release track's length
when the recording length is missing; otherwise it displays **Duration unknown**.

Explo runs requests serially through a bounded queue. Status distinguishes waiting for
an Explo slot, searching a provider, requesting a peer, transfer progress,
conversion and staging. YouTube has a three-minute total budget; Soulseek has a
90-second budget and moves past peers with no progress for 20 seconds. Rejected
peers are retried within that budget. Only a completed file counts as success.
Provider failures remain visible when falling back, and Soulseek transfer history
is retained. Detailed technical diagnostics stay in Explo's logs.
A miss says which kind it was, because only some are worth a retry: YouTube
"found nothing" or "none of its N results was this song", Soulseek "no peer is
sharing this song", "none of the N files peers shared was this song", or "the
peers that answered had no free upload slot" (the one where trying later can
help). A featured guest is left out of the Soulseek search and file match, since
sharers name guests their own way or not at all.
Repeated requests reuse a tracked
job; failed downloads can be retried. Search does not clean files or replace
playlists. Existing staging rotation still applies. Explo holds jobs and recent
searches in memory, so a restart loses downloads still in flight; Samo keeps its
own record of every request, so a staged one still finishes, and a lost
download is reported as failed after 30 minutes. The browser retains its
displayed history in session storage across page refreshes.

## API

Samo routes use account bearer authentication:

- `GET /api/v1/explo/discovery/status`: configuration, connection, availability,
  providers, and an optional reason.
- `POST /api/v1/explo/connection`: admin-only automatic callback registration.
- `GET /api/v1/explo/search?q=artist+song`: recording results.
- `POST /api/v1/explo/downloads` with `{ "id": "<recording-mbid>", "provider": "auto" }`
  (`auto`, `youtube`, or `slskd`; omitted means `auto`): queued job. Admin only.
- `GET /api/v1/explo/downloads/{id}`: job state (`queued`, `downloading`, `staged`,
  or `failed`), with `provider`, `phase`, `progress` and prior failed `attempts`.
  Once staged, `library` carries Samo's side: `state` (`identifying`,
  `in-library` or `needs-review`), `message`, and `libraryTrackId` /
  `libraryAlbumId` once kept. Answered from Samo's record when Explo no longer
  has the job.
- `GET /api/v1/explo/albums?q=artist+album`: albums (`id`, `source`, `title`,
  `artist`, `type`, `secondaryTypes`, `year`); `id` is a release group or
  `deezer-<n>`.
- `GET /api/v1/explo/albums/{id}`: one album from a recent album search, with
  `releaseId` and `tracks` (songs with `albumTrack`: number, disc, totals) and
  each track's `job` when Samo already has a request for it.
- `POST /api/v1/explo/albums/{id}/downloads` with `{ "provider": "auto" }`:
  requests every track not already in the library or on its way. Admin only.
  Answers like the next route; `message` says so when Explo's queue took only
  some tracks.
- `GET /api/v1/explo/albums/{id}/download`: Samo's record of a requested album,
  `tracks` in album order, each with a `job` shaped like a song's download.
- `GET /api/v1/explo/art/{albumId}`: the album's cover from its catalog,
  404 when there is none. Opens with a stream token, for `<img>`.

Song results carry `source` and `albumId`, the album shown with the song, for its
cover. Every song and album id is a MusicBrainz id or `deezer-<n>`.

Explo exposes authenticated `/api/samo/status`, `/api/samo/search`,
`/api/samo/albums`, `/api/samo/albums/{id}`, `/api/samo/downloads` (whose
optional `album` queues a song as a track of an album looked up there), and
`/api/samo/downloads/{id}` endpoints. Authentication
uses the credential derived from its existing Samo login unless explicitly
overridden. Browser session/CSRF protections continue to apply to Explo's UI;
the integration routes use bearer authentication separately.

Only recently searched recordings, or tracks of an album opened from a recent
album search, can be queued. Clients cannot supply download
URLs, filesystem paths, shell arguments, or downloader settings.

For the local test connecting both actual implementations, see
[`integration/explo`](../integration/explo/README.md).

## Regression and live checks

In `samo-explo`, run `go test -race ./src/downloader ./src/web/backend/songsearch
./src/web/backend` for simulated provider acceptance, rejection/retry, missing
sources, deadlines, migration and status propagation. The opt-in command
`EXPLO_TEST_CHET_LIVE=1 go test -run TestLiveChetBakerRequest -v -timeout 4m
./src/web/backend/songsearch` uses the real Chet Baker query, downloads through
yt-dlp into a temporary directory and checks audio duration and artist/title tags
with ffprobe. It does not mutate a library or scheduled configuration.
`EXPLO_TEST_ALBUM_LIVE=1 go test -run 'TestLive(AlbumSearchAndTrackList|DeezerOnlyAlbum)' -v
./src/web/backend/songsearch` searches MusicBrainz for OK Computer and checks the
edition it picks lists the usual twelve tracks, and finds Quangou's Puer
Aeternus, which only Deezer lists; it downloads nothing.
