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

[`completed/`](completed/) holds the plans that are built and drilled.
[`open-problems/`](open-problems/) holds what the design still owes an
answer to. [`rejected/`](rejected/) holds what was tried or weighed and
set aside, with the measurements that decided it.

## The design

* [00, The library-operator design](00-design.md). What the operator is
  for, its six responsibilities, its resources, the catalog, the media
  browser, and the technology it is built on.

## Planned

These plans are staged in order. Each keeps its number and moves to
[`completed/`](completed/) when it is built and drilled. Plans 01 to 09
deliver one outcome: index a library, browse it on a screen, pick a
film, and play it on the same `Player`. Plan 10 documents that.

* [03, The catalog](03-the-catalog.md). The Corrosion cluster: the
  schema and its rules, the sidecar, the write path, the update stream,
  and the pod settings the proof of concept decided.
* [04, Scanners for films and
  series](04-scanners-for-films-and-series.md). The scanner contract and
  the first two kinds.
* [05, The idle screen in Iced](05-the-idle-screen-in-iced.md). The idle
  screen redrawn as a native client, and the `Player` field that selects
  it.
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
  Built, and rolled to `liken-1` on 2026-08-29 in release 2026.08.29-002.
  The `Library` CRD with its kind rule, the reconcile loop, the scanner
  pod with the Corrosion sidecar, the report over the bus, and the
  status with its two conditions. The scanner reports zero titles until
  plan 04.

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
