# 95, Progress in a file of several episodes

This plan, written on 2026-11-10, gives each episode of a file such as
`Show - S01E01-E02.mkv` its own progress: watched, in progress, or not
started, and its own resume point. The screens and Jellyfin agree on
it. Commit `cbac298a` made the scanner write one catalog item for each
episode of such a file, and plan 72 built the marks that this plan
reads.

Built on 2026-11-10. No drill on a cluster has run. The drill must
show, on a file of two episodes: both episodes in the catalog with
their parts, Play on the second episode starting at the midpoint, the
series page after a play past the midpoint, and a Jellyfin play of the
file read as both episodes. The Jellyfin listing in the tests is
written by hand and not recorded from a server.

## The problem

A file of n episodes is one file, and the scanner writes one item for
each episode it holds (`library-operator/series.go:331-335`). Both
items play the whole file from the start
(`docs/content/docs/guides/scanning.md:70-72`). The progress store
holds one row for each `Play`, and a row holds one position over the
whole file. So a person who watched both episodes of a double sees the
first one as finished and the second as not started. A person who
chooses the second episode watches the first one again. A play that
Jellyfin reports for the double names only the first episode, so the
second episode never shows progress from Jellyfin either.

## What the owner decided

1. The episodes split the file evenly. For a file of duration D and n
   episodes, episode k, counted from 1, covers the seconds from
   (k-1)·D/n to k·D/n. When the position passes k·D/n, episode k is
   finished, and the episode that holds the position is in progress.
2. The rule applies to plays on the screens of the cluster and to
   plays that come from Jellyfin, so both agree.
3. Play on the second episode in the media browser starts the file at
   that episode's start, which is the midpoint for a double. Resume
   works in the same model.
4. A file that is not split evenly marks an episode watched at the
   wrong moment. The project accepts that limit. Chapter marks could
   place the boundary later, and this plan does not design that.

## What the code holds today

### The catalog

- An episode is a row of `episodes`, keyed by `(library, id)`, with
  `path`, `duration`, `season`, and `episode`
  (`corrosion/schema/catalog.sql:158-180`). Its id is
  `episodeID(seriesID, season, episode)` (`series.go:360`), so the two
  episodes of a double are two rows with the same `path`.
- A file is one row of `files`, keyed by `(library, path)`, with the
  probe's `duration_ms` (`catalog.sql:220-229`). A double is one row.
- `file_items` links a file to the items it holds, and all three of
  its columns are the primary key (`catalog.sql:361-366`). A double
  has two links. The write does nothing on a conflict
  (`catalog.go:321-337`).
- No row says that an episode is one of n in its file, or which one.
  `episodeIdentity` knows both: it returns the episodes in the order
  of the file's range marker (`episodeidentity.go:33-78`), and
  `scanEpisode` loops over them (`series.go:356-389`), but neither
  number reaches a row.
- An episode with no `.nfo` block of its own takes the file's stream
  facts, so its `duration` is the duration of the whole file
  (`episodeidentity.go:93`, `nfo.go:397-405`). The first episode
  usually takes the same value from its block's `<fileinfo>`.
- The scanner writes every row on every walk: `scanwrite.go:34` and
  `scanwrite.go:38` upsert the episodes and the links of every file
  the walk read.
- The marks role asks no provider for a file of more than one episode
  (`marksrole.go:96-98`), so such a file carries only the estimate
  marks, which are positions in the whole file.

### Corrosion's rules for a schema change

Each agent applies the difference between its schema file and its
database on every start. It adds tables, columns, and indexes, and it
refuses to drop any. cr-sqlite allows only `CREATE TABLE` and
`CREATE INDEX`, needs a default on every column that is not null,
allows no unique index other than the primary key, and needs a primary
key on every table (`catalog.sql:1-7`, `progress.sql:1-8`). A new
column goes at the end of its table (`catalog.sql:218-219`).

### A `Play` from the media browser

- Enter on an episode's row runs `Series::press`
  (`media-browser/src/screens/series/episode.rs:105-142`). Resume
  takes the position of the episode's progress
  (`screens/series/progress.rs:115-121`), and Play, Start over, and
  Pick up here start at nothing.
- `request_play` (`media-browser/src/browser.rs:764-821`) resolves the
  play list with `Source::play` and publishes it. For an episode the
  list is one item, the `MIN(path)` primary video of the chosen
  episode (`catalog/local/play.rs:191-245`). Every `Step::Play`
  reaches this one function.
