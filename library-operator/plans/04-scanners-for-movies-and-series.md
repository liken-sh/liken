# Scanners for movies and series

Plan 04. The scanner contract, and the first two kinds. At the end of
this plan a `Library` of movies and a `Library` of series each run a
scanner that reads the volume into the catalog, keeps the catalog
current as files arrive, and reports what it found.

## The problem

The volume already has what a catalog needs. A movie is a folder, `Title
[Year]`, with `movie.nfo`, `folder.jpg`, a backdrop, a logo, and a
`.trickplay` directory of thumbnail tiles beside the file, written by
Jellyfin. A series is a folder with `tvshow.nfo`, season folders, and an
`.nfo` per episode. The scanner reads that into the catalog and detects
when it changes. On the lab's volume, about a fifth of the movie folders
had no sidecar, so the scanner also reports what it could not identify.

## The scanner contract

A scanner is an image, run as one container in a `Library`'s pod. The
contract is the same for every kind.

- It reads the library's mount read-only, at the root the `Library`
  names. It writes nothing to the volume.
- It skips the paths the `Library` names to ignore, and everything
  under them, so a volume's non-media folders, a recycle bin or a
  staging directory, stay out of the catalog.
- It receives its `Library`'s name, kind, and settings block, and the
  addresses of its sidecar's API and the bus.
- It writes to the catalog only through its sidecar's transaction API,
  in batches. It updates rows in place rather than dropping and
  recreating them, so every peer's file changes by the rows that
  changed.
- It publishes a retained status report on the bus: titles, unidentified
  folders, the time of the last full walk, and the time of the last
  change applied. The operator folds that into `Library.status`.
- It detects changes two ways. It accepts a webhook on a small HTTP
  endpoint, of the kind Radarr, Sonarr, and Jellyfin send on import, and
  rescans the path the hook names. And it walks the whole root on a slow
  timer. It does not use `inotify`, which fires only for writes made
  through the same kernel and never for another client's writes to a
  network volume.
- It has no API-server credential.

The project ships one image per kind. A settings block may name another
image, which is how a person supplies a scanner of their own.

## The catalog is durable and pruned

The first drill added two elements to this plan.

The scanner keeps its catalog on a `PersistentVolumeClaim`, not an
`emptyDir`. The catalog is derived, and a rescan rebuilds it, but a
catalog held only on `emptyDir` volumes vanishes the moment no pod runs
one, and rebuilding a large catalog on every restart is work the volume
already answered. The scanner is the catalog's one writer and its source
of truth for a library, so its agent's catalog is durable: the scanner
starts from the catalog it last wrote. A screen's agent stays an
`emptyDir`, because a screen is a reader that re-syncs from the scanner.
This does not change the derived, provider-scoped id. The id is still
read off the volume, and the claim holds the derived catalog, not any
minted state.

The scanner prunes what the volume no longer holds. Because it starts
from the catalog it last wrote, it reconciles against what the catalog
holds, not against an in-memory record of one session's writes, so a
removal survives a restart and reaches every peer. A walk is two passes.
The scan pass reads the volume and upserts the items, files, and aliases
it finds. The prune pass reads the catalog and deletes every item, file,
and alias the scan pass did not see, and every one whose folder the
`ignore` list now names. So a title deleted from the volume, and a folder
that starts matching the `ignore` list, both leave the catalog on the
next walk.

## Movies

A movies library is one folder per title, at the root or under one level
of grouping folders, as the lab's volume groups by genre. For a folder with
no sidecar, the scanner reads the title and year from the folder name,
in the `Title (Year)` and `Title [Year]` forms the `*arr` tools and
Jellyfin write. A folder with `movie.nfo` takes its identity, plot, cast, set,
genres, and provider ids from it. The scanner records the folder's
art on the item, and the path of a file's `.trickplay` directory on the
file, so the media browser and the display draw them from the volume and
the scanner copies nothing. A folder with neither a sidecar nor a confident parse is
counted as unidentified and cataloged by its folder name, so it is
browsable and the count is accurate.

## Series

A series library is one folder per series, with `tvshow.nfo`, season
folders, and episode files with an `.nfo` each. The scanner reads the season from a `Season NN` folder and the episode
from an `s02e05` or `2x05` marker, the forms the `*arr` tools write.
The catalog has a series item and an episode item for each episode, and
each episode's file attaches to its episode item. The episode item has
the season and episode numbers, the aired date, and the episode's own
art and thumbnails, so the media browser draws series, then seasons,
then episodes from the catalog alone. A season is a grouping the media
browser draws from the episodes' season numbers, not an item of its own.

## The catalog's shape for these kinds

The catalog holds three kinds of row: items, files, and aliases. An item
is a logical work. A movie is one item. A series is an item, and each of
its episodes is an item under it. An item carries the header from plan
03, the columns every kind sorts on, and a body in the kind's own shape.
The movie body has what `movie.nfo` has. The series body has what
`tvshow.nfo` has, and the episode body has what an episode `.nfo` has.

A file is one physical file on the volume: its path, its technical
attributes, and the path of its own `.trickplay` directory. One item has
many files, because an upgrade to 4K or a second encoding is another
file and not another work.

An alias maps one of an item's ids to the item. Every provider id in a
folder's `.nfo`, and the folder's own name, become aliases of the item,
so several names resolve to one work and a lost sidecar still resolves
the folder.

An item's id is derived from the provider id in the `.nfo`, scoped by
kind: `movie:tmdb:603`, `series:tvdb:81189`, and
`episode:tvdb:81189:s02e05` for an episode. The scanner reads the id off
the volume and mints nothing. A folder with no provider id takes an id
derived from its folder name, so two sidecar-less folders that name the
same title fold to one item. This is the weak case: a move of a
sidecar-less folder breaks its id. The scanner sets the sort key,
so "The Matrix" sorts under M in every media browser, and a display
slug, so a URL and a screen read `the-matrix-1999` and not the id.

## The local harness

`local/` gains a script that runs a scanner against a directory of movies
on the workstation, into the local three-agent cluster, so a parser
change shows in a catalog without a cluster. The lab workstation has a
folder of movies with real sidecars for this.

## What was set aside

Copying art into the catalog. The art stays on the volume and the
catalog stores paths. The display already reads art from the volume for
a `Play`.

Generating thumbnails. Jellyfin's `.trickplay` tiles are its own layout.
The project will write WebVTT thumbnail sidecars of its own, and that is
the enrichment plan's job.

Writing a cleaner id back to the volume. The provider-scoped id is
stable for a title with a sidecar, but a sidecar-less folder's id rests
on its path, and a move breaks it. Writing a minted id back to the
volume as a durable fact would fix that. The project trusts the public
databases' ids for now and defers writing its own, recorded in
[`open-problems/`](open-problems/writing-ids-back-to-the-volume.md).

## Proof

On `liken-1`: a `Library` of the lab's movies reports a title count equal
to the folder count and an unidentified count equal to the folders with
no `.nfo`. A series library reports its series, and the catalog has the
right season and episode structure for one series checked by hand. A
file added to the volume and announced by a webhook appears in the
catalog within a few seconds. A file added with no webhook appears after
the next slow walk. The scanner's catalog survives a restart of its pod.
A title deleted from the volume, and a folder added to the `ignore` list,
both leave the catalog on the next walk. The catalog's `state.db` size for the lab's movies
and series is recorded in the completed plan, because plan 06 budgets
against it.
