# Plans

This directory holds the operator's design documents. Each one is
numbered in sequence and keeps its number for life.

The form follows `liken`'s own `plans/` and `media-operator`'s. A
document states a problem, states the contract that answers it, and
states what was considered and set aside. It also states how the work is
proved, and a proof runs on hardware.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem. The pattern is documented in
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

* [09, The end-to-end drill](completed/09-the-end-to-end-drill.md). Complete. The proof on
  `liken-1`, with the numbers written down.
* [10, The documentation site](completed/10-the-documentation-site.md). Built. The manual
  and the generated reference, written after plan 09.

* [37, Prometheus metrics](completed/37-prometheus-metrics.md). Built and drilled on liken-1 on 2026-09-10.
  Enrichment progress, work needing attention, worker outcomes, and
  observation health, with optional collection and quiet idle libraries.
* [46, The power key brings the room up](completed/46-the-power-key-brings-the-room-up.md).
  Built on 2026-09-07. One press wakes the panel, the receiver, and
  the input. The browser half is an [open
  problem](open-problems/the-power-keys-browser-half-is-not-proved.md),
  because plan 54 retired the re-present path the plan named.
* [49, Remote keys for people and home](completed/49-remote-keys-for-people-and-home.md).
  Built in release 2026.09.07-005 and running on the house and on
  `liken-1` since 2026-09-08. The compose key raises the person
  picker, the WWW key goes home, and page up and down are named for a
  later choice.

## Future

These plans are named so the design accounts for them. Each is a stub
for a later agent to shape.

* [48, Larger type on banners and headings](48-larger-type-on-banners-and-headings.md).
  One step up on the brand's type scale for banners, headings, and
  franchise cards, with wall captions unchanged. Waits for a UI round.
* [12, The organizer](12-the-organizer.md). Renaming and moving files to
  a library's naming convention.
* [13, More kinds](13-more-kinds.md). Music, photos, audiobooks, books,
  and games.
* [23, Motion](23-motion.md). Focus that slides, walls that glide, and
  pages that open, on a loop that still draws only when it must.
* [56, Backing up progress](56-backing-up-progress.md). A stub: a
  copy of the progress store a person can take off the cluster and
  put back, and the test that says the copy is current.

Enrichment is one design in five plans. Plan 27 states the contracts
they share, and plans 28 to 31 build them in order.

* [27, Enrichment](completed/27-enrichment.md). Complete. The volume holds every fact and
  the catalog is derived from it alone: ecosystem files first, a
  `.liken/` directory for what they cannot say, `.contributors/` for
  people, concerns as the unit of work, `MetadataProvider`, and the
  write rules.
* [31, Franchises](completed/31-franchises.md). Built. A library kind whose files hold
  one story in story order, with a calendar and universes, resolved
  across the namespace by provider id.
* [33, The IMDb datasets](33-the-imdb-datasets.md). A stub: a provider
  with a store, because the datasets are bulk files and not a call per
  title. OMDb serves `rating.imdb` until then.
* [34, Every fact writes its rows](completed/34-every-fact-writes-its-rows.md).
  Built on 2026-09-03. Each fact writes its own catalog rows as it
  writes its files, only the columns it owns, with the art list in its
  own `arts` column and a prune that spares a row newer than the
  walk's start.
* [57, The phases fan out](57-the-phases-fan-out.md). A stub from plan
  34: the phases that share no file run at once, behind a mark per
  finished phase on a shared `emptyDir`.

Plan 32 stands apart from the enrichment work.


## Completed

* [52, Threads](completed/52-threads.md). Built and drilled 2026-09-08
  in releases 2026.09.08-005 and -006. The continue row across series,
  sets, and franchises: an exact-audience thread that a guest night
  extends and an old solo run does not, the plain successor, reasons
  stacked on one card per leaf, the room's circles on the heading, and
  the franchise page opened on a member. The drill found the one-read
  fix for franchise memberships.
* [51, Drop the Watch](completed/51-drop-the-watch.md). Completed
  2026-09-08. The `Watch` kind, its projection, and the `watch` field
  on the play request are gone; the people on a `Play` are the whole
  record of who shares its progress.
* [50, The Jellyfin backfill](completed/50-jellyfin-backfill.md). Drilled
  2026-09-08: one Job, every user's resumable and played items in the
  store with Jellyfin's dates. Found and fixed the series read that
  answered 400 without a user.
