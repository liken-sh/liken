---
title: library-operator
---

# `library-operator`

`library-operator` keeps a catalog of your films and series,
and puts a media browser on every screen of a
[`liken`](https://liken.sh/docs/) cluster. You declare each directory
of media as a `Library`. The operator scans it, fetches titles, plots,
and art from metadata providers, and shows the result on the screens
in the same namespace. A person picks a title with the remote, and the
browser starts it playing on that screen.

Things you can do:

* show a household's films and series on every TV, with art, plots,
  and who's in them,
* keep each person's progress in each title, and the same progress in
  Jellyfin,
* show the films and series of one story, such as a film franchise,
  in story order,
* add a title in Radarr or Sonarr and see it on the screens within
  seconds.

Start here:

* [Install the operator](/docs/guides/install/). The install is a
  kustomize base pinned to a release, and this site also serves the
  same files at [`/deploy/`](/deploy/kustomization.yaml).
* [Declare a library](/docs/guides/libraries/): the claim, the root
  directory, the kind of media, and what the `Library` reports.
* [Put the media browser on a screen](/docs/guides/browser/): one
  field on a `Player`.
* [Reference](/docs/reference/): every field of `Library`, `Catalog`,
  and `MetadataProvider`, and the franchise file.

## How it works

A `Library` is one root directory of media on a volume: films,
series, or a checkout of franchise files. The operator runs a scan
for each one, which reads the files and the metadata beside them into
the namespace's catalog. The catalog is a SQLite database that
[Corrosion](https://github.com/superfly/corrosion) copies to every
screen in the namespace within a second of a change.

The operator reads and writes the media, the `.nfo` files, and the
art on the volume in the formats that Kodi and Jellyfin read, and it
can rebuild the catalog from those files at any time.

The media browser runs on a `Player` that
[`media-operator`](https://liken.sh/media/) owns, in place of that
`Player`'s idle screen. It draws on the screen through the `Player`'s
own display claim, and this operator publishes no devices. A `Player` that doesn't name the browser keeps
`media-operator`'s idle screen.

`library-operator` is one of the extension operators for
[running a home theater](https://liken.sh/docs/concepts/running-a-home-theater/).
You install it only if you want a catalog and a browser.

* [The source](https://github.com/liken-sh/liken/tree/main/library-operator)
* [The `liken` manual](https://liken.sh/docs/)
