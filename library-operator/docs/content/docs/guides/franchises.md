---
title: Franchises
weight: 80
---

# Franchises

A franchise is the films and series of one story, in story order,
with a calendar of its own. Its files are written by people, in a git
repository, and a `Library` of kind `franchises` reads a checkout of
that repository and resolves each member against the other libraries
of the namespace. This guide gets a checkout onto a claim and declares
the `Library`. [Franchise files](/docs/reference/franchises/) describes
the file itself.

## 1. The checkout

The public repository at
[`tangled.org/guid.foo/fiction-franchises`](https://tangled.org/guid.foo/fiction-franchises)
holds the first files, one directory per franchise. How a checkout
reaches a claim is your choice. Any volume with one directory per
franchise serves. The [git CSI driver](https://git.liken.sh) is one
way, and it keeps the checkout current:

    apiVersion: v1
    kind: PersistentVolume
    metadata:
      name: franchises-repo
    spec:
      capacity: {storage: 1Gi}
      accessModes: [ReadOnlyMany]
      persistentVolumeReclaimPolicy: Retain
      storageClassName: ""
      csi:
        driver: git.liken.sh
        volumeHandle: franchises-repo
        readOnly: true
        volumeAttributes:
          url: https://tangled.org/guid.foo/fiction-franchises
          ref: main
          pull: 5m
    ---
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: franchises-repo
      namespace: media
    spec:
      accessModes: [ReadOnlyMany]
      storageClassName: ""
      volumeName: franchises-repo
      resources: {requests: {storage: 1Gi}}

Every scan `Job` and every screen mounts the storage claim read-only,
which is what the driver requires of a `ReadOnlyMany` volume.
[Read-only volumes](https://git.liken.sh/docs/guides/read-only/) in
the driver's manual covers `offline: allowStale` and private
repositories.

## 2. The art claim and the Library

The checkout is read-only, so the art a scan downloads needs a claim
of its own. Every scan `Job` of the library writes it, and every
screen that shows the library mounts it read-only, so it has to allow
those mounts at once:

    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: franchises-art
      namespace: media
    spec:
      accessModes: [ReadWriteMany]
      resources: {requests: {storage: 1Gi}}
    ---
    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: franchises
      namespace: media
    spec:
      storage:
        claim: franchises-repo
      kind: franchises
      franchises:
        art:
          claim: franchises-art

## 3. What a scan does

A scan walks the checkout in name order. Each directory with a
`franchise.yaml` is a franchise, named by its directory, so a renamed
directory is a new row. A directory without one is skipped, which is
how the checkout's own `.git` is skipped. A file the schema refuses is
counted in `status.unidentified` and reported by name, and the files
beside it still write their rows.

Before it reads the rows, the scan downloads the art each file links,
into the art claim under Kodi's names. A link the last scan already
read is not read again, and a file the scan did not write is kept.

A member is a provider id, `tmdb:` for a film and `tvdb:` for a
series, and the browser joins it against every library of the
namespace at read time. A member no library holds draws as a gap:
coming when its release date is ahead, missing otherwise. Add the
title to a movies or series library, and the gap fills on that
library's next scan.

## 4. Write a franchise

Put the schema line at the top of the file, so an editor validates it:

    # yaml-language-server: $schema=https://library.liken.sh/franchise.schema.json

    name: Example Saga

    sources:
      - https://example.com/example-saga-timeline

    calendar:
      unit: years
      zero: the Founding
      before: BF
      after: AF

    universe: Prime

    eras:
      - name: The First Age
        from: -10
        to: 0
      - name: The Second Age
        from: 0
        to: 12

    order:
      - movie: tmdb:900001
        title: "Example Saga: The Beginning"
        released: 2001-03-10
        time: { from: -2, to: -2 }
      - series: tvdb:900002
        title: "Example Saga: The Chronicles"
        released: 2003-09-01
        seasons:
          - season: 1
            time: { from: 0, to: 0 }
          - season: 2
            episodes: [S02E01, S02E03-S02E10]
            time: { from: 1, to: 1 }
      - movie: tmdb:900003
        title: "Example Saga: Convergence"
        released: 2010-07-04
        universes: [Prime, Offshoot]
        time: { from: 12, to: 12 }

`order` is the story, first to last. A series with seasons is one row
on the wall, and a season with an episode range is one run inside it.
An entry that names no universes is in the franchise's own. The
calendar is the story's clock, apart from `released`, the real date.

A fork is a second `Library` over another checkout. Two libraries that
both define one franchise are two rows on the screen.
