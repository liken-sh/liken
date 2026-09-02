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

* [09, The end-to-end drill](09-the-end-to-end-drill.md). The proof on
  `liken-1`, with the numbers written down.
* [10, The documentation site](10-the-documentation-site.md). The manual
  and the generated reference, written after plan 09.

## Future

These plans are named so the design accounts for them. Each is a stub
for a later agent to shape.

* [12, The organizer](12-the-organizer.md). Renaming and moving files to
  a library's naming convention.
* [13, More kinds](13-more-kinds.md). Music, photos, audiobooks, books,
  and games.
* [14, Watch state and people](14-watch-state-and-people.md). Positions,
  history, and who was watching.
* [23, Motion](23-motion.md). Focus that slides, walls that glide, and
  pages that open, on a loop that still draws only when it must.
* [25, People on the screen](25-people-on-the-screen.md). Cast and crew
  as pages of their own, from the tables plan 30 derives. Blocked on
  plan 30.
* [26, The home page](26-the-home-page.md). A first screen of rows that
  blends every kind, in place of the list of libraries.

Enrichment is one design in five plans. Plan 27 states the contracts
they share, and plans 28 to 31 build them in order.

* [27, Enrichment](27-enrichment.md). The volume holds every fact and
  the catalog is derived from it alone: ecosystem files first, a
  `.liken/` directory for what they cannot say, `.contributors/` for
  people, concerns as the unit of work, `MetadataProvider`, and the
  write rules.
* [29, Identification](29-identification.md). `MetadataProvider`, the
  enricher `Job` with its concerns as containers, the `probe` and
  `identity` concerns, the write package with its test, and the gap
  loop.
* [30, Facts, art, and contributors](30-facts-art-and-contributors.md).
  The `.nfo` and its write record, the art concerns, `credits.yaml`,
  `.contributors/`, and trickplay.
* [31, Franchises](31-franchises.md). A library kind whose files hold a
  universe in story order, resolved across the namespace by provider
  id.

Plan 32 stands apart from the enrichment work.


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
* [05, The idle screen in
  Iced](completed/05-the-idle-screen-in-iced.md). Done by
  `media-operator`'s plan 20 in its 2026.08.31 releases; this plan
  built no code here. Why the library layer needs the idle screen to
  be a native client on the bus, and what it needs the result to be:
  a replaceable `spec.idle.image`, every fact from the bus, and the
  look from `brand`'s Iced crate.
* [06, The catalog on a screen](completed/06-the-catalog-on-a-screen.md).
  Built, released in 2026.09.01-002 and -003, and drilled on `liken-1`
  on 2026-09-01. A `Player` that names this operator as its idle
  controller gets a screen pod: the browser, the Corrosion sidecar with
  the catalog on an `emptyDir`, every Library of the namespace mounted
  read-only, and the display claim `media-operator` publishes on
  `status.idle`. The drill found the browser container without the
  catalog mount, fixed in -003. The screen takes no input until plan 07.
* [07, The media browser](completed/07-the-media-browser.md). Built,
  released in 2026.09.01-004, and drilled on `liken-1` on 2026-09-01.
  The screen pod's browser takes the room's remotes over the bus: the
  presses `media-operator`'s idle command pod forwards arrive as keys,
  the shade moments draw black and draw again, back at the libraries
  asks for the shade, and a `Play`'s end re-presents the browser where
  it was. The browser measured 92 MiB resident on the box.
* [08, Playback from the media
  browser](completed/08-playback-from-the-media-browser.md). Built,
  released in 2026.09.01-005, and drilled on `liken-1` on 2026-09-01
  and 2026-09-02. Select on a cover publishes the resolved list on the
  bus, and the operator creates the `Play` with `claim://` references
  and names it after the chosen title. A film and an episode played
  from the X6, and from 2026.09.02-002 the browser is back the instant
  a film ends.
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
* [20, Library-scoped catalog keys](completed/20-library-scoped-keys.md).
  Built, released in 2026.08.31-002, and drilled on `liken-1` on
  2026-08-31. Every replicated table's primary key leads with the
  library, so Libraries that share a namespace never touch each
  other's rows. The change shipped against fresh databases through a
  versioned claim name, with a rescan, and 2026.08.31-003 returned
  the claim to its plain name after every scanner moved to a fresh
  database.
* [28, The catalog pod](completed/28-the-catalog-pod.md). Built in
  2026.09.02-004 to -008 and drilled on `liken-1` on 2026-09-02. One
  standing pod per namespace holds the catalog and reports it, every
  scan is a `Job` that exits on the reporter's echo of its run and its
  counts, the webhook is on the operator, and departure is a `Job`.
  The drill found four gaps, fixed the same day, and one open problem.
* [32, A screen keeps its catalog](completed/32-a-screen-keeps-its-catalog.md).
  Built in 2026.09.02-009 and drilled on `liken-1` on 2026-09-02.
  Every screen's agent runs on a claim of its own, so a restart holds
  the full catalog in one second where it took 157 s on an `emptyDir`.
  The move drill waits for a second display.
* [22, A screen per kind](completed/22-a-screen-per-kind.md). Built in
  two waves, released in 2026.09.02-003, and drilled on `liken-1` on
  2026-09-02. A kind is a screen design in the browser: the movies wall
  with its band and captions, a movie page with its set strip, a series
  page with a fixed header and season dividers, sets derived in the
  catalog, and a poster store that draws large art as bands, fits
  logos, keeps its handles, and holds memory under the pre-plan-22
  line. Pages are stacks of canvases, because a layer draws fills, then
  images, then text.
* [21, A Library takes its rows with
  it](completed/21-a-library-takes-its-rows-with-it.md). Built,
  released in 2026.08.31-004 through -006, and running on `liken-1`.
  A finalizer stops a deleted `Library`'s scanner and launches a
  cleanup pod that sweeps the library's rows out of the catalog, so a
  `Library`'s rows never outlive it. The drill found two gaps, closed
  in -006: the last scanner's retained Last Will republished onto a
  cleared topic, and a departure whose claim was deleted by hand
  released while survivors still held the rows.

## Open problems

* [The media browser shows no battery
  level](open-problems/the-media-browser-shows-no-battery-level.md).
  The browser's idle view draws the level `media-operator` puts on the
  `Player` status once one exists.
* [Which libraries a screen
  shows](open-problems/which-libraries-a-screen-shows.md). The resource
  that binds screens to libraries is undesigned; every screen shows
  every library until it exists.
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
  sidecar-less fifth rest their id on a path a move breaks. Plan 29's
  `identity.yaml` answers it.
* [A fresh agent's first version arrives
  late](open-problems/a-fresh-agents-first-version-arrives-late.md).
  A `Job` on a fresh claim pays one echo timeout on its first run,
  because its first write reaches the catalog pod minutes after the
  rest.
* [Richer file facts](open-problems/richer-file-facts.md). A future
  enhancement reads a file's container metadata for a measured duration,
  the bitrate, the HDR format, and the encode's quality. Plan 29's
  `probe` concern answers it.

## Rejected

* [11, Metadata enrichment](rejected/11-metadata-enrichment.md).
  Superseded by plan 27, which replaces the pod per provider with a
  `Job` per concern.
* [24, Franchises](rejected/24-franchises.md). Superseded by plan 31,
  which puts a franchise on the volume instead of in a resource.
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
