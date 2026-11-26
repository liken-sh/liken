# 100, The change of each item and its previous videos

Proposed and built on 2026-11-26. The operator's tests pass. No drill
has run on a cluster.

## The problem

The body that a walk posts to `spec.webhooks` (plan 99) says a title is
`added` when every video that the title has after the walk is a video
that the walk added by path. A download manager that upgrades a film
writes the new file at a new path and deletes the old one, such as
`The Long Survey [1999, Bluray-1080p].mkv` replaced by
`The Long Survey [1999, WEBDL-1080p].mkv`. After the walk, the film has
only the new video, so the body reports the film as `added`, and a
receiver announces a film that the library already had. Plan 99 says
that a rename makes the title `changed`. That is true only when the
title kept another video.

A series has the same fault on each episode. A new episode and an
upgraded episode both make the series `changed`, with an `added` file,
so a receiver cannot tell them apart.

The body also carries only the values after the walk. A receiver that
wants to say what an upgrade changed, such as "1080p to 2160p" or "new
audio track", has nothing to compare with. The walk's prune deletes the
rows of a video that left, so the close container cannot read them
after the walk.

## The change of each item

The body gives a `change` to each item: each movie, and each episode of
a series. The value is one of these:

- `added`: the catalog held no video of the item before the walk.
- `replaced`: a video of the item left, or a video at the same path
  changed its size or its modified time, and a video arrived.
- `version-added`: every video that the item had before the walk is
  still there, unchanged, and another video arrived, such as a 2160p
  encoding beside a 1080p one.

`replaced` takes precedence over `version-added`: an item that keeps
its 2160p video and gets a new 1080p video in place of the old one is
`replaced`. The body lists only items to which a video arrived. An
item that only lost a video is not in the body, as in plan 99.

The classification reads only the catalog: the videos, their size and
modified time, and the items that `file_items` links them to. It reads
nothing from a file name, and it knows nothing about Radarr, Sonarr, or
any other writer.

The `change` of a movie title is the change of the movie. The `change`
of a series title is `added` when the catalog held no video of any
episode of the series before the walk, and `changed` otherwise. Each
entry of the series' `episodes` has its own `change`. The values
`added` and `changed` of a movie title go away.

Each file keeps its own `change`, `added` for a new path and `replaced`
for a path whose size or modified time changed, and a file that the
walk did not change has none, as in plan 99.

## The previous videos

Each item in the body has `previous`, a list of the videos that the
walk took away from it. That is each video that left the item, and
each video at the same path that the walk replaced. Each entry has the
same form as an entry of `files`, with the values that the catalog held
before the walk: its `row` from `files`, and its `streams` and `marks`.
A receiver compares an entry of `previous` with the entries of `files`
to say what changed. The list is empty for an `added` item and for a
`version-added` item. A video that held several episodes is in the
`previous` of each of them.

```json
{
  "change": "replaced",
  "table": "movies",
  "row": { "id": "movie:tmdb:1001", "title": "The Long Survey", "...": "..." },
  "files": [
    { "change": "added", "row": { "path": "The Long Survey (1999)/webdl.mkv", "height": 1080, "...": "..." },
      "streams": [], "marks": [] }
  ],
  "previous": [
    { "row": { "path": "The Long Survey (1999)/bluray.mkv", "height": 1080, "...": "..." },
      "streams": [], "marks": [] }
  ],
  "genres": [], "credits": [], "trailers": [], "set": null, "franchises": []
}
```

## The capture before the walk

The scan writes two notes on the phases volume before it walks, after
its copy of the catalog has synced, by the same wait that plan 99 uses.

1. `videos-before.json` gains the items of each video. For every video
   of the library, it holds the path, the size, the modified time, and
   the ids that `file_items` links to the path. The close container
   classifies each item from it.
2. `rows-before.json` holds the whole `files` row of a video, with its
   `streams` and `marks` rows, for each video under the folders that a
   `folders` walk names. The scan reads the paths with the path range
   that the prune of a folder uses (`pathScopeClause` in `prune.go`).
   It then reads the rows of 100 videos at a time, one query for each
   table, and writes them to the note before it reads the next batch.

The note of rows has a budget of 4 MB for the whole walk. The webhooks
can name up to 64 folders, and a series folder can hold a thousand
episodes, so whole rows of every named video can be more than the scan
container's limit of 128Mi and the close container's limit of 64Mi.
When the next video would pass the budget, the scan keeps no more rows.
The close container keeps each video's rows encoded and decodes only
the videos that it puts in a `previous` list. It runs with a
`GOMEMLIMIT` of 32MiB, 32Mi under its limit, as the scan container does.

