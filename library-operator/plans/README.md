# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows `liken`'s own `plans/` and `media-operator`'s. A
document states a problem, states the contract that answers it, and
states what was considered and set aside. It also states how the work is
proved, and a proof runs on hardware. The pattern is documented in
`liken`'s repository: [milestone 56, device
operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

These plans state contracts. Each one leaves the code's shape, the file
layout, and the names to whoever builds it, and expects that person to
plan the build first. Where a plan needs a change in a lower operator,
it describes that change here and names the repository.

[`completed/`](completed/) holds the plans that are built. A plan there
records its drill in its own proof section, and an entry below says where
a drill is still owed.
[`open-problems/`](open-problems/) holds what the design still owes an
answer to. [`rejected/`](rejected/) holds what was tried or weighed and
set aside, with the measurements that decided it.

## The design

* [00, The library-operator design](00-design.md). What the operator is
  for, its six responsibilities, its resources, the catalog, the media
  browser, and the technology it is built on.

## Planned

These plans are staged in order. Each keeps its number and moves to
[`completed/`](completed/) when it is built. Plans 01 to 09
deliver one outcome: index a library, browse it on a screen, pick a
movie, and play it on the same `Player`. Plan 10 documents that.

* [05, The idle screen in Iced](05-the-idle-screen-in-iced.md). Why the
  library layer needs `media-operator`'s idle screen to be a native
  client on the bus, and what it needs the result to be. The work is
  `media-operator`'s, and this plan builds no code here.
* [06, The catalog on a screen](06-the-catalog-on-a-screen.md). The
  Corrosion sidecar in the idle pod, and the update stream the media
  browser reads.
* [07, The media browser](07-the-media-browser.md). Libraries on the
  screen, and the structure each kind gives.
* [08, Playback from the media
  browser](08-playback-from-the-media-browser.md). From a chosen title
  to a `Play` on the same `Player`, and back.
* [09, The end-to-end drill](09-the-end-to-end-drill.md). The proof on
  `liken-1`, with the numbers written down.
* [10, The documentation site](10-the-documentation-site.md). The manual
  and the generated reference, written after plan 09.

## Future

These plans are named so the design accounts for them. Each is a stub
for a later agent to shape.

* [11, Metadata enrichment](11-metadata-enrichment.md). Providers, the
  enricher pod, and the sidecars it writes.
* [12, The organizer](12-the-organizer.md). Renaming and moving files to
  a library's naming convention.
* [13, More kinds](13-more-kinds.md). Music, photos, audiobooks, books,
  and games.
* [14, Watch state and people](14-watch-state-and-people.md). Positions,
  history, and who was watching.

## Completed

* [01, The repository and its
  builds](completed/01-the-repository-and-its-builds.md). Built, and
  rolled to `liken-1` on 2026-08-29 in release 2026.08.29-001. The
  skeleton: an operator that connects and reconciles nothing, a media
  browser that opens a black window with the harness flags, the three
  images, the release workflow, the coverage gates, and the local
  harness directory.
* [02, The Library resource](completed/02-the-library-resource.md).
  Built, and rolled to `liken-1` on 2026-08-30 in release 2026.08.30-001.
  The `Library` CRD with its kind rule, the reconcile loop, the scanner
  pod with the Corrosion sidecar, the report over the bus, and the
  status with its two conditions. The scanner reports zero titles until
  plan 04.
* [03, The catalog](completed/03-the-catalog.md). Built, and rolled to
  `liken-1` on 2026-08-30 in releases 2026.08.30-002 and 2026.08.30-003.
  The schema with the item header and the `movies` table, the `catalog`
  `Service` and the `EndpointSlice` the operator writes behind it, the
  local three-agent cluster, and the numbers from both drills.
* [04, Scanners for movies and
  series](completed/04-scanners-for-movies-and-series.md). Built, and
  rolled to `liken-1` through release 2026.08.30-011. The streaming walk
  and the mark-and-sweep prune, the movies and series kinds with recursive
  descent through grouping folders, the durable catalog on a `Library`-owned
  claim sized by the namespace `Catalog` (plan 15), the Corrosion native
  sidecar gated by an exec probe, and the scanner's progress logging.
