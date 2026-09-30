# 74, A file replaced at the same path

Built on 2026-09-29. Everything below is built, and no drill on a
cluster has run yet.

Revised on 2026-09-29, in review before the build. A replaced file
opens `trickplay` and `episode-thumb` even where their outputs are still
on the volume, and the phases replace tiles and a thumbnail made from
the earlier file. The first draft kept those outputs. "An output made
from the earlier file" below states the rule, and the section on
rendering new tiles under "Considered and set aside" records the draft's
reasoning and why the review reversed it. The `replaced` time is the
file's change time and not the time of the probe, because the rule for
outputs needs the second the new file arrived. The walk logs its counts
on a line of their own and not in the summary line.

## The problem

The enricher records its work on a video file under the file's path.
When a new file arrives at the same path, the enricher does not see
the change, and it keeps the facts of the old file.

A download manager does this when it imports an episode again, for
example to replace a damaged file or to take a better release. It
writes the new file under the old name. It sets the new file's
modified time from the source file, so the modified time does not show
the change in a reliable way. On import it also deletes the old
file's extras, which include `<episode>-thumb.jpg` and the
`<episode>.trickplay/` directory.

In one series library with trickplay enabled, 16 episodes were
imported again this way. Each new file had a different size from the
file the probe had read: for one of them, `probe.yaml` recorded
3345988372 bytes, and the file on the volume held 3362396196. After
the import:

- `trickplay.yaml` still recorded `result: found` for each path, and
  `episode-thumb.yaml` still recorded `provider: existing`.
- `probe.yaml` and `marks.yaml` still held the records of the old
  files.
- `status.gaps` showed 0 for `trickplay` and for `episode-thumb`.
- Neither the hourly walk nor a webhook rescan of the series folder
  wrote the thumbnails or the tile directories again.

The workaround was to delete those paths' entries from
`trickplay.yaml`, `episode-thumb.yaml`, `probe.yaml`, and `marks.yaml`,
and then post a webhook rescan. The walk removed 64 stale attempt rows,
the art phase wrote all 16 thumbnails, and the trickplay phase rendered
the tiles again.

Two faults show here. A file replaced at the same path keeps every
fact of the file it replaced. An output that is gone from the volume
does not count as work while the attempt that wrote it is inside its
window.

## What the code does now

**The walk reads the size and the modified time of every file.**
`statFile` (`folderfiles.go:168`) returns both from one stat. The walk
calls it for every file in `folderFiles.row` (`folderfiles.go:82`), for
each episode in `scanEpisode` (`series.go:385`), and for each movie
video (`movies.go:212`).

**The probe record holds the size, and nothing reads it.**
`probedFile` (`probeledger.go:142`) holds `Modified` and `Size`, and the
probe fact fills both from a stat at the time of the probe
(`probe.go:91` to `probe.go:98`). The walk compares only `Modified`:
`folderProbes.fill` (`probes.go:52` to `probes.go:60`) takes the
record's columns when `record.Modified` equals the file's modified
time, and ignores `Size`. A file with a new size and the old modified
time reads as probed.

**The probe gap is `probed != modified`, and an attempt holds it
closed.** The probe query (`enrich.go:285` to `enrich.go:287`) wraps
that condition in `gapClause` (`enrich.go:257`), which adds
`attemptClause` (`enrich.go:183`). A `found` probe attempt keeps the
path out of the gap for 30 days. So a file whose modified time changed
is not probed again until its last probe attempt is 30 days old,
although the comment above `gapQueries` says the change is a gap.

**Every other file fact closes on its attempt alone.** Each gap query
is "the output is missing, and no attempt is inside its window":

| Fact | Output condition | Where |
|---|---|---|
| `trickplay` | `trickplay = ''` on the file row | `trickplay.go:36` |
| `episode-thumb` | no image of role `thumb` or `still` linked to the episode | `artfacts.go:220` |
| title and season art | no file row at the art's path | `artfacts.go:169`, `artfacts.go:200`, `artfacts.go:263` |
| `trailerfile` | no present file of role `trailer` under the title | `trailerfile.go:155` |
| `contributor.headshot`, `contributor.biography` | the column is 0 | `contributorfacts.go:404` |
| `marks` | none: the attempt window alone | `marksgap.go:205` |
| `arrival` | `arrived = 0` | `arrivalfact.go:22` |