* [47, Progress to Jellyfin over the bus](completed/47-progress-to-jellyfin-over-the-bus.md).
  Built, and drilled on the house on 2026-09-08 in release
  2026.09.07-006: a film paused on a phone showed at the same
  position in the store, and a Play on the television moved
  Jellyfin's resume point for the same person. The Webhook plugin
  stores its template base64-encoded, which the guide now states.
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
  asks for the shade, and the browser returns where it left when the
  `Play` ends. The browser measured 92 MiB resident on the box.
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
* [53, Up next](completed/53-up-next.md). Built and drilled 2026-09-08
  in release 2026.09.08-007. The browser names what follows the work it
  starts, from where the person started, on the `Play`; the player
  offers it on the scrubber and asks over the bus; the browser starts
  it. One episode per `Play`, with the season list gone.
* [54, The browser returns on the status
  edge](completed/54-the-browser-returns-on-the-status-edge.md). Built
  and drilled on `liken-1` on 2026-09-10 in release 2026.09.10-002, and
  on the house the same evening. Under ivi-shell the browser's window is
  visible again the moment a film's surface goes, so the fresh window on
  re-present and the app-id are gone. A film deleted from under the
  browser left it on the screen with no restart, and the browser drew
  its first frame within about 60 ms of the `Idle` status. The drill
  widened the edge to a move from any activity to `Idle`, so a `Play`
  that never played returns the page too.
* [55, Lights down, lights up](completed/55-lights-down-lights-up.md).
  Built and drilled on `liken-1` on 2026-09-10 in release
  2026.09.10-002, and on the house the same evening. The browser dims
  its whole frame to an eighth of full over 1.2 s from the `Player`
  status's move off `Idle`, and lifts it back to full over 0.3 s on the
  move to `Idle`, so the film fades in over a dimmed page and out over a
  page on its way back up. The curtain's logo keeps its full brightness
  through both. Captured frames read 0.77, 0.56, 0.35, and 0.18 at
  0.2 s steps, and then the 0.127 floor. The drill put the lights on the
  status alone, not on the select, and made the scrim a layer of the
  whole frame, so the home page and the walls dim with a title's page.
  Pairs with display-operator plan 18 and media-operator plan 30.

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
* [29, Identification](completed/29-identification.md). Built in
  2026.09.02-010 to -014 and drilled on `liken-1` on 2026-09-02 and
  2026-09-03. `MetadataProvider`, the enricher `Job` with its concerns
  as containers, the `probe` and `identity` concerns, the write package
  with its test, the `.liken/` reader, and the gap loop. Every stripped
  title came back with its original id, and the drill left six open
  items, none of which blocks plan 30.
* [30, Facts, art, and contributors](completed/30-facts-art-and-contributors.md).
  Built in six waves as 2026.09.03-001 to -007 and drilled on
  `liken-1` on 2026-09-03. The word "fact", four providers with one
  block each, the nfo, art, and people groups as phases of one Job,
  who answers, the fight check in the ledgers, and trickplay behind an
  opt-in. Every TMDb fact closed on both libraries, Fanart.tv filled
  the art TMDb lacks, and the drill left the OMDb and fight drills for
  later.
* [26, The home page](completed/26-the-home-page.md). Built in
  2026.09.04-001 to -003 and drilled on `liken-1` on 2026-09-03 and
  2026-09-04. A first screen that blends every library: a banner, the
  newest releases and arrivals, strips the day draws, and the
  libraries. Every screen of titles is one `Query` and one wall, the
  catalog holds a `genres` table, and an arrival ledger in `.liken/`
  feeds `added`.
* [36, Genres on the home page](completed/36-genres-on-the-home-page.md).
  Shaped, built in dev builds off main, and drilled on `liken-1` on
  2026-09-04. The home page reads off the frame thread, every genre
  gets a strip and a page, "see all" opens the page a strip is about
  and only where there is more to see, the home band shows Search
  alone, and a clock in the household's zone sits top-right on every
  screen.
* [39, Search and the keyboard](completed/39-search-and-the-keyboard.md).
  Shaped and built on 2026-09-05 and 2026-09-06 in the working tree and
  drilled on this workstation from `local/browse`. Home and search
  keys, a search wall that answers as a person types from an in-memory
  index, and an on-screen keyboard that hides when a physical letter
  arrives. The `liken-1` drill with the X6's Keymap rows is still owed.