A `full` walk writes no `rows-before.json`. A `folders` walk keeps no
rows for a path that the scan cannot map onto a title folder of the
volume, for a video outside the folders it names, and for a video past
the budget. For a video whose rows the scan did not keep, the `row` of
its entry in `previous` holds only `path`, `size_bytes`, and
`modified`, from the first note, and `streams` and `marks` are `null`.
Download managers name the folder they import into, so the upgrades
that a receiver announces arrive in `folders` walks.

A `folders` walk that names a path the scan cannot map walks the whole
root. After the walk, the scan writes a third note, `walk-scope`, with
the scope it walked, and the body names that walk `full`. The scan
kept the rows of the named folders that it could map before the walk,
so a `full` body of that kind carries whole rows for the videos under
those folders.

These are the sizes for a library of 3,000 videos, from the width of
the rows. A `files` row is about 1 KB of JSON, with 24 columns, and a
`streams` row is about 0.7 KB, with 27 columns. A video has from 5 to
15 streams, so one video's rows are from 5 to 12 KB.

- `videos-before.json` is about 170 bytes for each video, so about
  500 KB. Today's note is about 100 bytes for each video.
- `rows-before.json` for a `folders` walk of one film folder is from
  10 to 30 KB. For a series folder of 200 episodes, it is about 2 MB.
- Whole rows for every video would be from 15 to 36 MB on each full
  walk, which is the reason that a full walk writes none.

The build measured the notes against a test catalog of generated
rows. Each video had a full `files` row, 12 streams (1 video, 3 audio,
and 8 subtitle), and 3 marks.

- `videos-before.json` for 3,000 videos was 498,781 bytes, 166 bytes
  for each video.
- `rows-before.json` for one film folder was 7,756 bytes.
- `rows-before.json` for a series folder of 200 episodes was 1,540,201
  bytes.
- Whole rows of all 3,000 videos were 23,549,041 bytes, 7,849 bytes for
  each video.

## Items whose id changed

The walk can change an item's id. A title whose `.nfo` file the
download manager deleted has an id of the form `movie:path:<key>` until
the identity phase writes the provider's id again. The close container
matches each id from the first note against the item's aliases, which
map every provider id of a movie or a series and its folder key to the
item's current id. So a prior id that is an alias of the current item
counts as that item.

An episode has no aliases. Its id is built from its series' id, its
season, and its number (`episodeID` in `rows.go`). So an episode's
prior ids are the ids built from each alias of its series, and an
episode whose series changed from `series:path:<key>` to
`series:tvdb:<id>` in the walk counts as the same episode.

These cases are out of scope, and the item reads as `added`:

- An id changes from a provider id to the folder key, such as
  `movie:tmdb:1001` to `movie:path:<key>`, and the video is renamed in
  the same walk. That happens when the `.nfo` file is gone and identity
  cannot write it again. The new item's only alias is its folder key,
  and the walk removed the aliases of the old id.
- The new `.nfo` file names other provider ids than the old one, and
  the folder was renamed in the same import.

## A video deleted before its replacement arrives

A walk can run after the download manager deleted the old video and
before the new video is on the volume, for example when the sender's
delete event starts a walk of its own. That walk prunes the old video
and posts nothing, because the item only lost a video. The next walk
then reads the new video as `added`. A fix is out of scope.

## Set aside

- **Capture the rows at the prune.** The prune knows each `files` row
  that it deletes, so it could write those rows to a note, and a full
  walk would carry whole previous rows too. But a video replaced at
  the same path is not pruned: the walk writes the new row over the
  old one. So a capture at the prune needs a second capture at the
  write, in two places of the walk and in the identity phase, which
  walks the folder again.
- **Compare file names.** A receiver that compares the two names, such
  as `Bluray-1080p` and `WEBDL-1080p`, depends on one writer's naming.
  The rows carry the resolution, the codecs, and the streams, which
  every writer's files have.
- **Keep the title-level `added` and `changed` for movies.** A movie is
  one item, so its title-level value would only repeat the item's
  value with less detail.

## Proof

The operator's tests cover each change of an item: a film whose only
video is renamed, a film that gets a second encoding, a film whose
video is replaced at the same path, an episode that is new beside an
episode that is upgraded, a file of two episodes, and an item whose id
changed from its folder key to a provider id. They cover the
`previous` entries with whole rows in a `folders` walk and with the
three columns in a `full` walk, past the budget, and with a note of
rows that is missing. They cover an episode whose series changed its
id, a file that stops being a video, a note of rows that does not
parse, a `folders` walk that read the whole root, the order of the
notes and the walk in the scan phase, the reads of the rows in
batches, and the shape of the document. A drill on a cluster upgrades a film
in Radarr and reads the body that the receiver got.
