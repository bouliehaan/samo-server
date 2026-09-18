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

`heldForTheListeningDay` → `stationAired` → `familySeparation` →
`creatorSeparation` → `sourceSeparation` → `itemSeparation` →
`categoryRunLimit` → `airingCap` → `longFormRationing` → `alreadyHeard` →
`skipped` → `itemFitsRun`.
**`fitsBeforeAnchor` is never relaxed** — giving it up means starting something
that cannot finish before a booked show.

`stationAired` — the station's own record of having aired something in full —
is given up almost first, because what giving it up produces is a **rerun**,
and a rerun is ordinary radio. Everything below it produces something a
listener would call a fault, and a station that has run through its unaired
catalogue should repeat last week's episode before it plays the same host
twice in an hour or a six-hour epic two days running. `alreadyHeard` — a
*person* having listened — is a different witness and sits far lower.

The ladder runs over the **whole shelf** the block can reach, once, and every
rule is fitted to that shelf before it is applied. A position that asks for
something owed does not get its own, gentler ladder: an owed episode is airable
when it is among what that one pass lets through, at whatever rung the whole
shelf needed. When the shelf has something that plays cleanly, an owed episode
held by a spacing rule stays held and ordinary programming goes out — see
*Never heard first, then tiers order what is owed* for why that is the point rather than a compromise.

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

The owed list is an **order**, not a running order. Every decision filters
candidates through the hard rules before it scores them, so the most urgent
thing owed is not the next thing played while a rule holds it back — an
episode that aired at lunch and is owed a second hearing may be free of every
other rule and still have hours of item separation to run. `GET
/channels/{id}/obligations` says which: a pending item the rules would not offer
at this moment carries `held: { rule, reason }`, worded as the decision record
words a rejection, from the same rules asked without deciding anything
(`Engine.JudgeOwed`). Anything that shows the queue as "coming up" — the wall
does — puts the held items after the free ones.

### Never heard first, then tiers order what is owed

Each source carries a tier, `S` down to `F` (`C` by default). The queue is
ordered by:

```
urgency = unheardLift (if nobody has heard it)  +  tierSpread × tier  +  recency  +  expiryUrgency
```

**An episode nobody has heard goes before any episode somebody has, across
every tier.** A brand-new A-tier episode goes before the second surfacing of an
S-tier one; everything owed a second hearing waits until nothing unheard can
air. This is an order and not a weight: `unheardLift` is computed from the
policy's own weights as more than every tier step, the whole recency range and
the whole expiry lift put together, plus a tier step of margin, so no
combination of tier, age and deadline carries a heard episode past an unheard
one — under the default weights or under a plan that has stretched them. The
record shows the two classes as `heard: true` on the owed list; compare
urgencies within a class, not across.

Within a class, one tier step is worth more than the entire recency range, so
**an S-tier show from six hours ago goes before a B-tier one from ten minutes
ago** — anything else means the loudest publisher wins the morning. Within a
tier, newest first. Something about to stop being news climbs, because it is
the last chance.

**"Heard" is more than half of the episode reaching the listener** —
`heardThreshold`, 0.5 of credit, compared strictly. It is drawn on credit
rather than on minutes because credit is already the station's one definition
of "reached you": the fraction that played times what the block it aired in is
worth. So the 52 of 87 minutes of Comedy Bang Bang that went out on 2026-08-11
(about 0.6) counts as heard and the episode comes round again as a second
surfacing; the seven fifteen-second false starts of a Theo Von episode that
morning (a few thousandths each) do not, and it stays a first surfacing; a
full airing at three in the morning into a block worth nothing earns nothing
and is not heard however long it ran; exactly half is not "most of it".

This replaced a queue that ordered by tier alone. On 2026-09-16 and 17 the
records showed an S-tier episode the station had aired in full that morning —
Matt and Shane's Ep 636, credit 1 of 2 — going out at 19:03 over two B-tier
episodes nobody had heard, Ghost Brothers (A, credit 1 of 2) over the same two
at 20:21, and 205: Superstar (A, credit 1 of 2) over three of them at 12:15
the next day, each as "highest scoring candidate". Urgency had no term for
never having been heard, so on a two-surfacing plan every heard-once S and A
episode sat above every unheard B, and a listener who had heard the morning's
episodes was given them again before anything new.

Tier and weight are **different dials**: tier orders what is owed, weight splits
a category's archive airtime. A show can be S-tier and low-weight — surface every
new episode promptly, but don't fill the afternoon with its back catalogue.

Surfacing something owed is worth a lot, but not worth breaking a rule for: if
what is owed cannot air cleanly right now, ordinary programming goes out and the
obligation comes round again shortly.

