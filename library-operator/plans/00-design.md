# The library-operator design

The library operator is the media library layer of a
[`liken`](https://liken.sh/) cluster, expressed as Kubernetes resources.
It runs above
[`media-operator`](https://github.com/liken-sh/media-operator), which
owns the players, the plays, and the remotes, and above the hardware
operators, which publish each screen, speaker, and controller as a
claimable device. Those layers answer what plays where and who drives
it. This layer answers what there is to play, and it draws that answer
on the screen.

Like the layers below, this is an optional workload. A cluster runs
without it. It depends on `media-operator`'s public resources, its bus,
and its idle-client contract. `media-operator` does not depend on it.

## The problem

A `Play` names its media as URIs, and a person supplies them with
`kubectl`. The files on a library's volume have folders, `.nfo`
sidecars, artwork, and thumbnails beside them, written by the tools that
manage the volume, and nothing in the cluster reads any of it. A person
in a room has no way to see what is there and pick something. A catalog
of the volume and a media browser on the screen make a media system out
of the playback layer, and those are what this operator adds.

## Responsibilities

The operator has six responsibilities. Each one is a loop of its own,
and the loops meet on the library's volume: one loop writes files there,
and the next reads them.

1. **Monitor.** Detect that a library's files changed.
2. **Index.** Read a library into a catalog: its titles, their
   structure, and the paths of the metadata and art beside them. The
   catalog is derived. Losing it costs a rescan.
3. **Enrich.** Fetch what the volume does not hold, from named metadata
   providers, into the same sidecar files the indexer reads.
4. **Organize.** Rename and move files to the library's naming
   convention.
5. **Serve.** Replicate the catalog to every reader with no query
   service in the path, and notify each reader when a row changes.
6. **Browse.** Draw the catalog on a screen, let a person walk into a
   library, and start a `Play` on that screen's `Player`.

## Libraries and kinds

A `Library` is one root directory on one volume, of one kind. A cluster
has many: two film libraries on two volumes, a series library, a music
library, a photo library. Every relation between libraries and the
things that read them is many-to-many. A screen may browse several
libraries, and a library may appear on every screen.

A kind is a plugin. It defines how to walk a root, how to read the
sidecars that kind's ecosystem writes, and what structure a media
browser draws. Films are one flat list. Series are series, then seasons,
then episodes. Each kind runs as its own scanner image, so a photo
scanner never contains an `.nfo` parser. A new kind is a new image and a
new typed settings block in the `Library` schema. The kinds are films,
series, music, photos, audiobooks, books, and games.

Each kind uses the format its ecosystem uses. Films and series use the
`.nfo`, artwork, and thumbnail sidecars that Jellyfin, Kodi, and the
`*arr` tools read and write. Music uses the tags in the files. Photos
use EXIF and XMP. The operator writes no format of its own on the
volume, so every file it reads or writes stays useful to other programs.

## Storage

A `Library` binds to a `PersistentVolumeClaim` in its namespace. The
claim can be any volume the cluster can mount: an NFS export, a Longhorn
volume, a local disk on a single-node cluster. The scanner mounts it
read-only. The enricher and the organizer mount it read-write. Nothing
in this operator depends on the volume's kind.

A `Play` reaches the files by a media reference the `Player` accepts.
The operator derives that reference from the item's path and from the
volume behind the claim. `media-operator` resolves `https://` and
`nfs://` today, so an NFS-backed volume yields an `nfs://` reference. A
volume of another kind needs a reference that names the claim itself,
which is a change in `media-operator` that the playback plan describes.

## The catalog

The catalog is a SQLite database replicated by
[Corrosion](https://github.com/superfly/corrosion). A Corrosion agent
runs as a sidecar in every pod that reads or writes the catalog: beside
each scanner, and beside the media browser on each screen. Agents find
each other by SWIM gossip over QUIC, and each change reaches every peer.
Every agent stores a full copy on its own disk.

The contract has three rules. Every write goes through the local agent's
HTTP API, and only scanners write. A screen never writes. Every read is
a SQLite read of the local file, with no extension loaded and no service
in the path. A media browser receives each change from its agent's
update stream, which names the primary keys that changed, and re-reads
those rows from its own file.

The catalog's schema is one file that every agent loads. Each item has a
header, the columns every kind shares and every list sorts on, and a
body in the kind's own shape, stored as JSON. One table type with every
kind's columns as optional fields is the shape this rule prevents.

A proof of concept confirmed the fit. A change written on one node
reached a subscriber on another in 17 ms at the median, and an agent
with 105,000 rows used 74 MB of resident memory at rest. The costs it
found are in [`open-problems/`](open-problems/).

## Enrichment

A `MetadataSource` names one provider, with the same
discriminator-and-block shape as `Library`: one typed block per
provider, with its `Secret` reference and its settings. Each provider
serves fixed kinds. A `Library` lists its sources in order.

An enricher runs per provider, in its own pod, because it holds keys and
is the only part of the operator that connects to the internet. It
writes sidecars in the ecosystem's formats: `.nfo`, art files, and
WebVTT thumbnail sprites. The scanner detects the new files and updates
the catalog. The enricher never writes the catalog.

One program writes sidecars into a folder. A library that another tool
enriches, such as Jellyfin, has no enricher of its own until that tool's
writer is turned off.

## Organization

The organizer is the one loop that moves or renames files. It takes the
naming convention from the same settings block the scanner parses by, so
the two agree on what a name means. A move is announced to the scanner
through the same path an import uses, so the catalog updates the row
rather than losing one title and finding another. The organizer is
opt-in per `Library`, and it stays off for a library that another tool
organizes.

## The media browser

The media browser is one native Wayland client, built with
[Iced](https://iced.rs) in Rust. At rest it draws the idle screen that
`media-operator` defines: the mark, the clock, the unit's name and
parts, and their animations. On a press it draws the libraries. It runs
in `media-operator`'s idle pod, selected by a `Player` field that names
an image, and it reads the same bus topics the idle screen reads: the
`Player`'s status, commands, focus, and volume. `media-operator`'s
idle-command sidecar keeps the clock, the panel power, and the fade
policy, and has no notion of libraries.

The media browser reads the catalog from its own sidecar's file and
subscribes to that sidecar's update stream. Which libraries a screen
shows, and what its first view contains, are facts of this layer. They
belong to a resource of this operator and never to the `Player`.

When a person picks a title, the media browser publishes a request on
the bus: its `Player` and the item. The operator, which has the RBAC,
creates the `Play` in the `Player`'s namespace with the media reference,
the thumbnail sidecars, and the start position. The screen holds no API
credential. When the `Play` ends, the media browser is where the person
left it.

## Watch state and people

The operator records what each `Play` reached: the item index and the
position inside it, taken from the `Play` status. It records them per
`Player`, and per person once a person is a resource. A `Person` is a
fact of the whole cluster, owned by an operator of its own: a display
name, an avatar, a flag for a child, and a link to an identity provider.
A screen takes the person from a picker or from a default per room, and
the media browser puts the person on its `Play` request.

## Dependencies point one way

This operator reads `Player` and `Play`, writes `Play`, and uses
`media-operator`'s bus. `media-operator` reads nothing of this
operator's. Where the design needs a change below, a plan here describes
it and names the repository it lands in. Four are known.

- A `Player` field for the idle client image.
- A way to add containers and volumes to the idle pod.
- A `Play` status field for the current item of a list.
- A media reference that names a claim.

## Technology

The operator, the scanners the project ships, and the release tooling
are Go, like the operators below. The media browser is Rust, because
Iced is. Corrosion is Rust and arrives as a binary in a sidecar image. A
scanner is a contract: an image that reads a mount and posts to a local
HTTP API, in whatever language suits its parsers.

The project patches and forks what it needs. The plans name the patches.