- The request names the work by the series' aliases and the chosen
  episode's numbers, and `start` is a decimal count of seconds
  (`playrequest.go:40-51`, `media-browser/src/bus/play.rs:78-80`).
  The operator writes the numbers as the annotations
  `library.liken.sh/season` and `library.liken.sh/episode`
  (`playrequest.go:298-314`) and copies `start` into `spec.start`
  (`playrequest.go:224-234`).
- `media-operator`'s `PlaySpec.Start` is where the first item begins
  (`media-operator/api.go:422-431`), and the player passes it to mpv as
  `--start` (`media-operator/player.go:159-164`). The presentation
  carries one `season` and `episode` (`media-operator/api.go:515-517`)
  and the marks of the file (`api.go:556`, `api.go:584-598`).
- The next block names the work after the chosen one
  (`screens/upnext.rs:153-178`). For the first episode of a double it
  names the second episode, which the same file holds. Its
  `remaining_seconds` is the next episode's catalog duration less
  where the audience stopped in it (`upnext.rs:89-98`, `upnext.rs:274`).

### The progress store

- `plays` holds one row for each `Play`, keyed by `play`, with
  `position` and `duration` in seconds and `season` and `episode`,
  which are 0 for a film (`corrosion/progress-schema/progress.sql:28-43`).
  `play_people` and `play_aliases` hang off it (`progress.sql:48-62`).
  The store holds no catalog id (`progress.sql:10-13`).
- `recordStatus` writes the position and the duration that the
  playback sidecar reports (`progress.go:198-219`,
  `progressstore.go:79-92`). These are mpv's positions in the file.
  `recordAudience` writes the numbers from the annotations
  (`progressstore.go:101-126`, `plays.go:121-122`), and `recordFinal`
  closes the row (`progressstore.go:212-221`).
- `recordOutside` writes a Jellyfin play or a mark as one whole row.
  A newer message wins, and at the same second the message wins only
  when `outsideSaysMore` (`progressstore.go:143-207`).
- A mark writes one row for each episode it names, with the position
  and the duration that the browser sent for that episode
  (`progressmark.go:62-79`, `progressmark.go:132-154`). The browser
  sends the duration of the audience's play, or the catalog's
  duration where there is no play (`screens/movie.rs:879-884`).

So a row from a `Play` or from Jellyfin holds a position in the whole
file, and a row from a mark holds a position in one episode.

### The readers

- Every screen reads progress through one statement,
  `progress::seen` (`media-browser/src/catalog/local/progress.rs:139-212`).
  It joins a row to the catalog by alias, and an episode row to the
  episode at its `season` and `episode` (`progress.rs:44-50`). It
  computes `finished` with the credits marks of the item's main file
  (`progress.rs:67-133`, `catalog/progress/watched.rs:31-38`).
- `seen.rs` applies the rules to that answer: `every` for the thread
  rule, `episodes` for the episode wall, and `films` for the bars of
  plan 86 (`catalog/progress/seen.rs:68-145`, `seen.rs:199-241`). The
  continue-watching row, the series page, and next up all walk
  `thread::walk` over those rows (`catalog/progress/thread.rs:45-88`).
  `touched` resolves only the works a row names
  (`catalog/local/touched.rs:17-58`).
- The "who has seen" bars of plan 86 read films only
  (`seen.rs:210`). Plan 89, the status of a series, is not built and
  reads no progress.
- No Go reader of the store decides watched for a screen. The progress
  role reads one row only for the tie rule (`progressstore.go:144`).

### Jellyfin

- Jellyfin holds a double as one item with `IndexNumber` 1 and
  `IndexNumberEnd` 2, one position over the whole file, and one played
  flag.
- `jellyfinItem` does not decode `IndexNumberEnd` (`jellyfinapi.go:144-153`).
- `jellyfinLookups.fold` keys an episode by the series' aliases, the
  season, and `IndexNumber` alone (`jellyfinindex.go:331-343`). So
  `itemFor` finds the double for episode 1 and misses it for episode 2
  (`jellyfinindex.go:115-145`).
- The webhook names a play by the user and the item,
  `jellyfin-{user}-{item}`, with the template's `{{EpisodeNumber}}`,
  the first episode, and the item's position and run time
  (`jellyfinwebhook.go:128-162`). The backfill and the reconcile do
  the same through `identityOf` (`jellyfinbackfill.go:264-293`,
  `jellyfinreconcile.go:52-68`).