* [15, The namespace catalog](completed/15-the-namespace-catalog.md).
  Built out of sequence during plan 04, and rolled to `liken-1` through
  release 2026.08.30-011. The `Catalog` CRD, one per namespace, that owns
  the catalog `Service` and `EndpointSlice`, sizes a `Library`-owned catalog
  claim per scanner pod, and holds a `Library` `Pending` until exactly one
  `Catalog` exists.

Plans 16 to 19 follow the scanner plans and were built before plan 05.
Each one finishes something plan 04 left short. All four were released in
2026.08.30-012 and drilled on `liken-1` that day, and each records its
drill. The drills found two defects, both fixed in 2026.08.30-013: a
stale title that the prune never removed, and a subtitle language read
that took a hearing-impaired flag for Hindi.

* [16, The counts and the phase](completed/16-the-counts-and-the-phase.md).
  Built, and released in 2026.08.30-012. `status.items`, `status.files`,
  and `status.phase`, with the printer columns that show them and the
  two that move behind `-o wide`. The counts are the catalog's own,
  read after the prune.
* [17, Every file a title carries](completed/17-every-file-a-title-carries.md).
  Built, and released in 2026.08.30-012. The `type`, `role`, `language`,
  and `modified` columns on `files`, the walk that reads the season and
  extras folders, and the classification that opens no file. The
  mark-and-sweep prune needed no change.
* [18, A parallel walk](completed/18-a-parallel-walk.md). Built, and
  released in 2026.08.30-012. Eight workers over one pool of
  directories, one collector, and the count of outstanding directories
  that ends the walk. A directory the walk cannot read now marks the
  pass incomplete wherever it is in the tree.
* [19, The webhook, reachable](completed/19-the-webhook-reachable.md).
  Built, and released in 2026.08.30-012. The `Service` over each
  scanner pod, with the selector the pod's own labels give it, and the
  address in `status.webhook`.

## Open problems

* [Which libraries a screen
  shows](open-problems/which-libraries-a-screen-shows.md). The resource
  that binds screens to libraries is undesigned; every screen shows
  every library until it exists.
* [Every node stores every
  table](open-problems/every-node-stores-every-table.md). A Corrosion
  agent replicates the whole database.
* [Ingest memory and the restart that returns
  it](open-problems/ingest-memory-and-restart.md). A first full sync
  peaks at up to 380 MB; a restart returns the agent to 74 MB.
* [Slow agent shutdown](open-problems/slow-agent-shutdown.md). A busy
  agent can exceed the default grace period.
* [Clients that cannot run an
  agent](open-problems/clients-that-cannot-run-an-agent.md). Phones and
  laptops have no path to the catalog.
* [Writing ids back to the
  volume](open-problems/writing-ids-back-to-the-volume.md). The
  sidecar-less fifth rest their id on a path a move breaks; a minted id
  written back would fix it.
* [Richer file facts](open-problems/richer-file-facts.md). A future
  enhancement reads a file's container metadata for a measured duration,
  the bitrate, the HDR format, and the encode's quality.

## Rejected

* [Litestream as the catalog
  transport](rejected/litestream-as-the-catalog-transport.md). Built and
  measured. Polled reads, a compaction race, and a Go runtime inside the
  client.
* [dqlite](rejected/dqlite.md). Leader-based, Go-only, and built for
  high availability the catalog does not need.
* [A query service in the read path](rejected/a-query-service.md). REST,
  Meilisearch, Typesense, and Postgres.
* [Index trees on the volume](rejected/index-trees-on-the-volume.md).
  Operator-written symlink groupings, replaced by catalog queries.
* [Toolkits other than Iced](rejected/toolkits-other-than-iced.md). Gio,
  Slint, Bevy, Godot, and the rest, with the measurements.
