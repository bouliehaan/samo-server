# Samo Channels

Channels are Samo's personal 24/7 programmed radio. A channel pulls
from a mix of source kinds (podcast subscriptions, local file pools,
internet radio cut-ins) and a scheduler decides what plays next based
on time-of-day rules. ffmpeg transcodes every source through a single
codec/bitrate so podcast → commercial → live NPR all mux into one
continuous output that feels like real radio — not a glorified
playlist.

Channels live alongside the existing `radio_stations` loop concept
but are a distinct domain: stations are deterministic rotations,
channels are intelligently programmed streams.

## Mental model

A channel is a station you leave on, not a playlist you assemble. You say what
each piece of content **is** and what the day should be shaped like; the
scheduler works out the running order.

The engine knows **how to programme radio**. The station's **plan** says **what
radio to programme**. Nothing about talk, music, mornings, podcasts or waking
hours is compiled into the scheduler — those are all things a plan can say, and
a different plan says something else.

### The plan

One document per channel, edited in the PROGRAMME screen or over
`PUT /api/v1/channels/{id}/plan`. It has four moving parts:

- **Categories** — the station's own names for kinds of programming, each with a
  share of airtime. `talk` and `music` are only the default; a station can run
  `comedy`, `audiobook`, `oldtime`, `sports`.
- **Pools** — reusable named sets of sources. Pools may overlap freely.
- **Blocks** — what the station *is* for a stretch: which pools it plays, its own
  category balance, its limits, when it starts, what ends it, and what it hands
  over to.
- **Behaviour** — separation windows, how far back the balance is measured, and
  how much surprise the final choice is allowed.

**A channel with no plan is not a special case.** It runs the plan its existing
sources and booked slots already describe, derived on the fly, so nothing had to
be migrated and nothing changed on the day this landed. Editing that derived
plan is how it becomes yours.

### Blocks, not an hour clock

A broadcast clock is an hourly template of numbered positions. That assumes
items are interchangeable and about three and a half minutes long; this station's
run from a thirty-second ident to a six-hour episode, so an hour grid would be a
fiction the scheduler had to violate constantly — and every violation becomes a
special case. A block says what the station is right now and lets the running
order be generated.

Blocks start in one of three ways, and any combination of them:

| entry | means |
|---|---|
| `at: "07:00"`, `days: mon-fri` | a daypart on the clock |
| `at` + `hard: true` | an **appointment** — the rest of the schedule programmes around it |
| `after: "morning-news"` | starts when that block ends |
| `when: "window >= 45m"` | only if a condition holds |

**`after` is the one that matters most.** Anchor the news at 07:00, anchor the
music bridge to *after the news*, and the fresh-podcast cycle to *after the
bridge* — then moving the news to 06:30 moves the whole morning with it, with no
other edit. Under a schedule of independent slots there was nothing downstream to
move.

Exactly one block is the **default**: it has no entry condition, always accepts,
and is where everything falls back to. The plan validator refuses to save a plan
without one, and refuses blocks that hand over to each other in a loop.

### How one item gets chosen

Two questions, in order, and never mixed:

1. **What kind of programming should be happening right now?** — the timeline
   (what is booked, and how much room is left before it) and the block state
   machine answer this. Neither looks at an item.
2. **Which item satisfies that?** — the block's pools are unrolled into candidate
   items, hard constraints filter them, soft terms score what is left, and the
   final pick is weighted-random among candidates within reach of the top score.

Everything downstream of "unroll into items" is why length, creator, publication
date and how well something fills a gap can *compete* rather than only veto. The
old engine ranked sources and only asked the winner for an item — you cannot
score what you have not enumerated.

**Hard constraints**, in the order they are given up when literally nothing
qualifies (silence being worse than an imperfect choice — and every relaxation is
recorded, so a station quietly breaking its own rules is visible):

`familySeparation` → `creatorSeparation` → `sourceSeparation` → `itemSeparation`
→ `categoryRunLimit` → `airingCap` → `alreadyHeard` → `skipped` → `itemFitsRun`.
**`fitsBeforeAnchor` is never relaxed** — giving it up means starting something
that cannot finish before a booked show.

**Soft terms**, summed with configurable weights: `freshness`, `runContinuity`,
`categoryDeficit`, `windowFit`, `sourceDeficit`, `restedness`, `poolWeight`.