- The outbound joins a `Play`'s status and audience in memory, finds
  the item with `itemFor`, and writes the status position. It sets
  played only when `watched` passes for the whole file
  (`jellyfinoutbound.go:169-214`). A `Play` of episode 2 writes
  nothing today, because `itemFor` misses.
- The marks role writes each mark to each person's item, played at the
  mark's position or unplayed at 0 (`jellyfinmarks.go:237-368`), and
  remembers the position to drop its echo (`jellyfinoutbound.go:332-358`).

## The decision: where the split lives

### (a) Every reader maps file positions

The rows stay as they are. Each screen rule, such as `thread::walk`,
`seen::episodes`, the series page, and next up, learns the file's
episodes from the catalog and maps a file position to the state of
each episode.

The rule runs in many places, and each one must agree. A new screen
that forgets it shows the old state. The tie order, the clear at 0,
and the credits line each need their own mapping in each rule.

### (b) Writers fan out

Each writer writes one row for each episode, with a position and a
duration inside that episode, so every reader works unchanged.

- The progress role holds no catalog (`progress.sql:10-13`), so the
  `Play` must carry the episode's place in its file as more
  annotations. Each status then writes n rows, and a seek back across
  a boundary must also rewrite the earlier rows.
- A row name must name the episode, as `jellyfin-{user}-{item}-e2`.
  The rows written today keep their old names and hold file positions,
  and a reader cannot tell them from the new rows. The backup of plan
  56 and every tie of `recordOutside` then work on rows that one
  play wrote together.
- Jellyfin holds one position for the file. The outbound must convert
  each episode row back with (k-1)·D/n + p, and two rows map to one
  item, so two writes compete for one position.
- The stored rows hold the even split itself. When chapter marks
  place the boundary later, every stored row is wrong and cannot be
  computed again, because the file position is gone.

### (c) File rows in the store, episode rows at the one read

The store keeps the shape that Jellyfin and mpv use: a row from a
`Play` or from Jellyfin holds a position in the file and names the
first episode it covers. A row from a mark keeps a position in its one
episode. The catalog records where each episode is in its file. The
one statement that every screen reads, `progress::seen`, turns each
file row into one row for each episode it covers, with a position and
a duration inside that episode. Every rule above it then works
unchanged.

### The recommendation: (c)

(c) puts the split in two places that already exist for this kind of
fact: the scanner, which knows the episodes of a file, and the one
read that every screen takes. It ranks first on each count:

- **Features.** The screens and Jellyfin read the same file position,
  so they agree by construction. A better boundary from chapter marks
  later changes the catalog and the read, and every row written
  before it reads correctly with the new boundary.
- **Stability.** No writer changes. The progress role, its tie rule,
  the finalizer, the backup, the webhook, the backfill, and the
  reconcile write what they write today. Rows written before the
  change read correctly, except the case in "Rows written before
  this change".
- **One rule.** The thread rule, the episode wall, next up, and the
  continue-watching row see per-episode rows, so no screen can forget
  the split.

(a) is (c) with the mapping copied into each rule, so (c) replaces it.
(b) changes every writer, makes old rows ambiguous, and stores a
boundary that the project expects to improve.

## The design

### The catalog records each episode's place in its file

`file_items` gains two columns at its end:

```sql
part INTEGER NOT NULL DEFAULT 0,
parts INTEGER NOT NULL DEFAULT 0,
```

`part` is the episode's place in the file, counted from 1 in the order
of the file's range marker, and `parts` is how many episodes the file
holds. 0 in both is a row that no scan has written since the columns
arrived, or a movie, and every reader reads it as a file of one work.
The columns go on the link and not on `episodes`, because an episode
with a second encoding has two files, and only one of them can be a
double.

`scanEpisode` writes `part` as the place of each episode in the list
that `episodeIdentity` returns and `parts` as the length of that list.
An episode that the scanner leaves out for having no number still
counts in `parts`, because its seconds are still in the file.
`fileRow.Items` (`rows.go:209`) carries each item with its part.

`UpsertFileItems` writes the two columns, and on a conflict it updates
them only where they differ:

```sql
ON CONFLICT (library, path, item) DO UPDATE SET part = excluded.part, parts = excluded.parts
WHERE file_items.part IS NOT excluded.part OR file_items.parts IS NOT excluded.parts
```

So a repeat write still changes no row and broadcasts nothing.

