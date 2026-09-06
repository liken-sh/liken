---
title: library.liken.sh
---

# `library.liken.sh`

`library-operator` is the media libraries of a cluster, as Kubernetes
resources, and a media browser for them on every screen. It runs on a
[`liken`](https://liken.sh/docs/) cluster above
[`media-operator`](https://media.liken.sh), which owns the players,
the plays, and the remotes. This operator owns what there is to play.

A `Library` is one root directory of media on a volume: a directory of
movies, a directory of series, or a checkout of franchise files. The
operator runs a scanner for each one. The scanner reads the files and
the metadata beside them into a catalog, and a gossip agent replicates
that catalog to every screen within a second of a change. The media
browser is a native Wayland client. It takes the place of the idle
screen on a `Player`, lets a person walk into a library, and starts a
`Play` on that `Player`.

The volume stays the source of truth. The files, the `.nfo` sidecars,
and the artwork are what the operator reads and writes, in the forms
Kodi and Jellyfin read. The catalog is derived and rebuildable.

Start here:

* [Install the operator](/docs/guides/install/). The install is a
  kustomize base pinned to a release, and this site serves the same
  files at [`/deploy/`](/deploy/kustomization.yaml).
* [Declare a library](/docs/guides/libraries/): the claim, the root,
  the kind, and what it reports.
* [Put the media browser on a screen](/docs/guides/browser/): one
  field on a `Player`.
* [Reference](/docs/reference/): every field of `Library`, `Catalog`,
  and `MetadataProvider`, and the franchise file.

The operator is optional. A cluster that never installs it runs
unchanged, and a `Player` whose idle controller never names it draws
`media-operator`'s own idle screen. It publishes no devices. The
screen it draws claims the display through the `Player`'s standing
claim, which the [display operator](https://display.liken.sh) serves.

* [The repository](https://github.com/liken-sh/library-operator)
* [The `liken` manual](https://liken.sh/docs/)