The walk writes each output condition correctly. The trickplay column
comes from a stat of the tile directory (`names.go:433`), and the
episode thumbnail from up to four stats (`names.go:444`). But the
attempt half of the query still holds the path closed. A `found`
attempt from last week keeps a deleted tile directory out of the gap
until the attempt is 30 days old.

**The walk lifts every attempt the ledgers hold, with no check.**
`likenDir.read` (`attempts.go:217` to `attempts.go:249`) turns each
ledger attempt into an `attempts` row. `itemOf` (`attempts.go:262`)
keys a file fact's attempt on the file's path. Nothing compares the
attempt with the file or with the output that the attempt says exists.
The marks spans come out of `marks.yaml` the same way
(`attempts.go:186`).

**The facts accept an output that is already there.** `trickplayOne`
(`trickplay.go:113` to `trickplay.go:123`) records `found` with
provider `existing` when a tile directory exists, whoever made it,
Jellyfin included. `artOne` (`artrole.go:64` to `artrole.go:72`) does
the same for an art file. Neither one decodes or downloads in that
case.

**The arrival fact never rewrites an entry.** `stampArrivals`
(`arrivalfact.go:85`) adds an entry only for a path the ledger does
not hold, because the entry records the first time the path held a
video.

**The walk and every fact read a folder with the same reader.**
`readFolder` (`scan.go:757`) calls `scanMovieFolder` or
`scanSeriesFolder`, and so does the walk's folder rule. After each
write, a fact reads its folder again through `readFolder` and upserts
its own attempt rows (`factrows.go`, `writeRows`).

**The sweep removes the attempt rows the walk did not lift.** The
prune deletes every `attempts` row that the walk's epoch did not mark
and whose `at` is before the walk's start (`attempts.go:95`,
`prune.go:713`). A scoped rescan does the same within one folder
(`attempts.go:107`). This is how the workaround worked: the walk no
longer lifted the deleted entries, and the sweep removed their rows.

## The design

The walk is the one reader that sees a folder's ledgers and the files
on the volume at the same time. So the walk decides whether an attempt
still describes the volume. An attempt that does not describe the
volume is not lifted, the sweep removes its row, and the fact's gap
opens. The gap queries, the attempt windows, and the ledgers' writers
do not change. The walk still writes nothing to the volume.

The check runs at the end of the read of one title folder, in the code
that `scanMovieFolder` and `scanSeriesFolder` share. It compares the
folder's lifted attempts and marks with the file rows, the episode
rows, and the probe records that the same read already holds. So the
walk, the webhook rescan, and the re-read after each fact's write all
apply one rule.

### The identity of a video file

A video file is the file the probe read when its size equals the size
in its probe record. A file whose size differs is a **replaced** file.

The size decides and the modified time does not, for three reasons. A
download manager sets the modified time from the source, so a new file
can carry the old time. A tool that edits tags in place, or a restore
that does not keep times, changes the modified time of a file whose
content is the same. And the walk already reads the size in the stat it
makes, so the check costs nothing.

The modified time keeps its current job, one that works as intended
after this change. A file whose modified time differs from its probe
record and whose size is the same is probed again, and no other fact
opens. The walk does not lift that file's `probe` attempt, so the probe
gap `probed != modified` opens at once and not 30 days later. A tag
edit in place can change a stream's language without a change of size,
so the probe reads it again. Nothing else runs, so a change of the
modified time alone never starts a decode, a download, or a provider
call.

A video with no probe record has no identity to compare, and the walk
treats it as it does now.

### What a replaced file opens

For a replaced file, the walk does four things:

1. It does not lift the `probe`, `trickplay`, `episode-thumb`, or
   `marks` attempt keyed on the path. These are every fact whose
   attempt keys on the video's path, except `arrival`. A file fact
   added later, such as the subtitle fact of plan 60, joins this list.