When the file's probe duration is known, `scanEpisode` writes each
episode's `duration` as that duration divided by `parts`. So the
runtime under each still, the duration of a mark, and the
`remaining_seconds` of an offer are one episode's length. When the
probe duration is 0, the episode keeps the `.nfo` duration as today.

The progress store's schema does not change.

### The split, in one function

A new module, `media-browser/src/catalog/progress/parts.rs`, holds the
rule as a pure function. Its inputs are one row's position and
duration, the file's parts, the part the row names, and the file's
credits marks. Its output is the rows of the episodes that the row
covers. The file's duration D is the probe's `duration_ms` / 1000 when
it is known, and the row's duration otherwise, so the read and the
start position use the same boundaries. Part k starts at
floor((k-1)·D/n), and it ends where part k+1 starts, or at D for the
last part.

A row covers the part it names and every later part of the file:

| The row | What the read answers |
|---|---|
| A file of one work: `parts` 0 or 1 | The row as it is. |
| D is 0 | The row as it is, for the episode it names. A row with no duration is never finished. |
| A mark row: no library and the `mark-` prefix (`catalog/progress/finish.rs:31-41`) | The row as it is. A mark already holds one episode's position. |
| Position 0 or less | One row for the named episode at position 0. A clear and an unplayed toggle in Jellyfin keep their meaning. |
| Position before the named part's start | No row. The play reached only seconds the row does not cover. |
| Any other position | One row for each covered part that starts before the position. A part that the position passed is finished at its own length. The part that holds the position is at the position less the part's start. |

Each row has the part's length as its duration and the part's numbers
as its `season` and `episode`. `finished` runs on each row with the
credits marks that start inside the part, moved to the part's own
seconds, and an absent end becomes the part's end. Each row takes the
name of the play with `#` and the part in two digits after it, as
`living-room-x7k2#02`. The thread rule breaks a tie in recorded time
on the name (`thread.rs:70-74`), so the later part sorts last and the
thread stands on the part that is in progress. `Origin::of` reads only
the prefix and the library, so the origin of each row does not change.

`progress::seen` reads, for each episode play, the part and the parts
of its named episode in that episode's main file, the file's
`duration_ms`, and the numbers of every episode of that file with its
part. It reads them through `file_items_library_item`, the index that
the credits lookup already takes (`progress.rs:61-80`). The gate that
reads the credits only for a play past the half of its work
(`progress.rs:103-109`) applies to a file of several episodes only
after the split, so the read takes the credits of every such play.
`plan()` (`progress.rs:160-164`) checks the query plan in a test.

`--print-progress` (`media-browser/src/main.rs:127-128`) prints the
rows after the split, so a drill reads each episode's state with no
screen.

### Where a play of episode k starts

`Source::play` for an episode also answers the second where the
episode starts in its file: floor((k-1)·D/n) from the main file's
`part`, `parts`, and `duration_ms`, and 0 for a file of one work or a
file with no duration. `request_play` adds it to the start that the
step carries, so every `Step::Play` keeps its start in the episode's
own seconds and one function converts it:

- Play and Start over on episode 2 of a double start at the midpoint.
- Resume on episode 2 starts at the midpoint plus the episode's
  position.
- Resume on episode 1 starts at its position, as today.
- Pick up here marks the earlier episodes, as today, and plays from
  the chosen episode's start.

The request's `start`, the `Play`'s `spec.start`, the interruption
resume of `media-operator` (`media-operator/operate.go:1816`), and the
progress rows all hold positions in the file. `media-operator` does not
change.

### One `Play` covers the file from its first episode to its end

A `Play` of episode k plays the file from part k to the end of the
file. The work identity on the `Play` names episode k and never
changes during the play. The read derives the later episodes from the
position, so nothing has to move the identity at a boundary, and no
second `Play` starts mid-file.

The next block follows the file, not the episode. `after_episode`
(`upnext.rs:153-178`) offers the work after the last episode of the
chosen episode's file, so a play of episode 1 of a double offers
episode 3. `remaining_seconds` for a next work that is part k of a
file counts from part k's start to the end of the file, because its
`Play` runs that far. The display raises the card at the file's
credits marks or at its end, which is the end of the last part.

### Through the stages of a play

A double S01E01-E02 of 2,640 seconds, with each part 1,320 seconds,
followed by a single S01E03. The time rule marks an episode of 1,320
seconds finished at 1,254 seconds, because 5 percent is 66 seconds.

