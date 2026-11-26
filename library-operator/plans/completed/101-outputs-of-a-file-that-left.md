# 101, Outputs of a file that left

Proposed and built on 2026-11-26. No drill on a cluster has run yet.

## The problem

A download manager that takes a better release of a film writes the new
video under a new name and deletes the old one. For example,
`The Long Survey (1961) Bluray-1080p.mkv` becomes
`The Long Survey (1961) WEBDL-1080p.mkv`. These outputs of the old
video stay in the title folder:

- `The Long Survey (1961) Bluray-1080p.trickplay/`, its tile directory.
  The walk still writes a `files` row for it, linked to the film
  (`folderfiles.go`), so the browser lists it with the film's files.
- `.liken/appearances/<old video>.jsonl`, `.matches.json`, and
  `.spans.json`, the appearances tool's detections record and the two
  files that its match writes.
- `.liken/titles/<old video>.jsonl`, the titles tool's reading.

A writer that is stopped in the middle of its work also leaves a
temporary on the volume, such as a trickplay staging named
`.<video>.trickplay.liken-tmp-<writer>`. Each `Job` has a new writer
name, so no later run clears it. The close container removes stale
temporaries only under `.liken/` (`stalepartials.go`).

Each of these is a cache of one video. A tool makes it again from the
video, so a wrong remove costs a decode and never data. When the video
is gone, no program reads the output. Jellyfin itself tries to delete
an item's tile directory when it removes the item.

## Who is responsible for what

[Plan 27](27-enrichment.md) says that the operator never removes a file
it did not write, and that a stale file stays in place for a person to
delete. This plan replaces that rule with one of responsibility. The
operator works beside the download managers and Jellyfin, and each
program is responsible for some kinds of file:

- The operator is responsible for the kinds of output that it derives
  from one video: the tile directory, `<video base>.trickplay/`, and
  its records in `.liken/`. When the video is gone, it removes those
  outputs, whoever wrote the file. A tile directory that Jellyfin
  wrote is the same kind of output, and it serves no video either.
- The other programs are responsible for the videos, a download
  manager's extras, an episode's `-thumb.jpg`, which Sonarr adopts and
  deletes on an upgrade, and the `.nfo` file of each video. The
  operator leaves those kinds alone.

Every remove still goes through a door in `volumewrite.go`, and
`TestOnlyTheWritePackageRemovesRenamesOrTruncates` still fails the
build on a remove anywhere else.

## The design

**When an output goes.** A full walk finds the outputs, and the close
container removes them through the doors, which check again. An output
goes when all three of these hold:

1. Its video is not there. `Lstat` of `<video>`, `<video>.backup~`, and
   `<video>.partial~` each answer that it does not exist. Radarr and
   Sonarr move a file to `<video>.backup~` while they change the case
   of its name. Older releases are said to copy to `.partial~`.
2. No video in the same directory derives the same output name. A tile
   directory is named for the video without its extension, so
   `The Long Survey (1961).mkv` and `The Long Survey (1961).mp4` share
   one. The comparison ignores case, and a name with one of the two
   suffixes counts as its video. A video here is a file with one of the
   extensions of Jellyfin 12.4's own list, which is wider than the set
   the walk catalogs, so a `.vob` or an `.iso` keeps its tiles. A file
   of the same name with another kind of extension, such as the `.nfo`
   that a download manager leaves, is not a video.
3. The title still has a video. The title is the film's folder and its
   extras folders, or the series folder and its season folders. An
   upgrade always leaves a video. A folder that is still arriving, and
   an outage that hides the videos, leave none.

The door refuses a symbolic link, an entry that is absolute or has a
`..` element, and an entry deeper than an extras or season folder and
a file. A record goes with one unlink. A tile directory is renamed to a
temporary name and then removed, so a tree that is half removed has a
temporary name, and the temporary sweep removes what is left.

**Only after a complete walk.** The scan writes its note of outputs for
the close container only when `fullWalk` returned no error, and only
after `awaitCatalogSync` against a target with an actor. The scan of
every walk `Job` takes that target, whether or not the `Library` names
webhooks, and a rescan does not wait for it. A webhook
rescan, a failed walk, and a first walk write no note, so they remove
nothing. The close container logs one line for each run with the counts
of what it removed, what it kept, and what the volume refused. A
read-only volume counts refusals and never fails the `Job`.

**Temporaries.** The walk notes each name that contains `.liken-tmp-`
in a title, season, extras, or `trailers/` folder, a file or a
directory, with or without the leading dot that releases before
2026-10-30 left out. The close container removes it when it is older
than 48 hours, twice `factWorkerDeadline`. The age counts from the
newest modified time or change time in the tree, because ffmpeg writes
into the layout folder while it decodes, and a rename sets the change
time. The aside tree of `replaceEarlierTree`,
`.<video>.trickplay.earlier.liken-tmp-<writer>`, stays while its tile
directory is missing and a video still derives that directory's name,
because it is then the only copy of that video's tiles.
The sweep under `.liken/` keeps its rule. The walk and the close
container take the time as an argument, so a test outside a `synctest`
bubble ages a change time that the kernel set.

**The catalog.** The walk writes no `files` row for a `.trickplay`
directory with no video of its base name, by rule 2. The player reads a
video row's `trickplay` column and never the directory's own row.

## Set aside

A first design recorded what each writer landed, the inode and the
times of each output, kept a ledger of when each video went missing,
counted the walks that saw it gone, guarded against many videos
vanishing at once, and moved a record to a video that could be the
same file under a new name. Two reviews made it safe, and it grew to
four doors and two new ledgers. Every output that it protected is a
cache that a decode makes again, so the protection cost more than the
mistakes it stopped. A person could not have followed one of its
removals at 2 in the morning. The three checks above are each one
`Lstat` or one listing, and the log line says what happened.

## Not in this plan

- The `-thumb.jpg` of an episode and the `.nfo` file of a video, which
  other programs are responsible for.
- The files of a title whose folder has no video left.
- The ledger entries of a gone video, which the walk does not lift and
  which `dropGone` removes at the ledger's next write.

## Decisions

The maintainer approved this design on 2026-11-26, in place of the
heavier one under "Set aside". The operator removes the outputs it is
responsible for, for a video that is gone from a title that still has a
video.

## How the work is proved

Table tests of each door cover each refusal: a video that is there, or
there as `.backup~` or `.partial~`; a video with another extension or
in another case that derives the same tile directory; a title with no
video left; a symbolic link; an absolute entry, a `..` element, and an
entry too deep; and a read-only directory. The temporary door removes a
stale staging with and without the leading dot, and keeps a fresh one,
a name without the mark, and an aside tree whose tile directory is
missing. A pass test runs a full walk and the close container, and
checks that a webhook rescan removes nothing. A walk test checks that
an orphaned tile directory has no `files` row.

A drill on `liken-1` replaces a film's video with a file of another
name, and checks that the next full walk removes the old tile directory
and records and logs the counts.