2. It does not lift the marks spans of the path, so the spans of the
   old file leave the catalog. `UpsertMarks` (`markrows.go:26`) already
   deletes the rows of a file row that brings no spans.
3. It writes 0 to the file row's `probed` column, so the probe gap
   `probed != modified` opens even where the modified time did not
   change.
4. It takes no technical column from the probe record or from the
   `.nfo` stream details. The probe wrote those details from the old
   record, and another tool wrote them for the old file. The row keeps
   what the file name gives. So `duration_ms` is 0 until the probe
   reads the new file, and the `trickplay` and `marks` phases wait for
   the new length, as they wait for any new file.

To open a fact means that its attempt no longer holds its gap closed.
The fact's own output condition still decides, and "An output made
from the earlier file" below makes the tiles and the thumbnail of the
earlier file fail that condition. So a replaced file opens all four
facts, whether the download manager deleted its outputs or left them.

Item facts do not open. A replaced video does not change the title's
identity, its `.nfo` elements, its art, its trailers, or its people.

### The time of the replacement

The walk sees the replacement only until the probe reads the new file.
After that, the record matches the file again. If a phase did not
reach the file in that `Job`, because it hit its time limit, a
provider's daily allowance was spent, or its source was not `Ready`,
the next walk would lift the old attempt again. For `marks`, that
brings back the spans of the old file for the rest of their window.

So the probe records when the new file arrived. The probe record gets
one more field, `replaced`: the change time of the file, where the
probe found a size different from the record it replaces. A move, a
hard link, and a copy all set a file's change time, and no tool can set
it back, so it is the second the new file took the path. The probe fact
already reads its ledger in the same write (`probe.go:100`), so the
rule is:

- No earlier record: `replaced` stays empty. No earlier file is known.
- An earlier record of the same size: `replaced` keeps its value, so a
  later `chmod` or tag edit, which moves the change time, does not move
  it.
- An earlier record of a different size: `replaced` is the file's
  change time.

Until the probe has read the new file, the walk and the phases take the
file's change time from the stat as the same second.

The walk then applies one more check to every file fact except
`arrival`. An attempt made before the path's `replaced` time describes
the earlier file, and the walk does not lift it or its marks spans. An
attempt made at or after that time describes this file and counts as
usual. A record with no `replaced` time holds no attempt back, so every
ledger written before this change reads as it does today, and the
first walk after the upgrade opens nothing.

The marks fact follows the same rule when it writes. Today a partial
answer replaces only the spans of the sources that answered
(`marksrole.go:114`). When the path's last marks attempt is older than
its `replaced` time, the fact first removes every span of the path,
because a source that did not answer holds spans of the old file. The
marks container reads `probe.yaml` for this and does not write it, so
each ledger still has one writer.

### An output made from the earlier file

A thumbnail is a frame of one encode, and tiles place each thumbnail at
one encode's times. A new encode can crop the frame another way or
shift every time, so the outputs of the earlier file are not this
file's. The rule tells them apart by their modified time against the
second the new file arrived:

- A tile directory or a thumbnail made before that second was made from
  the earlier file.
- One made at or after that second was made from this file, by this
  operator, by Jellyfin, or by any other writer, and it counts as this
  file's.

Jellyfin makes its tiles after it reads a file, so the rule still
accepts a tile directory that Jellyfin made first for this file. It
does not accept Jellyfin's tiles of the earlier file.

The walk applies the rule to its rows. A video no longer names tiles
made before the second, and an episode no longer links to a still made
before it. The trickplay and episode-thumb gaps then open through their
own output conditions. The phases read the rule again just before they
act, because the walk can be minutes old:

- The trickplay phase decodes the new file and replaces the whole tile
  directory through a door of the volume writer that takes only a
  `.trickplay` directory, and only while it is older than the second.
- The art phase downloads the still again and writes it over the old
  thumbnail through a door that takes only a `-thumb.jpg` file, and
  only while it is older than the second.

Each door checks the modified time itself, so a tile directory or a
thumbnail that another writer made for this file between the walk and
the write is kept.

### An output that is missing