| The play | The row | The series page | Next up |
|---|---|---|---|
| Nothing played | none | no bars | the series is not in the row |
| Play on E01, at 600 s | E01, 600 of 2,640 | E01 at 600 of 1,320 | Resume S01E01 at 10:00 |
| The same play at 1,300 s | E01, 1,300 of 2,640 | E01 finished, E02 no bar | Next in S01E02, from 1,320 s in the file |
| The same play at 1,800 s | E01, 1,800 of 2,640 | E01 finished, E02 at 480 of 1,320 | Resume S01E02 at 8:00, from 1,800 s in the file |
| The same play to the end | E01, 2,640 of 2,640 | both finished | Next in S01E03 |
| Play on E02 alone, at 1,800 s | E02, 1,800 of 2,640 | E01 no bar, E02 at 480 of 1,320 | Resume S01E02 at 8:00 |
| Jellyfin plays the double to 1,800 s | `jellyfin-{user}-{item}`, E01, 1,800 of 2,640 | E01 finished, E02 at 480 of 1,320 | Resume S01E02 at 8:00 |

### A file of more than two episodes

The same formulas apply for any n. A file of three episodes of 3,960
seconds has parts that start at 0, 1,320, and 2,640 seconds. A
position of 3,000 seconds reads as E01 and E02 finished and E03 at 360
of 1,320. `part` takes two digits in a row's name, so a file of up to
99 episodes sorts in order.

### A file with no duration

When the probe and the row both hold a duration of 0, the read does
not split the row. The row reads for the episode it names, with no
duration, so it is never finished and draws no bar, as today. A play
of episode 2 of such a file starts at the beginning of the file, and
`request_play` writes a log line that says the file has no duration to
split. The scanner keeps the `.nfo` duration for each episode.

### Jellyfin

The jellyfin role keys a double under each of its episodes and reads
nothing new in the webhook:

- `jellyfinItem` decodes `IndexNumberEnd`. `fold` keys the item under
  every episode number from `IndexNumber` to `IndexNumberEnd`, so
  `itemFor` finds the double for episode 1 and episode 2. The index
  also holds, for each episode item, its first number, its last
  number, and its run time, so the marks can find a part's seconds.
- **Inbound.** The webhook, the backfill, and the reconcile do not
  change. Each row names the item's first episode and holds the file
  position, and the read covers every part from it.
- **Outbound.** `jellyfinoutbound.go` does not change. The status
  position of a `Play` is already a file position, and `itemFor` now
  finds the double for a `Play` of episode 2. Played is still set
  only when the whole file passes `watched`, because Jellyfin's flag
  covers the whole item. A person who stops at the end of episode 1
  leaves the double resumable at the midpoint in Jellyfin.
- **Marks.** A mark holds one episode's position, and Jellyfin holds
  one position for the file. `jellyfinmarks.go` converts with the
  index: part k is the episode number less the item's first number,
  plus 1, and n is the item's last number less its first, plus 1. D is
  the item's run time, or the mark's duration times n where the run
  time is 0.
  - Watched on part k of n, k less than n: position k·D/n, and the
    played flag left as Jellyfin holds it.
  - Watched on part n: played, at position D, as today. The check that
    skips an item already played with no position applies only here.
  - Cleared on part 1: unplayed at 0, as today.
  - Cleared on part k, k more than 1: unplayed at (k-1)·D/n.
  - A Pick up here list that names several parts of one item makes one
    write for that item: the furthest watched part sets the position,
    and with no watched part, the first cleared part does.
  - The echo record holds the file position that the role wrote,
    because the webhook compares file positions.

A double that Jellyfin holds with no `IndexNumberEnd` keys only its
first episode, and a `Play` or a mark of episode 2 finds no item and
writes the log line it writes today.

### Intro marks, credits marks, and subtitles

- The display reads every mark as a second of the file
  (`media-operator/api.go:584-598`). A play of part k starts at part
  k's second, so the intro skip and the up-next card act on the
  file's marks as today. Providers give no marks for a file of several
  episodes (`marksrole.go:96-98`), so a double carries only estimates.
- The finished rule maps the credits marks into each part, as the
  split above states, so a credits mark at the end of the file moves
  only the last part's line.
- The subtitle tracks and their event counts are facts of the file
  (`catalog/local/play.rs:104-118`) and do not change. Plan 67's
  saved subtitle offset is not built, and a file offset would apply to
  every part of the file.

