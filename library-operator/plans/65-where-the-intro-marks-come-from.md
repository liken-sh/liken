# 65, Where the intro marks come from

A stub from a 2026-09-22 conversation. [Plan
62](62-intro-and-credits-marks.md) records the missing fact: nothing in
the catalog says where a work's story starts or where it ends. This
plan names where that fact can come from. A community database can hold
the marks for a work, and when no database holds one, this operator can
find the marks in the media itself. Nothing here is built, and this
plan proposes no design.

## The problem

Plan 62 leaves open where a mark comes from. A person can supply one,
but no person will hand-enter every episode of every series. Two kinds
of source can fill the gap without that work.

**A community database.** TheIntroDB and IntroDB publish intro, recap,
credits, and preview segments for television and film. A lookup
uses the provider ids that the catalog already holds from its identity
work.
TheIntroDB serves a free API at `api.theintrodb.org`, takes TMDb, IMDb,
or TVDb ids, and uses an optional API key to reach a person's own
pending submissions. IntroDB serves an anonymous API. A segment is a
kind, a start, and an end in the file's time.

**The media itself.** A season's episodes share a title sequence. The
audio of that sequence repeats across the episodes, so a fingerprint of
each episode's audio finds the span they share. This is the method
`intro-skipper` uses for Jellyfin: chromaprint fingerprints, the same
family of fingerprint that AcoustID publishes, compared across the
episodes of one series. A black frame or a title card marks a boundary
the same way, and the trickplay base already decodes with ffmpeg.

Neither source is complete. The community databases hold some works and
no others, and a submitted segment can be wrong. A detected segment
depends on the rip, and a cold open or a recap can move the span. The
fact records which source a mark came from, so the weak marks stay
visible.

## The shape, not yet decided

**The fact.** The marks become a fact in this operator, beside the art,
subtitle, and rating facts. The player in
[`media-operator`](https://github.com/liken-sh/media-operator) reads
the marks from the catalog and offers the skip, which is plan 62's
other half. A community database is a `MetadataProvider` block in
`spec.providers`, keyed the way the other providers are, with an
optional `Secret` for the API key. The fact asks by the title's
provider id, writes the segments it finds, and records a miss with a
date for a title no database holds, so the retry window applies. The
catalog holds one row per mark per file: the kind, the start, the end,
and the source.

**Detection as the fallback.** When no database holds a season, a
detection fact reads the season's episodes, fingerprints a low-rate
mono decode of each, and writes the span that repeats across most of
them. It runs as its own Job on the ffmpeg image, one pass per series,
the way trickplay runs on a decode. The decode is the cost, so the fact
runs only for a season no community mark covers and no pass has already
marked.

**Which source wins.** A person's own mark outranks a community mark,
and a community mark outranks a detected one. The rule is a `source`
column on the mark row, and the fact never overwrites a mark from a
stronger source. A detected mark carries its confidence, so a screen
can ask a person to confirm it before the player offers the skip.

## What is not decided

Whether detection is in the first version at all, because it costs a
decode per episode and depends on the rip. Whether the marks live in
the `.nfo` beside the media, in a `.liken/` file, or only in the
catalog, which plan 62 left open. The format decides whether Jellyfin
and Kodi read the marks too. The community databases' terms, their rate
limits, and whether a full answer needs an account. How a person
reviews a detected mark, which is `media-operator`'s call.

## How the work is proved

Table tests over a fake segment server prove the lookup by each id
kind, the miss with its date, the retry window, and the precedence
rule. A table of fingerprint pairs proves the detected span over a
shared title sequence, a cold open, a recap, and an episode with no
match. The drill runs on `liken-1` against a season with a known title
sequence and a season no database holds: the community fact writes the
marks for the first, the detection fact writes the span for the second,
and a person reads the skip the player offers.

## Sources

- [TheIntroDB](https://theintrodb.org), a community database of intro,
  recap, credits, and preview timestamps with a free API at
  `api.theintrodb.org`. Its [Jellyfin
  plugin](https://github.com/TheIntroDB/jellyfin-plugin) matches a
  title by TMDb id, falls back to IMDb and TVDb, and fills Jellyfin's
  media segments.
- [IntroDB](https://introdb.app), a community database of intro, outro,
  and post-credit timestamps with an anonymous API.
- [ChapterDB](https://chapterdb.plex.tv), a community database of film
  chapter titles.
- [intro-skipper](https://github.com/intro-skipper/intro-skipper), the
  Jellyfin plugin that fingerprints the audio of the episodes in a
  season with chromaprint and compares them for the repeated intro.
- [Chromaprint](https://acoustid.org/chromaprint), the fingerprint
  library behind AcoustID.