* [40, The rail and the strip](completed/40-the-rail-and-the-strip.md).
  Shaped and built on 2026-09-06 and drilled on this workstation from
  `local/browse`. A right-hand rail on long walls that jumps by letter,
  year, or decade and cycles the order from its button, a seasons rail
  on long series, the genre head folded into the band, and a search
  icon by the clock on every screen. The `liken-1` drill is still owed.
* [14, Watch state and people](completed/14-watch-state-and-people.md).
  Built and drilled on `liken-1` on 2026-09-06. A cluster-scoped
  `Person` in a repository of its own, `people-operator`; a `Watch`
  that names the set of people on one item; a `Play` that names its
  people and its `Watch` through owner references and the work through
  alias annotations; and a progress store, a second `Corrosion` cluster
  per namespace fed from the bus, that a standing pod holds beside the
  catalog pod. The operator is the only API client and holds a
  finalizer on every `Play` and every `Person` until the store answers.
  Plan 51 removed the `Watch`.
* [42, A screen keeps its art](completed/42-a-screen-keeps-its-art.md).
  Built in 2026.09.04-003-dev-041 and drilled on `liken-1` on
  2026-09-07. A second claim beside plan 32's, for every piece of art
  the browser scales, sized by `Catalog.spec.screens.artCache.size`
  and classed with the catalog claim. The cached art survived a pod
  restart, and a deleted claim came back fresh on the next pass.
* [43, Durable and ephemeral claims](completed/43-durable-and-ephemeral-claims.md).
  Built in 2026.09.06-002 and drilled on `liken-1` and the house on
  2026-09-07. `spec.progress` sizes and classes the progress claim,
  and `spec.libraries` classes a `Library`'s two working copies, so a
  cluster keeps its two central stores on a durable class and every
  copy on a node-local one. Each field defaults to `spec.storage`.
* [41, The people on the screen](completed/41-the-people-on-the-screen.md).
  Built in 435db0f and on the house since 2026-09-07. A second agent
  on every screen pod, the person picker, the continue row, and the
  progress marks on pages and walls. Still owed from its build: how
  the `Person` list reaches a pod, the `watch` message over the bus,
  a history page, and bars on series and episode wall cards.
* [44, Copies on per-node volumes](completed/44-copies-on-per-node-volumes.md).
  Built with `per-node-csi-driver` 2026.09.07-001 and on the house
  since 2026-09-07. Every copy of a store binds to a `per-node`
  volume the operator writes up front.
* [45, The browser at 4K](completed/45-the-browser-at-4k.md). Built
  in 2026.09.07-004 and proved on the living room on 2026-09-07 at
  scale 2: the 1080p layout with art at the panel's resolution, and a
  browser that draws no frame under a film. The memory reading at the
  end of a film is still owed to the plan.
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
* [35, The loading state](completed/35-the-loading-state.md). Built
  and released in 2026.09.03-009. Between select and the film's first
  frame, the page steps back and the title's backdrop and logo hold the
  screen with the mark beneath them, and the page returns when the
  browser is presented again. The same release carries the ratings
  line, the foot of studios and file lines, the volume row, and the
  parts under a person's works.
* [25, People on the screen](completed/25-people-on-the-screen.md).
  Built and released in 2026.09.03-008, and drilled on `liken-1` on
  2026-09-03. Two stripes of headshots at the end of a movie's page
  and a series' page, the crew and the cast, and a page for each
  person with a wall of their works across the libraries. The same
  release adds `spec.refresh` to `Library`, one time per fact from
  which a fact asks its provider again, and makes a provider's cast
  and crew replace the sidecar's. The drill's one gap, an enricher
  that read its gap before a count-neutral walk reached its copy, is
  closed in 2026.09.03-010.

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
* [The power key's browser half is not
  proved](open-problems/the-power-keys-browser-half-is-not-proved.md).
  Nothing here reads the `awake` edge, and neither of plan 46's two
  cases is drilled.
* [A fresh agent's first version arrives
  late](open-problems/a-fresh-agents-first-version-arrives-late.md).
  A `Job` on a fresh claim pays one echo timeout on its first run,
  because its first write reaches the catalog pod minutes after the
  rest.

## Rejected

* [11, Metadata enrichment](rejected/11-metadata-enrichment.md).
  Superseded by plan 27, which replaces the pod per provider with a
  `Job` per concern.
* [24, Franchises](rejected/24-franchises.md). Superseded by plan 31,
  which keeps the pointer in the `Library` and the truth in a public
  repository, in place of a resource the cluster owns.
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
