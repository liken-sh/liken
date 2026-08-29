# The library-operator design

The library operator is the media library layer of a
[`liken`](https://liken.sh/) cluster, expressed as Kubernetes
resources. It runs above
[`media-operator`](https://github.com/liken-sh/media-operator), which
owns the players, the plays, and the remotes, and above the hardware
operators that publish each screen, speaker, and controller as a
claimable device. Those layers answer "what plays where, and who
drives it". This layer answers "what is there to play", and it puts
that answer on the screen.

Like the layers below, this is an optional workload. A cluster runs
fine without it. It depends on `media-operator`'s public resources,
its bus, and its idle-client contract, and `media-operator` never
depends on it.

## The problem

A `Play` names its media as URIs, and today those URIs come from a
person with `kubectl`. The films, series, and albums on the share
have folders, `.nfo` sidecars, artwork, and thumbnails beside them,
written by the tools that manage the share, and nothing in the
cluster reads any of it. A person in a room has no way to see what
is there and pick something. The pieces that would make a media
system out of the playback layer are a catalog of the share and a
browser on the screen, and those are what this operator adds.

## Responsibilities

The operator has five responsibilities, and each one is a separate
loop with a contract between it and the next. The share is where
the loops meet.

1. **Monitor.** Know when a library's files change. The share is
   NFS, which delivers no `inotify` events for another client's
   writes, so a scanner is told of changes by the tools that make
   them, through webhooks, and from a slow full walk as the backstop.
2. **Index.** Read a library into a catalog: the titles, their
   structure, and the metadata and art paths beside them. The catalog
   is derived. Losing it costs a rescan and nothing else.
3. **Enrich.** Fetch what the share does not hold yet, from named
   metadata providers, into the same sidecar files the scanners
   read. This is a later plan; until then, the tools that manage the
   share do the enriching.
4. **Serve.** Put the catalog where every reader can reach it with no
   query service in the middle: on each screen's own disk, kept in
   step by gossip, with a push when something changes.
5. **Browse.** Draw the catalog on a screen as a native client, let a
   person walk into a library, and start a `Play` on the same
   `Player`.

Reorganizing the files themselves, renaming and moving them to a
naming convention, is a sixth responsibility the design accounts for
and does not build yet.

## Libraries are many, and kinds are plugins

A `Library` is one root on one share, of one kind. A cluster holds
many: two film libraries on two shares, a series library, a music
library, a photo library. Nothing in the design assumes one library
of a kind, and every relation between libraries and the things that
read them is many-to-many. A screen may browse several libraries,
and a library may appear on every screen.

A kind is a plugin. It says how to walk a root, how to read the
sidecars that kind's ecosystem already writes, and what structure a
browser draws for it. Films are one flat list. Series are series,
then seasons, then episodes. Each kind runs as its own scanner image,
so a photo scanner never carries an `.nfo` parser, and a new kind is
a new image and a new typed block in the `Library` schema, never a
change to the old ones. The first two kinds are films and series.
Music, photos, audiobooks, books, and games are named and deferred.

Each kind keeps the format its ecosystem uses. Films and series use
the `.nfo`, artwork, and thumbnail sidecars that Jellyfin, Kodi, and
the `*arr` tools read and write. Music uses the tags in the files.
Photos use EXIF and XMP. The operator invents no format of its own on
the share, so every file it reads or writes stays useful to other
programs.

## The catalog

The catalog is a SQLite database carried by
[Corrosion](https://github.com/superfly/corrosion), Fly.io's
gossip-based SQLite replication. A Corrosion agent runs as a sidecar
in every pod that needs the catalog: beside each scanner, and beside
the browser on each screen. Agents find each other with SWIM gossip
over QUIC and spread each change to every peer. Every node holds a
full copy on its own disk.

The contract is simple and strict. Every write goes through the
local agent's HTTP API, and only scanners write. A screen never
writes. Every read is a plain SQLite read of the local file, with no
extension loaded and no service in the path. A browser is told of a
change by its agent's change stream, which names the primary keys
that changed, and re-reads those rows from its own file.

The proof of concept measured the shape: a change written on one node
reached a subscriber on another in 17 ms at the median; a 5000-title
seed converged on every node in under a second and 105,000 rows in
under thirty; a screen's agent holding 105,000 rows sat at 74 MB
resident after a restart and answered a browse query from its file in
under 4 ms. The costs it found are recorded in the catalog plan and
in [`open-problems/`](open-problems/): ingest memory that only a
restart returns, every node holding every table, and a slow
shutdown.

The catalog's schema is one file every node loads. Each item has a
common header, the columns every kind shares and every shelf sorts
on, and a body in the kind's own shape, carried as JSON. That is how
Kubernetes keeps a `Pod` and a `Service` apart, and it is why the
catalog never grows one row type with two hundred optional columns.

## The browser

The browser is one native Wayland client, built with
[Iced](https://iced.rs) in Rust, that has two faces. At rest it is
the idle screen `media-operator` draws with `mpv` and Lua today: the
mark, the clock, the unit's name and parts, and their animations. On
a press it opens the libraries. It runs in `media-operator`'s idle
pod, swapped in by one `Player` field that names an image, and it
speaks the same bus contract the idle screen speaks: the `Player`'s
status, commands, focus, and volume topics. `media-operator`'s
idle-command sidecar keeps the clock, the panel power, and the fade
decisions, and it holds no notion of libraries.

The browser reads the catalog from its own sidecar's file and
subscribes to its change stream. When a person picks a title, the
browser creates a `Play` on the same `Player`, with the `nfs://` URI
the catalog minted and the thumbnail sidecars the display already
draws, and it returns to the shelves when the `Play`
ends.

The toolkit was chosen by a head-to-head against Gio, with both
reproducing the idle screen and a five-thousand-poster wall. Iced won
on text rendering, on frame time at the 99th percentile, and on
memory under a full poster scroll: 115 MB against 242 MB at the same
cache size. The other candidates, and why each lost, are in
[`rejected/`](rejected/).

## Dependencies point one way

This operator reads `Player` and `Play`, writes `Play`, and uses
`media-operator`'s bus. `media-operator` reads nothing of this
operator's. Where this design needs a change below, the plan
describes it here and names the repository it lands in. Two are known
at the outset: a `Player` field that names the idle client image, and
a `Play` status field that reports the current item of a list, so a
season resumes on the right episode. Which libraries a screen may
browse is a fact of this layer, so it lives here and never on the
`Player`.

## Technology

The operator, the scanners the project ships, and the release
tooling are Go, matching the operators below. The browser is Rust,
because Iced is. Corrosion is Rust and arrives as a binary in a
sidecar image, so nothing else in the project has to be. Scanners are
a contract, not a language: a scanner is an image that reads a mount
and posts to a local HTTP API, and a kind may be written in whatever
suits its parsers.

The project patches and forks what it needs rather than waiting on
upstream. The plans name the known patches.

## What this design does not do yet

It keeps no watch state and has no notion of people; a `Play` starts at the
beginning, and the design for positions, history, and identity is a
later plan. It enriches nothing; Jellyfin keeps writing the `.nfo`
and art beside the files, and also stays the app for phones and
laptops, which this operator does not serve. It moves no files. It
carries films and series and no other kind. And it has no view for
which libraries a room shows: every screen shows every `Library` in
its namespace until the shelves plan lands.