A `found` attempt says that the fact's output exists. The walk lifts a
`found` attempt only when the same read found that output:

| Fact | The output the walk must find |
|---|---|
| `trickplay` | the tile directory beside the video: the file row's `trickplay` column is not empty |
| `episode-thumb` | an image of role `thumb` or `still` linked to the episode, the condition of the gap query |
| `poster`, `backdrop`, `logo`, `clearart`, `banner`, `landscape`, `discart`, `season-poster`, `season-banner` | a file row at the attempt's path |
| `trailerfile` | a file of role `trailer` under the title folder |
| `contributor.headshot`, `contributor.biography` | the contributor row's column is 1 |

A result other than `found` promises no output, so a `nothing`, an
`error`, or a `partial` attempt is lifted as usual. A title whose
provider has no still keeps its `nothing` attempt and its 30-day
window.

The rule does not ask who made the output. A tile directory that
Jellyfin made satisfies a `trickplay` attempt, and the trickplay phase
still records `existing` for a directory it finds before it decodes,
unless the directory was made from an earlier file.

A fact that writes its output and then its attempt can race a walk
that reads the same folder. If the walk looks for the output before
the write and reads the ledger after it, the walk does not lift the
new attempt. The attempt row survives, because the sweep keeps every
row whose `at` is after the walk's start. If the fact's own re-read
lost the race as well, the next phase reads a gap that the volume
already fills, and it records `existing` after one stat.

### What `status.gaps` counts

The reporter counts each gap with the query that the phase reads
(`attempts.go:293`). A walk, in a walk `Job` or in a webhook rescan,
removes the rows that it did not lift before any phase starts. So the
next report counts the replaced files and the missing outputs as soon
as a walk has read their folder, before a phase does the work. The
operator then schedules the phases as it does for any gap. No field of
the status changes. The `trickplay` and `marks` counts of a replaced
file appear once the probe has measured the new file, because both
gaps need its length, which the walk clears for a replaced file.

A person also needs to see why a count grew. A walk and a rescan log
one line for each replaced file with both sizes, and one line of counts
when they reopened anything. The summary line keeps its form. The line
names the file by its opaque name, as every line of the walk does:

    replaced file path:6f1c2a90d3b4: 3345988372 bytes in the probe record, 3362396196 on the volume
    reopened the facts of 16 replaced files and 32 missing outputs

A walk that could not read part of the tree removes nothing, as it
does now, so its counts do not grow until a complete walk runs.

### Arrival does not move

`arrival.yaml` records the first time a path held a video, and the
recently added rail reads it. A replaced file keeps that entry, and
its `arrival` attempt is lifted as usual.

- The title is not new to the library. The episode was on the volume
  before, and a person who watched it gains nothing when it returns to
  the recently added rail.
- A download manager that upgrades a whole series to a better release
  replaces every episode. If arrival moved, one upgrade would fill the
  rail with old episodes and push the new ones off it.
- The arrival fact never rewrites an entry, and its ledger exists to
  keep the first sighting through every later change to the volume.
  A replaced file is such a change.

A person who wants a title back on the rail can delete its entry and
its attempt from `arrival.yaml`. The next walk then opens the arrival
gap, and the arrival phase stamps the path again.

### The cost

The walk reads nothing new for a file that matches its record. It
already stats every file, reads every `.liken` ledger of the folder,
stats the tile directory beside each video, and stats the thumbnail
names beside each episode. The check compares values that the same
folder's read already holds in memory. A file whose size or modified
time differs from its record costs one more stat, for its change time.
A library of tens of thousands of unchanged files walks in the same
time as before.

The phases do only the work that a replacement or a deletion calls
for:

- A replaced file costs one `ffprobe`, one marks request to each
  source that serves `marks`, one decode where trickplay is on, and one
  still download. Its tiles and its thumbnail are made again whether
  the earlier ones are still there or not.
- A change of the modified time alone costs one `ffprobe`, and no
  other phase runs. Today the same probe runs 30 days later, when the
  attempt window ends, so the change moves that cost earlier and does
  not add it. A restore that changes the modified time of every file
  costs one `ffprobe` per file, which is the cost of the library's
  first probe.
