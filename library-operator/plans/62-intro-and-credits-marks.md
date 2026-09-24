# 62, Intro and credits marks

This plan joins two stubs: plan 62 from a 2026-09-14 conversation,
which recorded the problem, and plan 65 from a 2026-09-22 conversation,
which named the sources. The design is from a 2026-09-24 conversation.
The first version is built in this repository and in
[`media-operator`](https://github.com/liken-sh/media-operator) on
2026-09-24, and it is not yet drilled on a cluster.

## The problem

Nothing in the catalog says where a work's story starts or where it
ends. A video file has a runtime and little else. Every part of
`liken` that needs those two points guesses them from the runtime.

**A person must skip the recap and title sequence of every episode by
hand.** An episode starts with a recap of earlier episodes, then a
title sequence, and then the story. A person who watches a season skips
the same two segments in every episode. Nothing tells the player where
the story starts, so nothing can offer the skip.

**The start of the credits is estimated from a percentage.** The
up-next card appears when three percent of the runtime or three minutes
remain, whichever is less. A work counts as watched when the time that
remains is both a twentieth of the runtime or less and five minutes or
less. Both numbers estimate where the credits start. Credits do not
scale with the runtime. A feature's credits run about five to ten
minutes whatever its length, and an episode's credits run about thirty
to sixty seconds. A percentage is therefore too late for a long film
and too early for a short one.

The two problems need the same kind of fact: a point in the file that
comes from the work itself, where today the point is calculated from the
runtime. This operator stores the fact, and the player in
`media-operator` acts on it, so the plan covers both repositories.

No person will enter marks by hand for every episode of every series.
Two kinds of source can supply them without that work: a community
database, and the media itself. The first version reads the community
databases. Detection in the media is a later step, described under
[Later](#later).

## The design

### The sources

Two community databases publish the spans, and the `marks` fact reads
both. Each is a `MetadataProvider` block in `spec.providers`, the same
way TMDb, OMDb, and TVmaze are.

**TheIntroDB**, block `theintrodb`. The fact calls
`GET https://api.theintrodb.org/v3/media` with `tmdb_id`, plus
`season` and `episode` for an episode. TheIntroDB also accepts IMDb and
TVDb ids, but its documentation names the TMDb id as the canonical key
and says the other ids go through a mapping that can be wrong. So a
title with no TMDb id gets no call. The fact also sends the probe's
runtime as `duration_ms`, and TheIntroDB uses it to select the release
version whose spans fit the file, such as a theatrical cut or an
extended one. The answer has four lists: `intro`, `recap`, `credits`,
and `preview`. Each entry is a `start_ms` and an `end_ms`. A null start
is the start of the file, and a null end is the end of the file. A 404
means TheIntroDB has no submission for the work.

A key is optional, and it is a bearer token in a `Secret`. Without a
key, the limit is 500 `/media` calls a day for each IP address. With a
key, the limit is 1000 a day for the account, and the answer also
includes the account's own pending submissions. Both have a rate limit
of 30 calls in 10 seconds. The provider check calls `/health`, which
spends none of the daily allowance.

**IntroDB**, block `introdb`. The fact calls
`GET https://api.introdb.app/segments` with `imdb_id`, plus `season`
and `episode` for an episode or `is_movie=true` for a film. IntroDB
takes no key. Its answer has one span or null for each of `intro`,
`recap`, `outro`, and `post_credits`. IntroDB's documentation says
`outro` is the end credits, so the fact stores it as `credits`, and it
stores `post_credits` as the kind `post-credits`. The documented query
on IntroDB's home page, `?imdb=`, answers "Invalid query params". The
parameters above come from test calls on 2026-09-24.

**The two databases give several answers for one kind.** On
2026-09-24, TheIntroDB returned three intro spans for Game of Thrones
S01E02: 0 to 107.0 seconds, 7.0 to 106.5 seconds, and 8.0 to
109.0 seconds. IntroDB returned 6.0 to 105.0 seconds for the same
episode. The spans come from different submissions and different
release versions, and a submitted span can be wrong. The fact stores
every span exactly as each database returns it, with its source, and
the readers merge them. A reader that merges its own copy can change
its rule later without a new lookup.

### The fact

`marks` is one more fact in the enricher, with the same vocabulary as
the others: a name in `spec.facts` and `spec.refresh`, its own
container, its own `.liken/` file, a miss with a date, and the retry
window.

- **The gap** is the main video of each identified movie or episode,
  after the probe has measured its runtime. The runtime is part of the
  TheIntroDB query, so the probe runs first.
- **The container** runs after the trailer container.
- **The ledger** is `.liken/marks.yaml` in the file's folder. It holds
  every span, keyed on the file's path, with its kind, start, end, and
  source, and one attempt per file with its date and result. The start
  and end are in milliseconds, and each is absent where the database
  returned null.
- **A miss** clears the file's spans. An error keeps them, so a
  database that is down does not delete marks that were already found.
- **A file that holds two episodes** records a miss without a lookup,
  because the first episode's spans would give wrong times for the
  file.

The catalog has a `marks` table with one row per span:
library, path, ordinal, kind, `start_ms`, `end_ms`, and source.
`start_ms` and `end_ms` accept null. The walk reads the ledger into
rows, and the rows are deleted with their file and swept with their
library.

**No `.nfo` convention exists for these spans.** Kodi's and Jellyfin's
`.nfo` formats have no element for an intro or credits segment.
Jellyfin stores its media segments in its own database. The only file
convention is Kodi's `.edl` sidecar, whose action codes (cut, mute,
scene marker, and commercial break) have no word for intro or credits.
So the fact writes no `.nfo` and no `.edl`.

**Rate limits.** The provider HTTP code that every provider uses reads
`Retry-After`, and it also reads TheIntroDB's `X-RateLimit-*` and
`X-UsageLimit-*` headers. A request whose cooldown is a minute or less
waits and goes out again. A request whose cooldown is longer fails at
once with the provider's answer, and the fact asks again on a later
run. When a provider has spent its daily allowance, the run stops
asking it. At 1000 calls a day, a first pass over a library of several
thousand episodes takes several days, and the retry window spreads the
load.

### The Play

The marks go to the player on the Play, in each item's presentation.
`media-operator`'s `Presentation` has a `marks` list:

```json
"marks": [
  {"kind": "intro", "end": 107.0, "source": "theintrodb"},
  {"kind": "intro", "start": 7.007, "end": 106.482, "source": "theintrodb"},
  {"kind": "credits", "start": 3253.0, "end": 3316.0, "source": "theintrodb"}
]
```

The times are in seconds. An absent start or end is the edge of the
file. The media browser reads the file's rows from the catalog and
sends every span in ledger order, for the Play it starts and for
up-next's next Play. The display ignores a kind it does not know, so a
library can send a new kind before the display acts on it.

### The player

The display merges the candidates of one kind before it acts on them.
It groups the candidates whose spans overlap. Each group becomes one
span, from the median start and the median end of its members. A
median moves less than an average when one submission is a few seconds
off. Groups that do not overlap stay apart, because a film can have two
credits spans: the main credits, then a scene, then more credits.

**Skip.** While the playhead is inside a merged intro or recap span,
the display shows a "Skip intro" or "Skip recap" control on the
up-next chip's row, at the left margin. The control is a focus stop.
The press that wakes the display focuses it, and a select seeks to the
end of the span, so a skip takes two presses. The control stays up for
the whole span, and it never skips by itself. In a local run with three
intro candidates, the merged span was 5.0 to 61 seconds, and the skip
landed at 61.0 seconds.

**Skip to the post-credits scene.** A scene after the credits is
marked two ways. IntroDB returns a `post-credits` span. TheIntroDB
returns two credits spans with the scene in the gap between them. While
the playhead is inside the credits and a scene follows, the display
offers "Skip to post-credits scene" with the same control. The target
is the start of the first `post-credits` span after the playhead. With
no such span, the target is the end of the first of two or more credits
spans. A film with credits and no scene offers no control. The card
and the control are up together in the credits, and a wake focuses the
control, because a skip is a seek that a seek back undoes, and the
offer ends the run. In a local run with TheIntroDB's form, two credits
candidates merged to 120.25 to 170.1 seconds, and the skip landed at
170.1 seconds.

**Up next.** The card rises at the start of the earliest merged
credits span that starts in the second half of the runtime. A credits
span in the first half is an opening title sequence, and it moves
nothing. With no such span, the card rises by the time that remains, as
before. In the local run, a 200-second test file with credits at
150 seconds raised the card at 2:31. The time rule would have raised it
at 3:14.

### Watched

The jellyfin role writes the watched state to a Jellyfin server. It
reads the credits spans of the Play's first item from the play
audience message, which the operator fills from the Play. It merges
them the same way the display does, and a work counts as watched once
the position reaches the earliest merged credits start in the second
half. With no such span, the time rule applies.

The media browser's own watched rule, `finished` in
`media-browser/src/catalog/progress.rs`, does not read marks. The
browser's watched state comes from the progress store alone.

## Later

**Detection in the media.** A season's episodes share a title
sequence. The audio of that sequence repeats across the episodes, so a
fingerprint of each episode's audio finds the span they share. This is
the method `intro-skipper` uses for Jellyfin: chromaprint fingerprints,
the same family of fingerprint that AcoustID publishes, compared across
the episodes of one series. A black frame or a title card marks a
boundary the same way, and the trickplay base already decodes with
ffmpeg. A detection fact would run as its own Job on the ffmpeg image,
one pass per series, only for a season that no community span covers.
Most of the cost is the decode of each episode, and the result depends
on the rip. A cold open or a recap can move the span.

**Which source takes precedence.** A person's own mark would outrank a
community mark, and a community mark would outrank a detected one. The
`source` column records the rank. A detected mark would carry a
confidence value, so a screen could ask a person to confirm it before
the player offers the skip. How a person reviews a detected mark is
`media-operator`'s decision.

**A person's own marks, and submissions.** TheIntroDB accepts
submissions through `POST /submit` with an account key. A screen that
lets a person correct a span could send the correction back.

## What is not decided

- Whether to write an `.edl` beside the media for Kodi. It would carry
  only a start, an end, and an action code, and it would drop the kind.
- Whether the up-next offer should do anything different when a
  post-credits scene is still ahead. A person who takes the offer in
  the first credits span does not see the scene, and nothing on the
  screen says a scene follows. One option adds a line to the card.
  Another raises the card at the start of the last credits span,
  after the scene.
- The databases' terms of use. Neither database states terms for its
  read API.

## How the work is tested

Table tests over a fake server test each provider: the lookup by each
id, the runtime in the query, a 404 as a miss, the miss with its date,
the retry window, and the rate-limit headers. Table tests test the
ledger, the rows, the delete and the sweep, and the presentation the
browser builds. In `media-operator`, table tests over the merge test
overlapping candidates, a null start, a null end, two credits spans, an
unknown kind, and a credits span in the first half. The watched rule's
tests use the same cases.

A headless local run of the display, with a generated test video,
proved the skip and the credits card. The drill on `liken-1` is not
done: it plays an episode that both databases have and a film with a
post-credits scene, and a person checks the skip, the card, and the
watched state in Jellyfin.

## Sources

- [TheIntroDB](https://theintrodb.org), a community database of intro,
  recap, credits, and preview timestamps. Its [OpenAPI
  spec](https://theintrodb.org/openapi.yaml), API version 3, read on
  2026-09-24, documents `/media`, `duration_ms`, the rate limits, and
  the daily allowances. Its [Jellyfin
  plugin](https://github.com/TheIntroDB/jellyfin-plugin) matches a
  title by TMDb id, falls back to IMDb and TVDb, and fills Jellyfin's
  media segments.
- [IntroDB](https://introdb.app), a community database of intro, outro,
  and post-credit timestamps with an anonymous API.
- [Jellyfin media segments](https://jellyfin.org/docs/general/server/metadata/media-segments/),
  the segment types Jellyfin stores in its own database, with no file
  format.
- [jellyfin-plugin-edl](https://github.com/endrl/jellyfin-plugin-edl),
  which exports Jellyfin's segments to Kodi `.edl` files.
- [ChapterDB](https://chapterdb.plex.tv), a community database of film
  chapter titles.
- [intro-skipper](https://github.com/intro-skipper/intro-skipper), the
  Jellyfin plugin that fingerprints the audio of the episodes in a
  season with chromaprint and compares them for the repeated intro.
- [Chromaprint](https://acoustid.org/chromaprint), the fingerprint
  library behind AcoustID.