### New episodes are owed, not scored

A new episode is not "a candidate with a good freshness number". It is something
the station **owes** you, and it stays owed until it actually reaches you.

Each one becomes a record the moment it appears — an episode that drops at 13:37
is owed at 13:37, not tomorrow morning — carrying the **tier** of the show that
published it and an expiry (published + the source's fresh window). It comes off
the list by earning **credit**:

```
credit += how much of it played  ×  how much the block it aired in counts for
settled at 1.0
```

That one multiplication replaces a boolean that used to burn episodes. An airing
in a block worth nothing earns nothing; a five-minute preemption of a
forty-five-minute episode earns about a ninth; a full airing where exposure
counts settles it. Under the old flag, all three were "it has been on air", and
the episode was never offered again.

**Exposure is a property of the block**, not of the clock. `exposure: 0` means
airing something new here reaches nobody; `0.5` means it half counts. A block
that says nothing falls back to the **listening day** — which is the same rule
the engine used to have hard-coded, now a default that can be overridden per
block.

### Tiers order what is owed

Each source carries a tier, `S` down to `F` (`C` by default). The queue is
ordered by:

```
urgency = tierSpread × tier  +  recency  +  expiryUrgency
```

One tier step is worth more than the entire recency range, so **an S-tier show
from six hours ago goes before a B-tier one from ten minutes ago** — anything
else means the loudest publisher wins the morning. Within a tier, newest first.
Something about to stop being news climbs, because it is the last chance.

Tier and weight are **different dials**: tier orders what is owed, weight splits
a category's archive airtime. A show can be S-tier and low-weight — surface every
new episode promptly, but don't fill the afternoon with its back catalogue.

Surfacing something owed is worth a lot, but not worth breaking a rule for: if
what is owed cannot air cleanly right now, ordinary programming goes out and the
obligation comes round again shortly.

### Breaks are a unit, not an item on a clock

A block's break policy states elements with count ranges, one **elastic**
element, a target duration and an acceptable range:

```
target 8m over 2 items · accept 3–14m, 1–3 items
station-ids [0,1] first · commercials [0,2] · music [1,3] fill
```

Both the count and the duration are hard, and the planner searches the
combinations for one that satisfies both. "Play two songs" is not a
specification: two fifteen-minute songs is a half-hour break and two
thirty-second songs is a minute. **An item is never truncated to hit a
duration.** When nothing lands inside the accepted range the closest is taken
and the record says so.

**No commercials needs no code path.** An element whose pool is empty
contributes nothing and the elastic element takes up the slack — degradation is
the ordinary route through the same code.

A break plays as the unit it was planned as, and **a break never follows a
break**: its own content is not the programming being separated, and without
that the rule re-fires on the break's own last item for ever.

### Cycles

A block can carry a repeating `pattern` of wants — `obligation`, `break`,
`fill` — which is how "new podcast, short break, new podcast, short break, until
there is nothing new left" is expressed. Combined with
`exit: when obligations.pending == 0`, the cycle ends itself and hands over.

It says nothing about how long each step takes, which is the part a broadcast
clock gets wrong.

### Separation is about the person, not just the source

Two shows with the same host, back to back, is not variety — and a naive source
rule thinks it is. Every source can carry a `creator` (falling back to its label,
so only genuinely shared-host shows need it) and an optional `family`.

For music the creator is the **track's artist**, because a playlist is one row
and four hundred artists. That is also why source separation only applies to
sources that are *one show*: separating a playlist from itself would make two
songs in a row impossible, which is most of what a radio station does.

Separation is measured from when an item **ended**. A forty-minute episode that
started forty minutes ago finished a second ago, and measuring from the start
makes "keep the same host forty minutes apart" satisfiable by playing them back
to back.

### It works with whatever is there

Every separation window **shrinks to what the library can actually satisfy**.
Ninety minutes between the same artist is a good rule for four hundred artists
and an impossible one for three — at three artists and four-minute tracks the
tightest achievable spacing is about eight minutes, so a ninety-minute rule is
not a standard, it is a guarantee that the rule gets broken on every third song.

So the window becomes the smaller of what was asked for and
`(distinct values − 1) × typical item length`, with headroom: demanding the whole
cycle would force the running order, and a rotation with no freedom left is a
loop. A rich library is unaffected. A thin one quietly gets a rule it can keep.

Nothing has to be configured for this. **A music-only station with three tracks
is a legitimate station** and plays without a single relaxation being reported —
which matters, because a station that reports a compromise on every pick has a
record that means nothing.

Repeats work the same way, split in two: the **airing cap** counts how many
times a day something may air (scaled by length), and **item separation** — which
adapts — decides how soon. Two rules that both said "not yet" with different
numbers is how they drift apart.

### Category first, source second

Ranking every source against every other compares numbers that are not
comparable: with four podcasts and one playlist at 75/25, each podcast targets
18.75% and the playlist 25%. After a long talk block every individual podcast is
still further behind its own small slice than the playlist is behind its larger
one, so talk keeps winning while talk *as a whole* is hours over. That is a
fifteen-hour marathon assembled one locally-reasonable decision at a time, and no
amount of tuning the shares fixes it.

Measured in **airtime, not plays** — three minutes of music and three hours of
Joe Rogan are one play each — and by **overlap**, so a block that started before
the window still counts for the part inside it. Booked programming counts too, so
a booked hour pushes what comes after it the other way.

### Limits are the station owner's, not the engine's

A block may say **`maxUnbroken`** for a category ("no more than 90 minutes of
people talking, and it takes 15 minutes of something else to end a run") and
**`minUnbroken`** ("having started on music, do at least 20 minutes of it, or the
station alternates song, episode, song").

Both are **off unless a plan sets them**. A derived plan sets exactly these two,
because they are what the old engine hard-coded — carried over as what they
always were: this station owner's taste, written down where it can be changed.

`maxUnbroken` also bounds a single item, because there is no way out of a
six-hour episode once it has started except the skip button. That check is the
last rule the engine will ever give up.

### Appointments, and the space in front of them

Anchors are resolved over a rolling 48 hours in the channel's own zone, so
tomorrow morning's show is visible at 23:50 and a window that crosses midnight is
just a window. Wall-clock times are built as wall-clock times, not as midnight
plus a duration — on the day the clocks change those are an hour apart.

`availableWindow` (time until the next appointment) is a **hard constraint on
every candidate**. If a show starts in thirty minutes, a ninety-minute episode is
not a candidate; `windowFit` then prefers whatever fills the gap best. This is
what replaced cutting an episode off mid-sentence.

When it is due and something is on air, an appointment does one of three things
(the `start` field): **`makeNext`** waits for the item to finish — nearly always
right, because nothing that would overrun was started; **`startImmediately`**
cuts in on the minute (what derived plans use, so nothing changed silently); and
**`waitUpTo`** waits, then cuts in past a grace period.

If the gap in front of an appointment closes to less than anything the station
owns, the appointment simply **starts early**. If the tail of its own hour has no
room for another item, it **releases early**. No threshold decides either — the
actual candidate set does.

### The listening day

The station runs 24 hours. You do not. Podcasts publish overnight, so without
this the station reliably spends the only genuinely new thing it has on a dark
room at 03:00 and serves reruns to whoever wakes up at 09:17.

Each channel has a **listening day** (`dayStartMinute`/`dayEndMinute`, 08:00–23:00
by default, in the channel's own timezone). Two consequences:

- A new release is **held** until the day starts, rather than aired to nobody —
  unless it would stop being new before then, in which case airing it now beats
  never.
- An airing **outside** the day does not spend the episode's newness. It is
  still logged, so the station does not loop it all night, but at 09:17 it is
  still new to you and the new-release pass still serves it.

### Skipping moves the programming, not a cursor

Skip means *the current programming choice was rejected*. Two things happen, and
deliberately only two: the item is passed over, and the show steps aside for 20
minutes so the reply is not the next episode of what you just walked out of.
Then the **whole decision runs again from the top** — block, window, balance,
candidates.

There is no "keep the next one under 45 minutes" rule any more. A length rule
invented at the skip button is exactly the kind of specific patch that
accumulates until nobody can say why the station does anything; if the next pick
should be short, that should fall out of the model.

Airtime that actually played is kept and the unplayed remainder is not; under a
minute the play-log row is discarded entirely, so skipping costs you the next few
minutes rather than the episode.

`SKIP` steps off one show. `NEXT MEDIA TYPE` steps off the whole medium for
three hours.

### Why did it play that

Every choice writes a **decision record**: which block was on and why, what ends
it, what is booked next and how much room is left, each category's target against
what actually aired, every candidate with its per-term arithmetic, everything
that was ruled out with the rule and reason, any rule that had to be relaxed, and
the selection.

Read it in the browser (the WHY THIS PLAYED panel on the channel screen), at
`GET /api/v1/channels/{id}/why?limit=10`, or from the simulator. A channel that
has never been on air answers with what it *would* decide right now, which is the
only way to debug a silent station.

### Simulating before broadcasting

```
samo-server radio-sim --channel <id> --hours 72 [--seed 42] [--verbose]
samo-server radio-sim --channel <id> --plan draft.json --hours 48
samo-server radio-sim --channel <id> --explain 12
samo-server radio-sim --channel <id> --warmup "talk:8h"
```

Runs the **real** scheduler against a virtual clock and an in-memory play log.
It writes nothing — no play-log rows, no programme state, no decisions — so it
can be pointed at the live station safely. It reports the block timeline, whether
each booked slot went out and how close to on time, category airtime, the longest
unbroken run of each category, source and creator airtime, separation violations
and relaxations, and any moment with nothing to play.

Deterministic for a fixed seed, which is what makes it usable for comparing two
plans, and what makes it the test harness.

### Repeats

Airing something once means a 6am drop is gone before you wake up. Episodes
repeat, with a cap that scales by length so a long one cannot eat the day:

| length | airings per day |
|---|---|
| ~25 min | 3 |
| ~1 hr | 2 |
| 3 hr | 1 |

`clamp(1, floor(2h / length), 3)`, with at least **4 hours** between airings so
a repeat lands at a genuinely different time of day. For a new release the count
is of airings **inside the listening day**, so an overnight play does not use up
one of the two chances you had to actually catch it.

### What you hear

- **Fresh and rerun are different questions.** A podcast source serves a recent,
  unheard episode if it has one, and otherwise falls back to the back catalogue.
  An episode with **no publication date is never fresh** — there is nothing for
  it to be recent relative to — so it is only ever a rerun. (The age filter used
  to read `PublishedAt != nil && before(cutoff)`, which waved every undated row
  through as current: that is how something from years ago arrived labelled as
  this month's episode.)
- **Reruns have no age limit by default.** Old is frequently the point — a
  five-year-dead podcast, or a 1955 radio serial added as a feed, is something
  you added *because* it is old. Set `rerunMaxAgeDays` on a source to bound it,
  which is worth doing for daily news. How much of the day they get is decided
  by the share, not by their age.
- **Playlists shuffle.** Picking the first unplayed track walks the list top to
  bottom, so you would hear the same opener every day and never reach track 40.
- **Music plays as a set** (20 minutes by default) rather than one track, so it
  does not alternate song / episode / song.

### The two memories

`user_playback` is what **you** heard, written by your phone; channels only
read it. `channel_play_log` is what the **station** aired. Keeping them apart
is what lets a channel air something without marking it listened, and what lets
reruns advance instead of looping on one episode.

## Source kinds

### file-pool

```json
{ "paths": ["/srv/media/commercials", "/srv/media/oldies/*.mp3"] }
```

Paths can be:

- Absolute file paths
- Directory paths (scanned one level deep, hidden files skipped)
- Shell globs (`*.mp3`, `[ab]*.flac`, etc.)

The scheduler prefers files not played in the lookback window
(default 4 hours). Once everything in the pool has been played, it
falls back to the longest-since-played file.

### podcast-subscription

```json
{ "podcastId": "podcast_abc123", "maxAgeDays": 30 }
```

The channel plays the freshest episode nobody has heard yet. Two filters
apply: episodes older than `maxAgeDays` are skipped so the channel
doesn't resurface ancient back-catalog material, and so are episodes any
listener on the server has already finished (or got ~90% through).

That second filter reads real playback state. The scheduler's own
recently-played suppression only knows what THIS CHANNEL aired in the last
few hours — it has no idea what you listened to on your phone last week,
which is why channels used to happily re-air episodes you had finished. If a cached enclosure
is available (via `internal/podcastcache`), the local path is used;
otherwise the enclosure URL is streamed live.

### internet-station

```json
{ "stationId": "internet-radio_xyz789" }
```

References an existing internet radio station by id. The scheduler
resolves the station's `streamUrl` at play time so editing the
station automatically propagates to every channel using it. The item
is marked `live: true` so ffmpeg doesn't double-pace it.

**A live source picked from rotation plays for an hour**, then hands back
to the rotation. It has to be bounded by something: a stream never ends,
so without a cap the first station the rotation picked would simply become
the channel. Override per source with `playMinutes`. If a scheduled rule
starts sooner than the cap, the shorter one wins, so a station picked at
15:30 yields cleanly at 16:00 rather than being cut off mid-sentence by
the preemption watchdog.

A live source picked by a *rule* is bounded by the rule's window instead —
that is what makes "NPR from 16:00 to 17:00" mean what it says.

### live-stream

```json
{ "url": "https://npr.example.com/live.mp3" }
```

A raw URL — no catalog row. Use this when you don't want to register
the station for general use (one-off, experimental, or restricted
streams). The catalog-backed `internet-station` kind is generally
preferred.

## Schedule rules

A rule has:

- **Source** — the source to play during the window
- **Days** — bitmask (Sun=1, Mon=2, Tue=4, Wed=8, Thu=16, Fri=32,
  Sat=64). Presets in the UI: EVERY DAY (127), WEEKDAYS (62),
  WEEKENDS (65), or any single day.
- **Window** — `start_minute` and `end_minute` (0–1440 minute-of-day).
  Cross-midnight? Add two rules (one per side).
- **Priority** — higher wins when windows overlap. Default 100.
- **Enabled** — disable without deleting.

When a rule fires, the scheduler caps the picked item's
`MaxDuration` at the time remaining in the rule window. A 60-minute
podcast picked at 16:30 inside a 17:00 boundary will play for 30
minutes then yield.

### On-the-hour preemption

While a rule's window is active, the streamer re-checks the scheduler
every 15 seconds. If a higher-priority rule has just become active
mid-track, the current ffmpeg subprocess is killed and the next pick
takes over. This is what makes "NPR cuts in at 16:00" feel live
instead of "NPR starts whenever the previous song happened to end."

Rule-driven items are exempt from their own preemption check (they
won't preempt themselves), and the watchdog ignores transitions where
the new pick has the same source as the current item (avoids audible
pops on rule changes that don't actually change content).

## Data model

```
channels                 channel_sources              channel_schedule_rules
  id                       id                           id
  name                     channel_id ──┐               channel_id ──┐
  description              kind         │               source_id ─→┐│
  codec / bitrate          label        │               label       ││
  sample_rate_hz           config_json  │               weekday_mask││
  enabled                  enabled      │               start_minute││
  created_at               weight       │               end_minute  ││
  timezone                 default_rotation              priority   ││
  talk_share               role                          enabled    ││
  day_start_minute         created_at                    created_at ││
  day_end_minute           updated_at                               ││
  created_at / updated_at                                           ↓↓
channel_play_log
  id              ──ON DELETE CASCADE──┘ (when channel goes, all this goes)
  channel_id
  source_id
  item_ref           ← what the scheduler hands the streamer
  title / artist / kind
  category           ← talk or music: the balance is a question about
                       CATEGORIES and cannot be asked of a table that only
                       knows source ids. Stored rather than joined, because
                       it is a fact about the airing — re-labelling a source
                       later should not rewrite what last night sounded like.
  started_at / ended_at
  duration_seconds
```

The scheduler reads recent `item_ref` values from `channel_play_log`
to suppress repeats. File-pool items use the absolute path as their
ref; podcast subscriptions use `episode:<id>`; internet stations use
`station:<id>`; raw live streams use `stream:<url>`.

Migration: [`migrations/020_channels.sql`](../migrations/020_channels.sql).

## Streaming pipeline

```
listener HTTP GET /channels/{id}/stream?stream_token=...
      │
      ▼
api.channelStream — attach to per-channel broadcaster
      │
      ▼ first listener wakes the goroutine
channelStreamer.loop:
   for {
     item := scheduler.NextItem(channel)
     ffmpeg -i <item.url> ... -c:a libmp3lame -b:a 192k -f mp3 -
        ↓ stdout
     broadcaster.fanOut → all attached listeners
        ↑ preemption watchdog (every 15s) kills ffmpeg
          when a higher-priority rule activates
   }
      ▲ last listener leaves → streamer teardown
```

One ffmpeg subprocess per channel. Slow listeners get dropped (a
non-blocking send into a buffered channel; if it fills, the listener
is removed). The broadcaster ships live bytes only — no historical
backfill on connect.

## Loudness levelling

A channel mixes a modern pop master (around -9 LUFS), a podcast (-18)
and an archive recording (-27). Aired at their native levels those are
eighteen decibels apart, which is the whole "why is everything a
different volume" complaint.

Every item is levelled to a common target before it reaches the
encoder. The mechanism is deliberately the boring one:

1. Measure the item's integrated loudness once, offline, with
   EBU R128 / ITU-R BS.1770 (`ffmpeg -af ebur128=peak=true:framelog=quiet`,
   a pure meter that reports and never touches the audio).
2. Cache it in `loudness_measurements`, keyed on the file path plus a
   size+mtime fingerprint. Audio does not change, so one measurement
   per file is enough forever.
3. At playback, apply the single constant decibel offset that lands
   the item on target: `-af volume=N dB`.

Items longer than ten minutes are sampled from 5% in rather than read
end to end. Integrated loudness is a gated average and averages
converge, so ten minutes pins the level to a fraction of a decibel —
while reading a 23-hour audiobook in full costs half an hour and blocks
everything behind it.

Step 3 is one multiplication applied equally to every sample. **The
item's own dynamics are untouched** — the quiet parts stay exactly as
far below the loud parts as the engineer left them. This is not
`loudnorm` in its single-pass dynamic mode, and not `dynaudnorm`;
those ride the gain *within* an item, which flattens music and pumps
audibly against a talk bed.

The one exception is a true-peak limiter, and it is bounded. If
reaching the target would push an item's peaks past the ceiling, the
gain is capped at whatever the limiter is allowed to absorb
(`MaxLimitDB`, default 6 dB) and the item is left slightly under target
rather than squashed to reach it. High-crest material — orchestral,
acoustic, old dynamic recordings — therefore comes out a couple of dB
quiet instead of compressed. That is the intended trade.

Timing:

- **Warm-ahead.** When an item starts playing, the streamer peeks at
  what the scheduler will pick next and measures it during the current
  item. Analysis runs far faster than real time, so an item is
  normally levelled on its *first* airing.
- **Backfill.** A slow background sweep measures the whole library
  (one file at a time, two seconds apart) so nothing depends on having
  aired before. This is also what makes "play to samo-radio" level, since
  that queue is resolved once and cannot be corrected later.
- **Never blocking.** A cache miss plays the item at its native level.
  Nothing in a live pipeline waits on an analysis subprocess; dead air
  between items is a much worse fault than one loud track.
- **Live sources** (a BBC stream, an internet station in the rotation)
  are measured through a 45-second window, flagged partial, given a
  tighter boost ceiling, and re-measured weekly.

Configuration:

| Variable | Default | Meaning |
| --- | --- | --- |
| `SAMO_LOUDNESS_TARGET` | `-16` | Target level in LUFS, or `off` to disable levelling entirely. Accepted range -30..-8. |

-16 LUFS is the streaming/podcast convention. Broadcast EBU R128 calls
for -23, which is right for television and too quiet for a box sharing
an amplifier with everything else in the house. Note the practical
consequence: loud modern masters get turned **down**, so the station as
a whole sits a little below what its loudest material used to hit. One
setting of the volume knob then works all day.

Levels and decisions are logged per item, e.g.
`channel abc: "Track" -9.4 LUFS peak -0.2 dBTP → -6.6 dB`.

## API

Admin (requires admin role):

| Method | Path | Notes |
|---|---|---|
| `GET`   | `/api/v1/channels` | All users can list |
| `POST`  | `/api/v1/channels` | Create channel |
| `GET`   | `/api/v1/channels/{id}` | Hydrated with sources + rules |
| `PATCH` | `/api/v1/channels/{id}` | Restarts streamer on codec change |
| `DELETE`| `/api/v1/channels/{id}` | Cascades sources/rules/log |
| `GET`   | `/api/v1/channels/{id}/sources` | |
| `POST`  | `/api/v1/channels/{id}/sources` | |
| `PATCH` | `/api/v1/channels/{id}/sources/{sourceId}` | |
| `DELETE`| `/api/v1/channels/{id}/sources/{sourceId}` | |
| `GET`   | `/api/v1/channels/{id}/schedule` | |
| `POST`  | `/api/v1/channels/{id}/schedule` | |
| `DELETE`| `/api/v1/channels/{id}/schedule/{ruleId}` | |
| `POST`  | `/api/v1/channels/{id}/preview` | Run scheduler once without ffmpeg |
| `PUT`   | `/api/v1/channels/{id}/plan` | Validate and store the programming plan |
| `DELETE`| `/api/v1/channels/{id}/plan` | Drop it, back to the derived plan |

The plan endpoints are the station-building surface; the PROGRAMME screen is a
client of them, and `PUT` is the only place a plan is validated. It rejects a
document with every problem listed at once rather than the first one found —
unknown pools, blocks that hand over in a loop, no default block to fall back
to, times and durations that are not.

Read (any authenticated user):

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/v1/channels/{id}/plan` | The stored plan, or the derived one, with `custom` |
| `GET` | `/api/v1/channels/{id}/why?limit=N` | Decision records, newest first |
| `GET` | `/api/v1/channels/{id}/obligations` | What the station owes you, most urgent first |
| `GET` | `/api/v1/channels/{id}/schedule/status` | Clock, booked slots, and current programming |

Stream (any authenticated user; `?stream_token=` supported for
browser `<audio>` tags):

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/v1/channels/{id}/now` | Current item + listener count + recent |
| `GET` | `/api/v1/channels/{id}/recent?limit=N` | Play log |
| `GET` | `/channels/{id}/playlist.m3u` | M3U pointing at the stream |
| `GET` | `/channels/{id}/stream` | The audio bytes (one long pipe) |

## Example: "personal NPR drive time"

You want NPR's All Things Considered at 4–5pm on weekdays, your
favourite podcasts in rotation otherwise, and 2000s Twin Cities
commercials as filler.

1. Add an internet radio station for NPR's MP3 stream
   (`/app#radio` → INTERNET → + NEW STATION). Note its id.
2. Add a podcast feed for each podcast you like
   (`/app#podcasts` → + NEW PODCAST). Wait for the feed to poll.
3. Drop your commercials into `/srv/media/commercials`.
4. Create a channel "Drive Home".
5. Add sources:
   - **file-pool** "Commercials" pointing at `/srv/media/commercials`
     (rotation: ON, weight 1)
   - **podcast-subscription** for each podcast (rotation: ON, weight 3)
   - **internet-station** "NPR Live" picking the NPR station
     (rotation: OFF — only fires during its scheduled window)
6. Add schedule rule:
   - source: NPR Live
   - days: WEEKDAYS
   - 16:00 → 17:00
   - priority: 200
7. Click TUNE IN. The channel plays podcasts + commercials all day,
   then at 16:00 the preemption watchdog notices the rule fired and
   cuts to NPR. At 17:00 ATC's window closes and rotation resumes.

## Implementation notes

- **Package**: `internal/channels`. Owns types, store, scheduler,
  streamer, service. API handlers live in `internal/api/channel_handlers.go`.
- **No god types**: `Channel`, `Source`, `ScheduleRule`, `PlaybackItem`,
  `NowPlaying`, `PlayLogEntry` are all narrow. Source kinds are strings
  with constants in `types.go`; new kinds are added by extending the
  resolver switch in `scheduler.go`.
- **Dependency injection**: `Dependencies` bundles the catalog/cache/
  internet-station readers as interfaces. Nil readers degrade
  gracefully (the relevant source kind just fails to resolve and the
  scheduler moves on).
- **Timestamps**: `parseStoredTime` accepts both RFC3339 and the
  SQLite `CURRENT_TIMESTAMP` format so legacy rows survive.
- **Tests**: `scheduler_test.go` covers rule priority + weekday +
  window matching, recently-played suppression, podcast freshness,
  internet-station resolution, rule-vs-rotation precedence, and rule
  tagging (so the preemption watchdog can trust `IsRuleDriven` /
  `RuleID`).

## Future work

- **On-the-clock alignment** for live cut-ins (start exactly at
  16:00:00 rather than within 15s)
- **Bumper/transition** support — short audio between rule changes
- **HLS output** for clients that prefer it over raw MP3 over HTTP
- **Per-source dayparting** — finer-grained weight schedules without
  needing a full rule
- **Channel sharing / public flag** — drop the auth requirement so
  channels can be shared with friends as personal radio stations
- **Listener history view** — UI to browse the play log across days