- A missing output costs one render or one download for that output.
  An output that exists costs one stat in the phase and no decode.
- The marks, trickplay, and episode-thumb facts read `probe.yaml` and
  stat the video once for each file they act on. The ledger is in the
  folder the fact writes in the same step.

A file that a program writes in place under the library, such as a
recording, changes size on each walk until it is complete. Each walk
then counts it as replaced, and it costs one `ffprobe`, one marks
request, and, where trickplay is on, one decode per walk. The libraries
this operator serves get their files from download managers, which
write in another directory and then move the file. So this plan adds no
rule for a file that is still growing, and that cost is a known limit.

### What stays the same

- One writer owns each ledger. The walk reads the ledgers and writes
  none of them, and the scan container still mounts the volume
  read-only. The probe writes `replaced` into its own ledger. The marks
  fact reads `probe.yaml` and writes only `marks.yaml`.
- The trickplay phase accepts a tile directory that Jellyfin made
  first for the file at the path, and an art fact accepts an art file
  another tool wrote. The only files the phases now replace are a tile
  directory and an episode thumbnail made before a new file arrived.
- The attempt windows, the refresh times, and the release-date rule
  apply to every attempt that the walk lifts.
- A ledger written before this change reads the same way, except for
  the two checks it must fail: a probe record whose size differs from
  the file, and a `found` attempt whose output is gone.

## Considered and set aside

**Keying each fact's ledger on the file's size as well as its path.**
Every file fact would write the size of the file it read into its
attempt, and the walk would compare each attempt with the stat. That
changes five writers, and every ledger on a volume today would need a
rule for an attempt with no size. The probe record already holds the
identity, and one field in one writer covers the case that the probe
record alone does not.

**Judging each attempt by the probe record's `at`.** An attempt older
than the probe record would describe an earlier file. That needs no
new field. But the probe writes a new record after a change of the
modified time and after a refresh of `probe`, so either one would open
every file fact of the file. `replaced` moves only when the size moves.

**Deciding in the gap queries.** The catalog could hold the size in
each attempt row and compare it in SQL. But the catalog cannot tell a
`found` attempt made after the last walk, whose output the walk has not
seen yet, from a `found` attempt whose output the last walk found gone.
The walk sees the ledger and the volume in one read, so it decides.

**Letting the probe clear the other facts' ledgers.** The probe sees
the replacement first. But then two containers would write one ledger,
and the ledgers on a network mount have no locks.

**Hashing the file.** A hash sees a replacement that keeps the size
and the modified time. It also reads every byte of every file, which is
terabytes on a large library. Two video files of the same size and the
same modified time at one path are not a case this plan covers.

**Keeping the tiles and the thumbnail of a replaced file.** The first
draft kept them: the trickplay phase would treat a tile directory older
than the probe's time as the old file's, but Jellyfin can make tiles
for the new file before the probe reads it, and that rule would replace
them. The review reversed this, because tiles and a still of another
encode can be wrong in their times and their frames. The rule compares
with the file's change time, the second it arrived, and not with the
time of the probe, so Jellyfin's tiles of the new file are kept.

**Moving arrival with the file.** The section "Arrival does not move"
gives the reasons.

**A status field for replaced files.** `status.gaps` already shows the
work, and the walk's log shows the cause. A field would repeat a count
that changes with every walk.

## How the work is tested

Each test came first and failed before the code it proves. The walk
tests build an enriched episode in `t.TempDir()`, with the ledgers
written through the types the facts write them with, and replace its
file by writing a new encode and setting the old modified time back.
The gap tests rescan the series folder into `newSQLiteCatalog` and read
`gapCounts`.

In `replacedfile_test.go`:

1. `TestAFileReplacedWithANewSizeReopensEveryFileFact`: after the walk,
   only the `arrival` attempt is lifted, and the path has no marks, no
   streams, no probed column, no length, no tiles, and no linked still.
2. `TestAnUnchangedFileStaysClosed`: every attempt is lifted, and every
   gap count is 0.