### Rows written before this change

- Rows of plays that started on episode 1 and every Jellyfin row read
  correctly, because they name the first part and hold file positions.
- Mark rows read as they did, because the split skips them. A mark of
  episode 2 that the browser wrote before the change holds the whole
  file's duration as its position and its duration, so it reads as
  finished, which is what the person marked.
- A row of a play that started on episode 2 before the change played
  the file from 0, so its position counts from the start of episode 1.
  The read takes the row as covering episode 2 only. A position past
  the midpoint reads as progress in episode 2 that the person did not
  watch, and a position before it reads as no row. These rows exist
  only where a person chose the second episode of a double, and the
  next play of that episode replaces them. The plan accepts the loss.
- Until a walk writes `part` and `parts`, every link reads as a file
  of one work, which is today's behavior. Every walk writes every link
  (`scanwrite.go:38`), so the first walk after the upgrade, at the
  latest the hourly one, records them. No migration runs and no row of
  the progress store changes.

## Files that change

**`library-operator`, the Go operator:**

- `corrosion/schema/catalog.sql`: `part` and `parts` on `file_items`,
  and the comment that says what they mean.
- `rows.go`: each item of `fileRow` carries its part and the file's
  parts.
- `series.go`: `scanEpisode` records the parts and writes each
  episode's duration as the file's duration divided by `parts`.
- `catalog.go`: `UpsertFileItems` writes and updates the two columns.
- `jellyfinapi.go`: `jellyfinItem.IndexNumberEnd`.
- `jellyfinindex.go`: `fold` keys every episode of an item, and the
  index holds each episode item's span and run time.
- `jellyfinmarks.go`: the conversion of a mark to the item's position,
  and one write for each item of a list.
- `docs/content/docs/guides/scanning.md`, `guides/browser.md`,
  `guides/jellyfin.md`, `reference/bus.md`, and the `skills/` that
  `make -C docs skills` writes from them.

**`library-operator/media-browser`, the Rust browser:**

- `src/catalog/progress/parts.rs`: new, the split.
- `src/catalog/progress.rs`: declares the module.
- `src/catalog/local/progress.rs`: the read of each play's parts and
  the call to the split.
- `src/catalog/local/play.rs`: `episodes` answers the start of the
  chosen episode in its file.
- `src/catalog.rs` and `src/catalog/local.rs`: `Source::play` carries
  that start.
- `src/browser.rs`: `request_play` adds it to the start of an episode.
- `src/screens/upnext.rs`: `after_episode` follows the last part of
  the file, and `remaining_seconds` counts to the end of the file.

**`media-operator`:** no change.

## Tests

The tests follow the `testing` skill: real SQLite fixtures for the
browser's reads, the fake Jellyfin of `fakejellyfin_test.go` for the
role, and tables for the cases of one rule.

- `catalogschema_test.go`: `file_items` holds `part` and `parts` with
  their defaults.
- `seriesepisodenfo_test.go`: a double writes parts 1 and 2 of 2 and
  half the probe duration on each episode. A triple writes three
  parts. A file whose first marker number has no item still counts it
  in `parts`. A file with no probe duration keeps the `.nfo` duration.
- `catalog_test.go`: a link written again with new parts changes the
  row, and a link written again unchanged changes no row.
- `jellyfinindex_test.go`: a double answers `itemFor` for episode 1
  and episode 2, and an item with no `IndexNumberEnd` answers only
  episode 1. The fixture in `testdata/` is a listing answer that a
  real Jellyfin server gave for a double.
- `jellyfinoutbound_test.go`: a `Play` of episode 2 writes its file
  position to the double, and sets played only at the end of the file.
- `jellyfinmarks_test.go` and `jellyfinmarklist_test.go`: one table of
  the four mark cases above with their positions and played flags, a
  list of two parts of one item that makes one write, and an echo
  record at the file position.
- `jellyfinwebhook_test.go` and `jellyfinbackfill_test.go`: a post and
  a listing of a double record the first episode and the file
  position, which pins the behavior the read depends on.
- `src/catalog/progress/parts.rs`: one table of every row of the split
  table above, n of 3, a position exactly on a boundary, credits marks
  moved into the last part, and the row names in part order.
- `src/catalog/local/tests/progress/`: a new file of fixtures with a
  double and plays of each kind. `episode_progress` answers the rows
  of the stages table, a mark row reads unchanged, and the query plan
  takes `file_items_library_item`.
