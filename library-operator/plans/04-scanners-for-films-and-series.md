# Scanners for films and series

Plan 04. The scanner contract, and the first two kinds. At the end of
this plan a `Library` of films and a `Library` of series each run a
scanner that reads the volume into the catalog, keeps the catalog
current as files arrive, and reports what it found.

## The problem

The volume already has what a catalog needs. A film is a folder,
`Title [Year]`, with `movie.nfo`, `folder.jpg`, a backdrop, a logo,
and a `.trickplay` directory of thumbnail tiles beside the file,
written by Jellyfin. A series is a folder with `tvshow.nfo`, season
folders, and an `.nfo` per episode. The scanner reads that into the
catalog and detects when it changes. On the lab's volume, about a
fifth of the film folders had no sidecar, so the scanner also reports
what it could not identify.

## The scanner contract

A scanner is an image, run as one container in a `Library`'s pod. The
contract is the same for every kind.

- It reads the library's mount read-only, at the root the `Library`
  names. It writes nothing to the volume.
- It receives its `Library`'s name, kind, and settings block, and the
  addresses of its sidecar's API and the bus.
- It writes to the catalog only through its sidecar's transaction
  API, in batches. It updates rows in place rather than dropping and
  recreating them, so every peer's file changes by the rows that
  changed.
- It publishes a retained status report on the bus: titles,
  unidentified folders, the time of the last full walk, and the time
  of the last change applied. The operator folds that into
  `Library.status`.
- It detects changes two ways. It accepts a webhook on a small HTTP
  endpoint, of the kind Radarr, Sonarr, and Jellyfin send on import,
  and rescans the path the hook names. And it walks the whole root on
  a slow timer. It does not use `inotify`, which fires only for writes
  made through the same kernel and never for another client's writes
  to a network volume.
- It has no API-server credential.

The project ships one image per kind. A settings block may name
another image, which is how a person supplies a scanner of their own.

## Films

A films library is one folder per title, at the root or under one
level of grouping folders, as the lab's volume groups by genre. The
settings block holds the naming convention for a folder with no
sidecar, in the form the `*arr` tools use, so the folder still yields
a title and a year. A folder with `movie.nfo` takes its identity,
plot, cast, set, genres, and provider ids from it. The scanner records
the paths of the art beside the file and the path of the `.trickplay`
directory, so the browser and the display draw them from the volume
and the scanner copies nothing. A folder with neither a sidecar nor a
confident parse is counted as unidentified and cataloged by its folder
name, so it is browsable and the count is accurate.

## Series

A series library is one folder per series, with `tvshow.nfo`, season
folders, and episode files with an `.nfo` each. The settings block
holds the season folder and episode naming conventions, in the `*arr`
form. The catalog has a series row and an episode row per file. The episode
row has the season and episode numbers, the aired date, and the
episode's own art and thumbnails, so the browser draws series, then
seasons, then episodes from the catalog alone.

## The catalog's shape for these kinds

Both kinds use the item header from plan 03. The film body has what
`movie.nfo` has. The series body has what `tvshow.nfo` has, and the
episode row has what an episode `.nfo` has. The scanner sets the sort
key, so "The Matrix" sorts under M in every browser.

## The local harness

`local/` gains a script that runs a scanner against a directory of
films on the workstation, into the local three-agent cluster, so a
parser change shows in a catalog without a cluster. The lab
workstation has a folder of films with real sidecars for this.

## What was set aside

Copying art into the catalog. The art stays on the volume and the
catalog stores paths. The display already reads art from the volume
for a `Play`.

Generating thumbnails. Jellyfin's `.trickplay` tiles are its own
layout. The project will write WebVTT thumbnail sidecars of its own,
and that is the enrichment plan's job.

## Proof

On `liken-1`: a `Library` of the lab's films reports a title count
equal to the folder count and an unidentified count equal to the
folders with no `.nfo`. A series library reports its series, and the
catalog has the right season and episode structure for one series
checked by hand. A file added to the volume and announced by a
webhook appears in the catalog within a few seconds. A file added
with no webhook appears after the next slow walk. The catalog's
`state.db` size for the lab's films and series is recorded in the
completed plan, because plan 06 budgets against it.
