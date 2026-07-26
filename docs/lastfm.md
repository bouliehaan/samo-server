# Last.fm Scrobbling

Samo sends music listens to Last.fm from the server. Clients report playback through the native Samo API; Samo applies Last.fm listen rules, queues failures, and retries in the background.

## Enable

Create API credentials at [last.fm/api/account/create](https://www.last.fm/api/account/create), then add them in Settings -> Account -> Last.fm API Credentials. Operators can also set:

```sh
SAMO_LASTFM_API_KEY=your-api-key
SAMO_LASTFM_SHARED_SECRET=your-shared-secret
```

Optional:

```sh
SAMO_LASTFM_POLL=true
SAMO_LASTFM_POLL_TICK=1m
```

Startup logs show `last.fm scrobbling: enabled` when credentials are active. Credentials saved through the UI take effect without restarting.

## Link an account

All routes require the caller's Samo user token (`Authorization: Bearer ...` or `X-Samo-Token`). Each Samo user links their own Last.fm account; scrobbles and queue/history are scoped to that user.

1. `POST /api/v1/lastfm/auth/begin`

   Returns `{ "authUrl", "token" }`.

2. Open `authUrl` in a browser and approve Samo.

3. `POST /api/v1/lastfm/auth/complete`

   ```json
   { "token": "<token from step 1>" }
   ```

4. `GET /api/v1/lastfm/status` should report `"connected": true`.

Disconnect with `DELETE /api/v1/lastfm/auth/session`.

## How listens are submitted

Samo scrobbles on **measured listening**, not on the position a client reports.
Each playback report is compared with the previous one, and the listen credited
is the smaller of two quantities: how far the track advanced, and how much real
time passed. Resuming a track at 4:07 of 4:08, dragging the scrubber to the end,
or leaving a track paused therefore credit nothing, while ordinary playback
credits second for second.

A listen is submitted once, when it meets the Last.fm rules:

- tracks shorter than 30 seconds are never scrobbled
- at least 30 seconds must have been heard
- and at least half the track duration, or 4 minutes, whichever is lower

Each listen is timestamped when the track started, so it lands in the right
place in your Last.fm history. Skipping a track only prevents a scrobble that
had not yet been earned — as with any Last.fm client, once the threshold is met
the listen counts.

### Exactly once, and never lost

Every scrobble is written to a durable queue and an idempotency ledger in one
transaction *before* Last.fm is contacted. The ledger key identifies the listen
itself, so a retried request, a race between concurrent updates, or a replay
after a crash can all try to submit the same listen and only one goes out.

If Last.fm cannot be reached, the listen stays queued and is retried with a
geometric backoff (30s, 1m, 2m, ... capped at 2 hours) for as long as Last.fm
will still accept its timestamp — roughly two weeks. Outages do not cost you
listens. Responses are checked for the per-scrobble `ignored` status Last.fm
returns alongside HTTP 200, so a rejected listen is reported rather than
silently discarded.

"Now playing" is deliberately never queued: it describes the present moment, and
replaying a stale one would announce the wrong song. Gapless clients that open
the next track's stream before the current one ends do not announce it early.

### Automatic triggers

| Source | When |
|--------|------|
| `PATCH /api/v1/playback/music-track/{id}` | progress updates, play/skip counters, favorite/star changes |
| `PUT /api/v1/playback/music-track/{id}` | full playback state writes |
| `GET /api/v1/music/tracks/{id}/stream` | stream start / resume (now playing) |
| `GET /rest/stream` (Subsonic) | stream start / resume (now playing) |
| `POST /api/v1/scrobble/events` | explicit client events |
| Subsonic `scrobble` / `updateNowPlaying` | compatibility clients |

### Explicit scrobble events

```json
POST /api/v1/scrobble/events
{
  "trackId": "track-id",
  "event": "start",
  "progressSeconds": 0
}
```

Events:

- `start` — begin a new listen and send now playing
- `progress` — report the current position; listening is credited from it
- `complete` — the track finished. This is an explicit client assertion and is
  trusted: a client that reports nothing but `start` and `complete` still gets
  its listen recorded
- `skip` — abandon the listen. Nothing further is credited, and a scrobble
  already earned is not withdrawn

Optional fields: `durationSeconds`, `startedAt` (RFC3339 timestamp used for the scrobble time).

### Love / unlove

When a music track becomes favorited or starred through playback updates, Samo calls Last.fm `track.love`. Clearing favorite and starred calls `track.unlove`.

## Queue, history, and recovery

| Route | Purpose |
|-------|---------|
| `GET /api/v1/lastfm/queue` | pending submissions, with attempt count and next retry time |
| `GET /api/v1/lastfm/history` | local audit log of submitted/queued/dropped attempts |
| `POST /api/v1/lastfm/queue/flush` | retry everything held for this user, ignoring the backoff schedule |

The background poller drains the queue every `SAMO_LASTFM_POLL_TICK`, and once
shortly after startup so a restart mid-outage recovers in seconds. It runs
whether or not credentials existed at boot, so saving them through the UI is
enough to start delivery.

If Last.fm rejects the stored session key, Samo clears the linked account and
requires re-auth; queued listens are held meanwhile and delivered as soon as the
account is reconnected.

## Metadata

Scrobbles include artist, track, album, duration, and MusicBrainz recording ID
when present on the catalog track. If Last.fm rejects a scrobble because it
cannot resolve the MusicBrainz ID, Samo resubmits it without one rather than
lose the listen.

## Current limits

- one linked Last.fm account per Samo user (not per device)
- scrobbling is music-track only (not audiobooks, podcasts, or radio)
- listening is measured from client-reported progress and stream resume
  position, not from byte-count inference mid-stream, so a client that stops
  reporting its position stops accumulating credit until it resumes or reports
  the track as finished

See also [docs/api.md](api.md).
