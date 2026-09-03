# Every fact writes its rows

Plan 34. Shaped on 2026-09-03 after plan 30's first walk with credits.
Today a fact writes files and nothing else; the next walk reads those
files into the catalog. This plan makes each fact write its own rows
the moment it writes its files, and lets the phases that do not share
a file run at the same time.

## The problem

The catalog learns what a fact wrote one walk late. A poster the art
phase writes at 10:05 reaches the browser at 11:00, when the next scan
reads the folder. The contributors phase reads its gaps from the
`contributors` table, and the only writer of that table is the scan,
so the people the credits fact creates in one run get their ids,
biographies, and headshots in the next run, an hour later.

Plan 30 drew the art, contributors, and trickplay phases as regular
containers running at once, and built them as init containers in a
row, because the enrich container must run last and nothing told it
when a regular container beside it was done.

Both problems have one root: the scan is the only writer of rows.
That rule kept one way to make a row. This plan keeps that, and adds
writers.

## The rules

**Files are the truth.** A fact writes its files first, as it does
today, through the write door. Jellyfin, Kodi, and a person edit those
files behind us, and the scan reads them all back on every walk. The
scan stays the reconciler: it reads every folder, marks what it saw,
and prunes what it did not.

**A fact writes the rows for what it wrote.** After a fact writes a
folder, it writes the rows that folder's files produce, through the
same reading code the scan uses on that folder. One reader, two
callers. A fact never writes a row from what it holds in memory,
because a second way to make a row is a second place for the two to
differ.

**A fact writes only the columns it owns.** Corrosion merges a row per
column, last writer wins. Two phases that update different columns of
one row never collide, in any order. Two phases that write the same
column race, and the slower one lands a stale value. So every column
has one owner, the table below, and a fact's statement names its own
columns and no other. There are no whole-row upserts outside the scan.

**The sweep spares what a run is writing.** The scan's prune deletes
rows the walk did not mark. A row a fact writes while a walk is under
way carries no mark. The prune skips a row whose file is newer than
the walk's start, so a poster written mid-walk survives to the next
walk, which marks it.

## The owners

| Table, column | Owner | Its write |
|---|---|---|
| `files`: `video_codec`, `audio_codec`, `width`, `height`, `duration_ms` | probe | one `UPDATE` per file it probed |
| `movies`/`episodes`: `duration` | probe | one `UPDATE` per item whose sidecar had no runtime |
| `movies`/`series`: `id`, `title`, `released`, `slug`, `sort_key`, `set_id`; `aliases`; `episodes`: `id`, `series` | identity | the folder rescan: read the folder into rows, upsert, prune the folder's old rows |
| `movies`/`series`/`episodes`: `body`, `nfo_facts` | nfo | one `UPDATE` per folder, from a re-parse of the sidecar it wrote |
| `credits` | nfo (credits fact) | delete the item's rows, insert the new set |
| `contributors`, `contributor_aliases`: rows for an entry it created | nfo (credits fact) | one insert per new person, none for a person that exists |
| `movies`/`series`/`episodes`: `art`, `arts` | art | one `UPDATE` per title after each image lands |
| `files`, `file_items`: the image's own row and link | art | one upsert per image written |
| `contributors`: `born`, `died`, `biography`, `headshot`; `contributor_aliases`: new ids | contributors | one `UPDATE` or insert per person per fact |
| `files`: `trickplay` | trickplay | one `UPDATE` per video |
| `attempts`: the row for (item, fact) | the fact that attempted | one upsert per attempt, with the ledger write |
| `runs` | the closer | one row at the end, as today |
| everything else | the scan | the whole-row upserts and the prune, as today |

`arts` is new: the list of art paths that today sits inside `body`
moves to its own column, because `body` belongs to the nfo phase and
the list belongs to the art phase, and the two run at once. The scan
writes both. The browser reads `arts` where it read the list in
`body`.

The identity fact is the one that rescans a folder rather than
updating a column, because the id is the key of every other row of
the title: the files, the aliases, the attempts, the credits. The scan
already reads one folder into rows for the webhook, and identity calls
that.

## The phases

```yaml
kind: Job
spec:
  template:
    spec:
      initContainers:
        - name: corrosion            # native sidecar, restartPolicy: Always
        - name: probe                # writes streamdetails into the sidecar
        - name: identity             # writes the ids into the sidecar
      containers:
        - name: nfo                  # edits the sidecar body; no other container does
        - name: art                  # needs the ids; nothing else
        - name: contributors         # needs the people nfo's credits fact creates
        - name: trickplay            # needs the probe; nothing else
        - name: enrich               # the closer: waits for the marks, writes the runs row
      volumes:
        - name: phases               # emptyDir, one mark per finished phase
```

Probe and identity stay in a row, before everything, because the ids
they write are what every other phase asks a provider with. The nfo
phase is the only writer of the sidecar body, so it runs alone on that
file and beside everything else.

A phase runs its gap loop until the loop finds nothing and every phase
it depends on is done, then writes its mark, a file named for the
phase on the shared `emptyDir`. Art and trickplay depend on nothing
in the fan-out, so they run once. Contributors depends on nfo: it
loops, and each pass finds the people the credits fact has created
since the last pass, because those rows are already in the pod's own
catalog. It stops when nfo's mark is there and a pass finds nothing.

The closer waits for every mark the Job named in its environment, then
writes the runs row and waits for the echo, as today. The marks are
files and not rows because a file on the pod is instant and needs no
poll of the catalog.

## What it costs

A fact's row write is a read of the folder it just wrote, a handful of
small files on a disk that just took the fsync, and one statement to
the pod's own Corrosion agent. Next to a provider call or an image
download it is noise. The first pass of trickplay and art over a
library writes thousands of folders, and each pays the same small
price.

The scan's walk is unchanged. It still reads every folder every hour,
and on a folder a fact wrote minutes ago it writes the same values
again, which Corrosion folds into nothing.

## Proof

On `liken-1`, against the lab libraries. A stripped title, metadata
only, comes back whole, and the browser shows its poster before the
next hourly scan. The contributors phase reports people written in the
same run that created them. A run's `runs` row starts at probe and
ends after the last phase's mark, and the enrich Job's containers show
nfo, art, contributors, and trickplay running at once. A walk started
while an enrich Job is writing does not prune the rows the Job wrote.
The rebuild drill still holds: delete the catalog's claim, roll, walk,
and the counts match, because every row still comes from a file.

## What is not decided

Whether the art list inside `body` has readers outside the browser.
The build finds every reader and points it at `arts`.

Whether a phase's gap loop needs a floor between passes when its
upstream is slow. The contributors phase polling an empty gap every
second while nfo works through a thousand titles is cheap but
pointless; a short sleep between empty passes is the likely answer.
