# Scanners for films and series

Plan 04. The scanner contract, and the first two kinds. At the end of
this plan a `Library` of films and a `Library` of series each run a
scanner that reads the share into the catalog, keeps it current as
files arrive, and reports what it found.

## The problem

The share already holds what a catalog needs. A film sits in its own
folder, `Title [Year]`, with `movie.nfo`, `folder.jpg`, a backdrop, a
logo, and a `.trickplay` directory of thumbnail tiles beside it,
written by Jellyfin. A series holds `tvshow.nfo`, season folders, and
an `.nfo` per episode. The scanner's job is to read that, and only
that, into the catalog, and to notice when it changes. On the lab's
share, about a fifth of the film folders had no sidecar yet, so a
scanner also has to say what it could not identify.

## The scanner contract

A scanner is an image, run as one container in a `Library`'s pod,
with a contract that is the same for every kind:

- It reads the library's mount, read-only, at a root the `Library`
  names. It writes nothing to the share in this plan.
- It receives its `Library`'s name, kind, and kind block, and the
  addresses of its sidecar's API and the bus.
- It writes to the catalog only through its sidecar's transaction
  API, in batches, and it updates rows in place rather than dropping
  and recreating, so every peer's file changes by the rows that
  changed.
- It publishes a retained status report on the bus: titles,
  unidentified folders, the time of the last full walk, and the time
  of the last change applied. The operator folds that into
  `Library.status`.
- It is told of changes two ways, and never through `inotify`, which
  NFS does not deliver for another client's writes. It accepts a
  webhook on a small HTTP endpoint, the kind Radarr, Sonarr, and
  Jellyfin send on import, and rescans the path the hook names. And
  it walks the whole root on a slow timer as the backstop.
- It has no API-server credential.

The project ships one image per kind. A kind block may name another
image, which is how a person brings a scanner of their own.

## Films

A films library is one folder per title, at the root or under one
level of grouping folders, the way the lab's share groups by genre.
The kind block holds the naming convention for a folder with no
sidecar, in the form the `*arr` tools use, so a folder can still be
identified as a title and a year. A folder with `movie.nfo` takes its
identity, plot, cast, set, genres, and provider ids from it. The
scanner records the paths of the art it finds beside the file, and
the path of the `.trickplay` directory when there is one, so the
browser and the display can draw them without the scanner copying
anything. A folder with neither a sidecar nor a confident parse is
counted as unidentified and still cataloged by its folder name, so
it is browsable and the count is honest.

## Series

A series library is one folder per series, with `tvshow.nfo`, season
folders, and episode files with an `.nfo` each. The kind block holds
the season folder and episode naming conventions, again in the
`*arr` form. The catalog holds a series row and an episode row per
file, with the season and episode numbers, the aired date, and the
episode's own art and thumbnails, so the browser can draw series,
then seasons, then episodes from the catalog alone.

## The catalog's shape for these kinds

Both kinds share the item header from plan 03. The film body carries
what `movie.nfo` holds. The series body carries what `tvshow.nfo`
holds, and the episode row carries what an episode `.nfo` holds. The
scanner, not the browser, decides the sort key, so "The Matrix" sorts
under M in every browser the same way.

## The local harness

`local/` gains a script that runs a scanner against a directory of
films on the workstation, into the local three-agent cluster, so a
parser change shows in a catalog without a cluster. The lab
workstation holds a folder of films with real sidecars for this.

## What was set aside

Copying art into the catalog. The art stays on the share and the
catalog holds paths; the display already reads art from the share
for a `Play`.

Generating thumbnails. Jellyfin's `.trickplay` tiles are its own
layout, and the project will write its own WebVTT thumbnail sidecars
one day, but that is the enrichment plan's job, not the scanner's.

Watching the share with `inotify` from inside the pod. It fires only
for the pod's own writes.

## Proof

On `liken-1`: a `Library` of the lab's films reports a title count
that matches the folder count and an unidentified count that matches
the folders with no `.nfo`. A series library reports its series, and
the catalog holds the right season and episode structure for one
series checked by hand. A file added to the share and announced by a
webhook appears in the catalog within a few seconds; a file added
with no webhook appears after the next slow walk. The catalog's
`state.db` size for the lab's films and series is recorded in the
completed plan, because the cache and memory settings in plan 06
rest on it.