3. `TestANewModifiedTimeReopensTheProbeAlone`: the probe gap counts 1,
   and no other gap opens. It failed before the change, because the
   found probe attempt held the gap closed.
4. `TestAnAttemptBeforeTheReplacementDoesNotCount`: an attempt before
   `replaced` and its spans are dropped, and one after it is lifted.
5. `TestAWebhookRescanOpensTheFactsOfAReplacedFile`: after the import
   the probe and the thumbnail gaps count 1, and after the probe the
   trickplay and marks gaps count 1 as well.
6. `TestARescanLogsTheFileItFoundReplaced`: the two log lines.

In `missingoutputs_test.go`:

7. `TestADeletedThumbnailReopensTheEpisodeThumbGap`.
8. `TestADeletedTileDirectoryReopensTheTrickplayGap`.
9. `TestAMissWithNoOutputStaysLifted`.
10. `TestAFoundAttemptWhoseFileIsGoneIsNotLifted`, over a poster, a
    season poster, and a trailer file, and
    `TestAContributorFileThatIsGoneReopensItsFact`, over a headshot and a
    biography.

In `replacedoutputs_test.go`:

11. `TestTheProbeRecordsWhenAFileWasReplaced`: no earlier record, one
    of the same size, and one of another size.
12. `TestTheTrickplayPhaseReplacesTilesOfTheEarlierFile` and
    `TestTheTrickplayPhaseKeepsTilesMadeAfterTheReplacement`, which is
    the case of tiles Jellyfin made for the new file.
13. `TestTheArtPhaseReplacesAThumbnailOfTheEarlierFile`: a still from
    before the replacement is written over, and one from after it is
    kept.
14. `TestTheMarksPhaseDropsEverySpanOfTheEarlierFile`: a partial answer
    leaves no span of the source that did not answer.
15. `TestTheReplaceDoorsRefuseAnythingElse`: the two doors refuse any
    other name and keep an output newer than the replacement.

## The guides that change

The guides are under `library-operator/docs/content/docs/`.

- `guides/scanning.md`: the section on the `.liken/` directory says
  that the probe record holds each file's size, and a new section on a
  file replaced at the same path says what the walk opens, what it
  keeps, and the two log lines.
- `guides/enrichment.md`: "When a fact asks again" says that an attempt
  stops counting when its file is replaced or its output is gone. The
  trickplay section states the exception to accepting Jellyfin's
  tiles, and says to delete a tile directory to render it again. The
  write rule states the one exception to "An art file that already
  exists is never replaced".
- `guides/webhooks.md`: a rescan of a folder opens the facts of a file
  replaced in it.
- The `status.gaps` description in `deploy/libraries-crd.yaml`, which
  generates `reference/libraries.md`, adds the two cases to the text on
  the attempt window.

Each guide generates a skill under `skills/`, so the docs generate
target runs after the edit.

## What the design must answer

The review settled each decision before the build:

1. Arrival does not move for a replaced file. Approved, for the reasons
   in "Arrival does not move".
2. The size decides whether a file is replaced. A change of the
   modified time alone opens only the probe, and it opens it at once
   and not after the 30-day window. Approved.
3. A replaced file opens every file fact, and the phases replace tiles
   and a thumbnail made before the new file arrived. Changed in review
   from the draft, which kept them. "An output made from the earlier
   file" states the rule.
4. The probe record carries `replaced`, the file's change time, and the
   walk, the marks fact, the trickplay fact, and the art fact read it.
5. The missing-output rule covers every fact that writes a file: the
   nine art facts, `episode-thumb`, `trickplay`, `trailerfile`, and the
   two contributor files.
6. `status.gaps` carries the work, and the log carries the cause. No
   status field is added.
7. A file written in place under the library has no rule. Its cost is a
   known limit, stated under "The cost".

## Not in this plan

- A replacement that keeps the size. The modified time still opens the
  probe for it, but no other fact.
- A file that is still being written in place, beyond the cost stated
  under "The cost".
- The `.nfo` facts. A deleted `.nfo` file loses the elements that the
  `nfo`, `identity`, and `probe` facts wrote. The fight check and the
  hash of each element group decide that case, and this plan does not
  change them.