That sentence was true, then quietly stopped being true, and the difference was
The Church of What's Happening Now — A tier, on a two-surfacing plan, airing
every new episode and never once heard. The obligation position had been given
its own relaxation ladder, run over the owed set alone; once every owed episode
had aired that morning, "whatever it takes" was item separation and the airing
cap, and every S-tier and A-tier episode went out a second time three hours
after the first, with seven rules given up. By the evening they were back
catalogue. A second surfacing is only worth anything **somewhere else in the
day**, so the rules that put it there — item separation scaled by credit, and
the airing cap — are never bent for it. The cap does honour the plan, though:
an episode owed two surfacings may air twice in a listening day whatever its
length, once the separation has run.

Among owed episodes the **most urgent** plays — of the category the balance
chose, whatever the score band says. The band (`selection.epsilon`) is for
choosing between interchangeable records; the queue's order does not depend on
the score, so a never-heard episode that the commitment cost or the plan's
weights put well below the top scorer still goes before a second surfacing,
and a plan that asks for no randomness at all (`epsilon: 0`) still gets the
queue's order. Which category plays remains the balance's decision: the queue
settles which spoken item goes out once spoken word has won the position, and
never reaches into another category. Length has no vote here either: a new
three-hour episode of a B-tier show is not a "rested giant" that gets the
floor, it is a B-tier episode, and the S-tier one from two hours ago goes
first — unless somebody has heard that one, in which case the B-tier episode
is the one nobody has had, and it goes first. The record says which decided:
`most urgent of what is owed` is the queue, `highest scoring candidate` is the
score, and the candidate marked as the contender is the one the choice was
actually made between, which under the queue is not always the top scorer.

**Two witnesses, and they are not interchangeable.** The station writes its own
airings into the playback table under the reserved server account, so an
episode it has already been through does not come back as a rerun. That row is
the radio's memory, not a person's: it says the episode went out, to whoever
was in the room, and *whether anybody was* is what credit measures. So the
station's own record only ever keeps **back catalogue** off the air — something
still owed a surfacing is, by the station's own reckoning, not yet heard, and
the station cannot retire it on its own say-so. A **person's** playback row is
the other witness, and it is absolute: an episode somebody here has listened to
is never offered again, and the obligation for it is settled at the next
decision, because it reached them — by another route, but it reached them.

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
`exit: when obligations.ready == 0`, the cycle ends itself and hands over.

`obligations.pending` is everything owed; `obligations.ready` is the part of
it that could go out right now without a rule being bent. They differ by
exactly what a new-episodes block should not sit waiting on — a second
surfacing with hours of separation still to run, an episode too long for the
room before the next booked show, one held for the listening day — and a block
gated on `pending` never hands over while any of those exist, which on a
two-surfacing plan is the whole afternoon. Gate the entry and the exit on
`ready` and the block comes on when there is something to give it and steps
aside when there is not. A break position at the top of a pattern is passed
over when a break is what just played, across a handover included.

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

The record is kept for a week per channel (with a ceiling of a few thousand rows
as a backstop against a runaway writer). The same choice made again within five
minutes — same block, same selection, or the same failure to select — is folded
into the row it repeats rather than written as a new one: the row moves to the
front, carries the latest account, and gains `retries: {count, since}`. That is
what a source producing no audio looks like: the streamer discards the play-log
row, backs off and asks again, and the scheduler answers the same. Recorded row
by row, an unreachable booked station once wrote three hundred and fifty
identical decisions in three hours and pruned five days of history to make room
for them; now it is one row that counts the retries and says when they started,
which is both smaller and more informative. The panel shows the run in red.

### Simulating before broadcasting

```
samo-server radio-sim --channel <id> --hours 72 [--seed 42] [--verbose]
samo-server radio-sim --channel <id> --plan draft.json --hours 48
samo-server radio-sim --channel <id> --explain 12
samo-server radio-sim --channel <id> --warmup "talk:8h"
```

Runs the **real** scheduler against a virtual clock and an in-memory play log.
It writes nothing — no play-log rows, no programme state, no decisions — so it
can be pointed at the live station safely. The station's own playback ledger
is layered the same way: the simulated station's airings are recorded in
memory over whatever the real table says, so an episode the run has aired in
full is retired exactly as the live station retires it. Without that the
simulator never exercised the already-heard rule at all, and a station whose
second surfacing was being cancelled by its own first airing looked healthy
for three simulated weeks. It reports the block timeline, whether
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

`clamp(1, floor(2h / length), 3)` — or the number of surfacings the plan asks
for, whichever is larger, for something still owed: the budget is there so
back-catalogue repeats cannot eat the day, and "surface this show's new
episodes twice" is a decision the owner made about exactly these items. How
*soon* a repeat may land is item separation's job — `separation.item` in the
plan, **8 hours** unless the plan says otherwise, so a repeat lands at a
genuinely different time of day. For something still owed, that window is
scaled by the credit it has earned: an airing nobody heard is not a time you
heard it, and holds it back for nothing. For a new release the count is of
airings **inside the listening day**, so an overnight play does not use up one
of the two chances you had to actually catch it.

Only a **clean end** counts as the whole item having gone out. A skip, a booked
show cutting in, the play window closing, the last listener leaving — each cuts
the item, and a cut item earns credit for the part that played and nothing
more. Reading a cut as a finish is what once settled a three-hour episode on
nineteen minutes of it.

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

