---
title: Libraries, the catalog, and the screens
weight: 10
---

# Libraries, the catalog, and the screens

You tell the operator where your media is and which metadata
providers to use. It scans the media into a catalog, and every screen
in the namespace shows that catalog. The API has three resources, all
in a namespace:

* A `MetadataProvider` is one account with one metadata provider,
  such as TMDb. You declare it once.
* A `Catalog` is the namespace's shared catalog. You declare one for
  each namespace. It creates the catalog pod, which always holds a
  copy of the namespace's SQLite catalog, and it sets the size of
  every claim that holds a copy.
* A `Library` is one root directory on a volume, and it says which
  kind of media is there. You declare one for each directory.

For each `Library`, the operator runs `Jobs`, one at a time. A `Job`
scans the volume on the `Library`'s schedule and writes rows into the
namespace's catalog. A webhook from Radarr, Sonarr, or Jellyfin runs
the same scan over only the folders it names. In the same `Job`,
beside the scan, the enrichment phases ask each provider that the
`Library` names for metadata, and write the answers beside the media, as the `.nfo` files
and the art that Kodi and Jellyfin read. The `Job` ends when the
catalog pod confirms that it has the `Job`'s rows. Each `Job` reads
and writes the catalog through a Corrosion agent on the `Library`'s
catalog claim.

The catalog is built from the files on the volume, so the operator can
always build it again.

A screen is a `Player` that `media-operator` owns. When the
`Player`'s idle controller names this operator, the operator runs a
pod on the `Player`'s display with a Corrosion agent and the media
browser. The browser reads its own copy of the catalog, gets the
remote's presses over the message bus, and publishes what a person
picked. The operator turns that into a `Play`.

Every `Library` in a namespace writes to the same catalog, and every
screen in that namespace shows it.
