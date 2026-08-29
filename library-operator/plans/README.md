# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows `liken`'s own `plans/` and `media-operator`'s. A
document states a problem, states the contract that answers it, and
states what was considered and set aside. It also states how the work
is proved, and a proof runs on hardware. The pattern is documented in
`liken`'s repository:
[milestone 56, device operators](https://github.com/liken-sh/liken/blob/main/plans/completed/56-device-operators.md).

These plans state contracts, not code. Each one leaves the shape of
the code, the file layout, and the names to whoever builds it, and
each one expects that person to plan the build before they start.
Where a plan needs a change in a lower operator, it describes that
change here and names the repository it lands in.

[`completed/`](completed/) holds the plans that are built and
drilled. [`open-problems/`](open-problems/) holds what the design
still owes an answer to. [`rejected/`](rejected/) holds what was
tried or weighed and set aside, with the measurements that decided
it.

## The design

* [00, The library-operator design](00-design.md). What the operator
  is for, its responsibilities, its resources, the catalog, the
  browser, and the technology it is built on.

## Planned

These plans are staged in order. Each keeps its number and moves to
[`completed/`](completed/) when it is built and drilled. The first
nine culminate in one outcome: index a library, browse it on a
screen, pick a film, and play it on the same `Player`.

* [01, The repository and its builds](01-the-repository-and-its-builds.md).
  The skeleton: a Go operator that runs and does nothing, the Rust
  browser crate that opens a window and draws nothing, the images,
  the release workflow, and the local harness directory.
* [02, The Library resource](02-the-library-resource.md). The one
  resource: what a `Library` declares, how a kind is chosen, how a
  library binds to storage, and what its status reports.
* [03, The catalog](03-the-catalog.md). The Corrosion cluster that
  carries the catalog: the schema and its rules, the sidecar
  contract, the write path, and the numbers from the proof of
  concept.
* [04, Scanners for films and series](04-scanners-for-films-and-series.md).
  The scanner contract and the first two kinds: what a scanner reads
  from the share, what it writes to the catalog, and how it learns
  that a file changed.
* [05, The idle screen in Iced](05-the-idle-screen-in-iced.md). The
  browser's first face: the idle screen `media-operator` draws with
  `mpv` today, redrawn as a native client on the bus contract, and
  the `Player` field that swaps it in.
* [06, A screen joins the catalog](06-a-screen-joins-the-catalog.md).
  The Corrosion sidecar in the idle pod, the catalog on the screen's
  local disk, and the change stream the browser reads.
* [07, The browser](07-the-browser.md). Libraries on the screen, and
  the structure each kind gives: films as one list, series as series,
  seasons, and episodes.
* [08, The browser starts a Play](08-the-browser-starts-a-play.md).
  From a chosen title to a `Play` on the same `Player`, and back to
  the browser when it ends.
* [09, The end-to-end drill](09-the-end-to-end-drill.md). The proof
  on `liken-1`: a library indexed, browsed, and played.

## Future

These plans are named so the design accounts for them. Each is a
stub at low fidelity, for a later agent to shape.

* [10, Metadata enrichment](10-metadata-enrichment.md). Fetching
  what the share does not yet hold, from named providers, into the
  same sidecars the scanners read.
* [11, The organizer](11-the-organizer.md). Renaming and moving
  files to a library's naming convention.
* [12, More kinds](12-more-kinds.md). Music, photos, audiobooks,
  books, and games.
* [13, Shelves and rooms](13-shelves-and-rooms.md). Which libraries
  a screen shows, and what its first screen holds.
* [14, Watch state and people](14-watch-state-and-people.md).
  Positions, history, and who was watching.
* [15, The documentation site](15-the-documentation-site.md). The
  manual and the generated reference on the operator's own
  subdomain, written after plan 09 has proved the design.

## Completed

None yet.

## Open problems

* [Every node holds every table](open-problems/every-node-holds-every-table.md).
  A Corrosion agent replicates the whole cluster's database; the
  first large kind decides how screens avoid carrying it.
* [Ingest memory and the restart that returns it](open-problems/ingest-memory-and-restart.md).
  A first full sync peaks at up to 380 MB and a restart returns it to
  74 MB.
* [The agent shuts down slowly](open-problems/the-agent-shuts-down-slowly.md).
  A busy agent can exceed the default grace period.
* [Clients that cannot run an agent](open-problems/clients-that-cannot-run-an-agent.md).
  Phones and laptops have no way to the catalog yet.

## Rejected

* [Litestream as the catalog transport](rejected/litestream-as-the-catalog-transport.md).
  Built and measured; polled reads, a compaction race, and a Go
  runtime inside the client.
* [dqlite](rejected/dqlite.md). Leader-centric, Go-only, and paying
  for high availability the catalog does not need.
* [A query service in the read path](rejected/a-query-service.md).
  REST, Meilisearch, Typesense, and Postgres.
* [Index trees on the share](rejected/index-trees-on-the-share.md).
  Operator-written symlink groupings, replaced by catalog queries.
* [Toolkits other than Iced](rejected/toolkits-other-than-iced.md).
  Gio, Slint, Bevy, Godot, and the rest, with the head-to-head
  numbers.