`user_playback` is what **you** heard, written by your phone. `channel_play_log`
is what the **station** aired. Keeping them apart is what lets a channel air
something without marking it listened, and what lets reruns advance instead of
looping on one episode.

The station does keep one row of its own in `user_playback`, under the reserved
`user-server` account, for every episode it has aired in full. It is read back
*apart* from everybody else's rows — it retires back catalogue, and it never
stands in for a person having heard something the station still owes.

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

### On-the-hour cut-ins

A booked slot's start is known in advance, so the handover is timed rather
than noticed. Eight seconds out, the slot's station is dialled and decoded
into a ring whose contents are thrown away; three seconds out, the crossfade
begins — what is playing goes down as the station comes up, over the same
three seconds — and on the second the item on air is cancelled and the
decision at the boundary adopts the station already on air. "NPR at 16:00"
starts at 16:00:00 at full level, with the end of the previous item under it
rather than chopped off. A slot with no live station to warm (a folder of
files, a station that did not answer in time) gets the item on air faded to
silence by the boundary and whatever the decision picks fading in after it.

A slow ticker still re-asks the scheduler every 15 seconds, as a backstop for
a plan that changed mid-item; a cut it catches goes out under the ordinary
two-second fade. Rule-driven items are exempt from their own check (they would
otherwise preempt themselves every tick), and a transition to the same source
is ignored.

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
     ffmpeg -re -i <item.url> [-af volume=…] -f s16le -   → PCM ring
   }
mixer (every 20ms): gain-shape the item on air, sum with the one on its
   way out, one frame → ffmpeg -f s16le -i - -c:a libmp3lame -f mp3 -
        ↓ stdout
     broadcaster.fanOut → all attached listeners
      ▲ last listener leaves → streamer teardown
```

Each item is **decoded**, not transcoded: its own ffmpeg turns it into raw PCM
in a bounded ring, and a mixer holds the clock. Every twenty milliseconds it
takes one frame from whatever is on air, runs it through a gain envelope, sums
it with whatever is on its way out, and hands the frame to one long-lived
encoder. That is what makes fades possible at all — by the time audio is MP3
there is no level left to move — and it is why the output is one continuous
encoded stream rather than encoded items laid end to end, with a header at
every join and a frame torn in half at every cut.

- An item **fades in** (1.2s after a clean end, 0.6s after a cut), **fades
  out** into a play window it will not outlive (2s, or the 3s a boundary fill
  asks for), and a booked show **crossfades** in over 3s, landing at full
  level on its second. A skip goes out under a 300ms fade: the cut is meant to
  be heard, this only keeps it from being a click.
- The mixer **paces** the output at real time whatever the input does. A file
  is paced by `-re` and blocked on its ring; a live station is never blocked —
  the far end would drop a client that stopped reading — so its connect burst
  fills its ring and goes out at real time, and past four seconds the oldest
  audio is dropped. Nothing reaches a listener faster than real time.
- With nothing on air the encoder is fed silence, so a client sees a stream
  that has gone quiet rather than one whose bytes stopped.

One decoder per item on air (two during a crossfade), one encoder per channel.
Slow listeners get dropped (a non-blocking send into a buffered channel; if it
fills, the listener is removed). The broadcaster ships live bytes only — no
historical backfill on connect.

A play-log row left open — by a crash, or by a write that failed at shutdown —
reads as "still playing" to every rule that consults the log, so any row open
when a streamer starts, or when the server starts, is closed where its own
length says it ended. The same housekeeping prunes settled obligations older
than a month; the decision only ever reads what is pending plus a week of
what settled.

A decision that is only being *asked about* — the loudness warm-up, the
cut-in warm-up, a preview — writes nothing: it reads the obligation table as
it stands rather than noticing or settling anything. The preemption backstop
does not decide at all; it asks the timeline whether a slot that cuts in is
on air.

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
| `GET` | `/api/v1/channels/{id}/obligations` | What the station owes you, most urgent first; a pending item the rules would not offer right now carries `held: {rule, reason}` |
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
  streamer, service. The audio stage is `mixer.go` (the ring, the envelopes,
  the clock) and `streamer_audio.go` (decoders, the encoder, warming). API
  handlers live in `internal/api/channel_handlers.go`.
- **Booked slots in a stored plan** are rebuilt from their schedule rules on
  every load: the window, days and pool follow the rule; the start policy,
  grace, exposure, breaks, limits and long-form policy are the plan's and
  survive. The status endpoint reports a booked show no slot names as
  unreachable, since a match pool never sweeps a show in.
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

- **Bumper/transition** support — short audio between rule changes
- **HLS output** for clients that prefer it over raw MP3 over HTTP
- **Per-source dayparting** — finer-grained weight schedules without
  needing a full rule
- **Channel sharing / public flag** — drop the auth requirement so
  channels can be shared with friends as personal radio stations
- **Listener history view** — UI to browse the play log across days
