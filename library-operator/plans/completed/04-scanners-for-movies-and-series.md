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
  folders, the time of the last full walk, the time of the last change
  applied, and the count of rows the last sweep removed. The operator
  folds that into `Library.status`.
- It detects changes two ways. It accepts a webhook on a small HTTP
  endpoint, of the kind Radarr, Sonarr, and Jellyfin send on import, and
  rescans the path the hook names. And it walks the whole root on a slow
  timer. It does not use `inotify`, which fires only for writes made
  through the same kernel and never for another client's writes to a
  network volume.
- A webhook prunes within the scope of the path it names. If the folder
  still exists, the rescan upserts it and deletes only that title's rows
  the rescan did not produce, such as a file an upgrade replaced. If the
  folder is gone, the item and its files and aliases leave the catalog. A
  webhook cannot see a title that vanished with no hook, so a removal that
  arrives with no event waits for the next full walk.
- It has no API-server credential.

The project ships one image per kind. A settings block may name another
image, which is how a person supplies a scanner of their own.

## Marked and swept

The scanner reconciles against the catalog, not against an in-memory
record of one session's writes, so a removal survives a restart and
reaches every peer. The catalog is the namespace's shared store, and
where it lives and how large each agent's copy is are the `Catalog`
object's concern, in [the namespace catalog plan](15-the-namespace-catalog.md).
This does not change the derived, provider-scoped id: the id is read off
the volume, and the store holds the derived catalog, not any minted
state, so a full catalog loss re-derives the same rows by a rescan.

A library can hold millions of items, more than the
scanner can hold in memory, so neither the scan nor the prune can load
the whole set at once. Both stream. The scanner marks and sweeps. Each
full walk takes an epoch. The scan pass streams the volume one title
folder at a time: it reads a folder, upserts that folder's items, files,
and aliases, and marks each with the current epoch, then moves on and
holds no more than one folder. The prune pass then deletes every catalog
row the walk did not mark this epoch, and every row whose folder the
`ignore` list now names. So a title deleted from the volume, and a folder
that starts matching the `ignore` list, both leave the catalog on the
next walk, and the scanner never holds more than one title folder in
memory.

The mark lives in a local table the catalog agent does not gossip, a
`seen` table beside the replicated `items`, `files`, and `aliases`. A
mark on a replicated row would gossip to every reader on every walk, and
a new column on a populated cr-sqlite table backfills a clock row for
every existing row. The scanner creates `seen` at runtime, not in the
schema file, because Corrosion makes every table a schema file names a
replicated table. A table created through the write API stays a plain
local table. So the local table carries the epoch, a walk marks freely,
and the readers see only real content changes and real deletions.
The prune streams the ids the current epoch did not mark out of the
catalog with a bounded query, and deletes them by id in batches. It holds
no full key set in memory, whatever the library's size. A single
statement that deletes by joining the local table would save the read
pass, but Corrosion's write API may not run a delete that reads another
table, so the scanner does not depend on it.

An upsert writes each row with `INSERT ... ON CONFLICT DO UPDATE`, never
`INSERT OR REPLACE`. A replace is a delete and an insert under cr-sqlite,
which writes a tombstone and bumps every column even when the row did not
change. `ON CONFLICT DO UPDATE` takes cr-sqlite's compare-and-skip path,
so a row the walk re-reads unchanged gossips nothing. For the same
reason, and because a changed primary key is a delete and a create, the
scanner keeps its derived ids and file paths stable across walks.

The prune runs only after a clean, complete walk. A walk reads a network
volume, and a walk that fails partway marks only the rows it reached, so
a prune then would delete every row the walk did not reach. A read error
at the root, or a walk that returns far fewer rows than the catalog
holds, aborts the prune for that pass and keeps the rows. The next clean
walk prunes. The report carries the count of rows the last sweep removed,
so a false mass delete is visible on the bus without a shell.

A `Library` spec change reaches the scanner as a pod roll. The scanner's
container carries the root, the `ignore` list, and the settings block in
its environment, so a change to any of them changes the pod template. The
operator stands the scanner pod itself and replaces it when the template
hash changes, and the new pod's startup walk is the new scan. The
replacement is also the queue: a scan already in flight ends with its
pod. The catalog volume the new pod mounts, and the handoff as one pod's
agent releases it, are the `Catalog` object's concern.

## Movies

A movies library is one folder per title, at the root or under grouping
folders. A grouping folder holds no `movie.nfo` and no video, so the walk
descends into it and keeps descending until it reaches title folders. This
finds a title a volume nests under a genre and then a studio, and a
grouping folder is never a title itself. For a folder with
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
[`open-problems/`](../open-problems/writing-ids-back-to-the-volume.md).

## Proof

On `liken-1`: a `Library` of the lab's movies reports a title count equal
to the folder count and an unidentified count equal to the folders with
no `.nfo`. A series library reports its series, and the catalog has the
right season and episode structure for one series checked by hand. A
file added to the volume and announced by a webhook appears in the
catalog within a few seconds. A file added with no webhook appears after
the next slow walk. A title deleted from the volume while the scanner was
down leaves the catalog on the first walk after the scanner restarts,
because the walk reconciles against the catalog and not against memory.
A title deleted from the volume, and a folder added to the `ignore` list,
both leave the catalog on the next walk, and the report's removed count
equals the rows that left. A change to the `Library` spec rolls the pod,
and the new pod's startup walk reflects the change. A walk that fails
partway, forced by an unreadable root, prunes nothing and keeps the
catalog. The catalog's `state.db` size for the lab's movies and series is
recorded in the completed plan, because plan 06 budgets against it.

## What the build and the drill found

Plan 04 shipped over releases 2026.08.30-001 through -011 and drilled on
`liken-1`.

- The scanner reconciles by marking and sweeping against the catalog, and
  the walk streams one folder at a time, so neither the scan nor the prune
  holds the whole library in memory. The catalog is durable on a
  `Library`-owned `ReadWriteOnce` `PersistentVolumeClaim`, sized by the
  namespace `Catalog` (plan 15).
- The scanner's first walk raced the Corrosion sidecar. It posted to the
  agent before the agent bound its API, the walk failed, and the `Library`
  reported zero titles for a full `scanInterval`, five minutes. Corrosion is
  now a native sidecar, an `initContainer` with `restartPolicy: Always`, and
  an exec `startupProbe` that runs `corrosion query "SELECT 1"` holds the
  scanner until the API answers. A kubelet `tcpSocket` or `httpGet` probe
  cannot gate it, because the API binds loopback alone. On `liken-1` the
  scanner started three seconds after Corrosion and its first walk populated
  the count.
- A walk no longer discards a good count when a catalog read-back fails. It
  publishes the count once the write lands, and the prune and the second
  count are best-effort steps that log and wait for the next walk. An
  incomplete walk keeps the last report.
- A movies volume nests a title under more than one grouping folder, a
  genre and then a studio. The one-level walk cataloged the studio folder
  as an unidentified title and missed every film under it. The walk now
  recurses into any grouping folder, and the nested folders went from no
  titles to all of them.
- On `liken-1`: a movies `Library` and a series `Library` each reported
  their titles, and the series reported its episodes under the right
  season and episode numbers, checked by hand against one series. A
  webhook drove a scoped rescan within the request, and adding a folder to
  the `ignore` list removed its rows from the catalog. The catalog's
  `state.db` for both libraries is 38 MB, which plan 06 budgets against.
