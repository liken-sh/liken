---
title: Declare a library
weight: 20
---

# Declare a library

A `Library` is one root directory on one volume, holding media of one
kind. This guide declares one, reads what it reports, and describes
what its namespace decides.

## The declaration

A `Library` names a claim in its namespace, a directory inside it,
and the kind that directory holds:

    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      storage:
        claim: movies-pvc
        root: /media/movies
      kind: movies
      movies: {}

The block named by `kind` must be present, and the other kinds'
blocks must not. An empty block is a complete one. The kinds are
`movies`, `series`, and `franchises`. A series library holds one
folder per series with a season folder inside. A franchises library
is the one kind whose files are written by people, and
[Franchises](/docs/guides/franchises/) covers it.

`root` defaults to `/` and must be absolute, so one volume can hold
several libraries at different roots. The kind and the storage are
immutable. A different volume, a different root, or a different kind
is a different `Library`.

[Library](/docs/reference/libraries/) describes every field. The ones
you are likely to set:

* `spec.sources` names the `MetadataProviders` to ask, in order.
  [Enrichment](/docs/guides/enrichment/) covers them.
* `spec.ignore` lists path components the scanner skips, such as a
  recycle bin or a staging directory.
* `spec.scan.schedule` is the cron expression the full walk runs on,
  in the cluster's time zone. It defaults to once an hour, on the hour.
* `spec.trickplay.enabled` builds the thumbnail sheets for the scrub
  bar. It is off by default, because the first pass reads every video
  end to end.
* `spec.refresh` reopens one fact at a time, so it asks its provider
  again and rewrites its own files in place.

## What it reports

The listing shows the counts and the phase:

    $ kubectl -n media get libraries
    NAME     KIND     TITLES   ITEMS   FILES   WAITING   STATUS   READY   AGE
    movies   movies   128      128     560     1         Idle     True    12d

`Titles` is what the last walk cataloged. `Items` counts movies, or
series and episodes together. `Files` counts the video files with
their sidecars, art, subtitles, and trickplay directories. `Waiting`
counts the titles a provider returned candidates for, which a person
resolves by naming the right `uniqueid` in the `.nfo`. With `-o wide`
the listing adds the claim and the `Unidentified` count, the folders
the walk could not name, which are still browsable under their folder
names.

`Status` is the phase. `Pending` means the storage, the catalog pod, or
the schedule is not ready yet. `Scanning` and `Enriching` name the
`Job` that runs. `Idle` is the state between them. `Failed` means the
last scan `Job` failed and wrote no rows. `Offline` means the
namespace's reporter has left the bus. `Departing` means a deleted
`Library` is still removing its rows from the catalog.

Four conditions report why the phase is what it is:

* `Bound` reports the storage. The reasons for `False` are
  `ClaimNotFound`, `ClaimUnbound`, and `VolumeNotFound`.
* `Ready` reports the scanning path: the catalog pod runs, the
  `CronJob` exists, and the reporter has reported this library. The
  reasons for `False`, in the order they are checked, are `NotBound`,
  `NoCatalog`, `ManyCatalogs`, `CatalogPending`, `ScanPending`,
  `Offline`, and `NoReport`.
* `Sources` reports `spec.sources`. It is absent on a library that
  names none.
* `Departing` reports the teardown of a deleted library, for as long
  as its finalizer keeps the object from being removed.

`status.runs` holds the last run of each worker: `scan`, `rescan`,
`enrich`, and `cleanup`, with its `Job`, its times, and its failure if
it had one. `status.webhook` is the address that rescans one folder;
[Webhooks](/docs/guides/webhooks/) gives it to Radarr, Sonarr, and
Jellyfin.

## The namespace is a boundary

Every `Library` in a namespace writes into that namespace's one
catalog, and every screen in the namespace shows that catalog. So a
`Library` waits with the reason `NoCatalog` until the namespace holds
a `Catalog`, and two `Catalogs` block both. Every catalog row is keyed
by its library, so two libraries in one namespace never touch each
other's rows, even on the same relative path or the same provider id.

Put libraries that should show together in one namespace. Put
libraries that should never meet on a screen in different namespaces.

## How a title is played

When a person plays a title from the browser, the operator creates a
`Play` for `media-operator`, and the media reference in it names the
claim, never the volume behind it:

    claim://movies-pvc//media/movies/Some Film (1999)/Some Film (1999).mkv

The claim is the one the screen already mounts read-only, so
`media-operator` plays from the same claim, and no second claim is
created. A path outside the library's root is refused.