- `src/catalog/local/tests/plays.rs`: the play list of episode 2 of a
  double starts at the midpoint, episode 1 at 0, and a file with no
  duration at 0.
- `src/screens/series/tests/resume.rs` and `src/browser/tests`:
  Resume on episode 2 publishes the midpoint plus its position, and
  Play on episode 2 publishes the midpoint.
- `src/screens/upnext.rs` tests: the offer after episode 1 of a double
  is episode 3, and the remaining seconds of a part count to the end
  of its file.
- `src/catalog/progress/thread.rs` tests: the rows of one play stand
  the thread on the part in progress.

A drill on the `lab` fleet or a physical test cluster plays a double
from episode 1 across the midpoint, reads `--print-progress`, and
checks the series page and the next-up card at each stage of the
table. A drill against a real Jellyfin server checks that the works
listing carries `IndexNumberEnd`, and that a play and a mark of
episode 2 reach the double in both directions.

## The manual

- `guides/scanning.md:70-72`: a file of several episodes splits
  evenly. Each episode starts at its share of the file, and each
  episode's runtime is its share.
- `guides/browser.md`, sections 5 and 7: Play on a later episode of a
  file starts at that episode's share, one play continues to the end
  of the file, and the up-next card offers the work after the file.
- `guides/jellyfin.md:87-127`: a double is one item in Jellyfin, a play
  of either episode writes its file position there, played is set at
  the end of the file, and how a mark on each part is written.
- `reference/bus.md:253` and `reference/bus.md:257-262`: `start` is a
  second of the file, so a request for a later episode of a file
  carries that episode's start. A series still continues through
  `next`, and a file of several episodes is one `Play` from the chosen
  episode to its end.
- `reference/bus.md`, the mark fields: `position` and `duration` are
  the episode's own.

## Risks

- **The read is the one place.** Every screen depends on
  `progress::seen`, so a fault in the split shows on every screen at
  once. The table tests of `parts.rs` and the fixture tests of the read
  carry that risk.
- **A clear must stay a clear.** `since_clear` and the thread rule read
  a row at 0 as not started (`seen.rs:245-256`, `thread.rs:39-44`). A
  part that the split answered at 0 would read as a clear of that
  part. The split never answers a covered part at 0: it answers no row
  for it, and only a row at 0 answers 0 for its named part.
- **The cost of the read.** The read now finds the main file of every
  episode play, where today it does so only for a play past half of
  its work. A household holds a few thousand plays, and each lookup
  takes the index. The plan test checks the index, and a drill times
  the series page of a long series.
- **`IndexNumberEnd` in the listing.** The design assumes that
  Jellyfin's works listing returns `IndexNumberEnd` with the fields it
  asks for now. If it does not, the listing asks for it in `fields`.
  The drill against a real server settles it.
- **Two durations.** The file's duration is the probe's where it is
  known and the row's elsewhere. mpv and the probe can differ by a
  fraction of a second, which moves a boundary by that much.

## The order of work

Each commit builds and passes `make test` in `library-operator` on its
own, and updates the pages of the manual its change touches.

1. **The catalog records the parts.** The schema columns, `rows.go`,
   `series.go`, and `catalog.go`, with their tests, and the scanning
   guide. No reader reads the columns yet, so the screens do not
   change.
2. **The read splits the rows.** `parts.rs`, the read in
   `catalog/local/progress.rs`, and `--print-progress`, with their
   tests. A play of episode 1 and every Jellyfin row of a double now
   show progress on each episode.
3. **A play starts at its episode.** The start in `Source::play` and
   `request_play`, and `after_episode` and `remaining_seconds` in
   `upnext.rs`, with their tests, the browser guide, and the bus
   reference.
4. **Jellyfin finds each episode of a double.** `jellyfinapi.go` and
   `jellyfinindex.go`, with their tests. A `Play` of episode 2 now
   writes to Jellyfin.
5. **Marks reach the right seconds in Jellyfin.** `jellyfinmarks.go`,
   with its tests, and the Jellyfin guide.

## Later

- Chapter marks that name each episode can place each part's start in
  place of the even split. The columns of `file_items` and the split
  in `parts.rs` are where that fact would go.
- The display's header names the episode the `Play` started on for
  the whole play. Naming the episode at the playhead needs the
  presentation to carry the parts, which is a change to
  `media-operator`'s API.
