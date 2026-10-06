---
title: Declare a library
weight: 20
description: "Declare a Library on one directory of one claim, read what it reports, and learn what its namespace determines. Use when adding a movies, series, or music root to a namespace, or when a title will not play."
---

# Declare a library

A `Library` is one root directory on one volume, holding media of one
kind. This guide declares one, reads what it reports, and describes
what its namespace determines.

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

`root` defaults to `/` and must be absolute. One volume can therefore
hold several libraries at different roots. The kind and the storage
are immutable. A different volume, root, or kind is a different
`Library`.

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
  end to end. A trickplay worker `Job` of its own decodes the videos
  after each `Job` of the `Library` ends.
  `spec.trickplay.gpuResourceClaimTemplate` names a
  `ResourceClaimTemplate` you write, so the decode runs on the GPU it
  claims instead of on the CPU.
* `spec.appearances.enabled` records which credited actor is on screen
  at each second of each feature, from the faces in the video and the
  actors' headshots. It is off by default, because the first pass
  decodes every frame of every feature. An appearances worker `Job`
  runs after each `Job` of the `Library` ends, and
  `spec.appearances.gpuResourceClaimTemplate` names the
  `ResourceClaimTemplate` of a GPU to decode and run the face models
  on. The
  [enrichment guide](/docs/guides/enrichment/#appearances) describes
  the fact.
* `spec.refresh` reopens one fact at a time, so it asks its provider
  again and rewrites its own files in place.

## What it reports

The listing shows the counts and the phase:

    $ kubectl -n media get libraries
    NAME     KIND     TITLES   ITEMS   FILES   WAITING   SOURCES   STATUS   READY   AGE
    movies   movies   128      128     560     1         2/2       Idle     True    12d

`Titles` is what the last walk cataloged. `Items` counts movies, or
series and episodes together. `Files` counts the video files with their
`.nfo` files, art, subtitles, and trickplay directories. `Waiting` counts
the titles a provider returned candidates for, which a person resolves
by naming the right `uniqueid` in the `.nfo`. `Sources` counts the
ready providers against the names in `spec.sources`, and it is empty
for a library that names none. With `-o wide` the listing
adds the claim and the `Unidentified` count. Those are the folders the
walk could not name, and they are still browsable under their folder
names.

`Status` is the phase. `Pending` means the storage, the catalog pod, or
the schedule is not ready yet. `Scanning` means a walk runs, and
`Enriching` means the phases of a `Job` run after its walk or in a
`Job` that fills gaps. `Idle` is the state between `Jobs`. `Blocked`
means the pod of a `Job` has stayed `Pending` for five minutes, and no
other `Job` of the `Library` starts until that `Job` ends. `Failed`
means the last walk failed and wrote no rows, or a `Job` failed and no
later `Job` succeeded. [A Job that does not start or that
fails](/docs/guides/scanning/#a-job-that-does-not-start-or-that-fails)
says what to do for both. `Offline` means the
namespace's reporter has left the bus. `Departing` means a deleted
`Library` is still removing its rows from the catalog.

Four conditions report why the phase is what it is:

* `Bound` reports the storage. The reasons for `False` are
  `ClaimNotFound`, `ClaimUnbound`, and `VolumeNotFound`.
* `Ready` reports the scanning path: the catalog pod runs,
  `spec.scan.schedule` parses, the library's `Jobs` start and succeed,
  and the reporter has reported this library. The reasons for `False`,
  in the order they are checked, are `NotBound`, `NoCatalog`,
  `ManyCatalogs`, `CatalogPending`, `ScheduleInvalid`, `JobNotStarted`,
  `JobFailed`, `Offline`, and `NoReport`. The message of
  `JobNotStarted` and of `JobFailed` names the `Job` and the reason
  Kubernetes gives.
* `Sources` reports `spec.sources`. It is absent on a library that
  names none.
* `Departing` reports the teardown of a deleted library, for as long
  as its finalizer keeps the object from being removed.

A fifth condition, `GPUClaimTemplates`, does not change the phase. It
reports the `ResourceClaimTemplates` that the enabled trickplay and
appearances workers name. The reason for `False` is
`ClaimTemplateNotFound`, and the operator starts no worker whose
template is missing. It is absent on a library whose enabled workers
name none.
[A worker on a GPU](/docs/guides/enrichment/#a-worker-on-a-gpu)
describes the templates.

`status.runs` holds the last run of each worker, with its `Job`, its
times, and its failure if it had one. `scan` is a full walk, `rescan`
is a walk of the folders webhooks named, `enrich` is the phases of a
`Job` and its hand-off, and `cleanup` is the sweep of a deleted
`Library`. A phase that failed names its failure in the `enrich` run,
and the `Job` still succeeds, so the next `Job` tries that phase's
titles again. `status.webhook` is the address that rescans one folder;
[Webhooks](/docs/guides/webhooks/) gives it to Radarr, Sonarr, and
Jellyfin.

## The Events it posts

A condition holds only its last verdict. The operator posts a
Kubernetes `Event` on the `Library` for each change, so
`kubectl -n media describe library movies` shows what happened in the
last hour:

* Each new condition, and each change of a condition's status or
  reason, posts one `Event` with the condition's reason and message. A
  new message with the same reason posts none. The reasons that ask a
  person to act post a `Warning`: `ClaimNotFound`, `VolumeNotFound`,
  `ManyCatalogs`, `ScheduleInvalid`, `JobNotStarted`, `JobFailed`,
  `ProviderNotFound`, `ProviderNotReady`, `FactNotServed`,
  `ClaimTemplateNotFound`, and the `Departing` reason `Blocked`. Every
  other reason posts a `Normal` `Event`, so a `Ready` that returns to
  `True` posts `Ready`.
* `JobCreated` (`Normal`) names each `Job` the operator creates for the
  library, what it runs, and why.
* `ScanCompleted` (`Normal`) gives the titles and files of a walk that
  finished, and `ScanFailed` (`Warning`) gives the failure of a walk that
  did not. The operator posts each one when the reporter publishes the
  run.

The API server deletes an `Event` an hour after its last write, so the
conditions and the operator's log keep the facts after that.

## The namespace is a boundary

Every `Library` in a namespace writes into that namespace's one
catalog, and every screen in the namespace shows that catalog. So a
`Library` waits with the reason `NoCatalog` until the namespace holds
a `Catalog`, and two `Catalogs` block both. Every catalog row is keyed
by its library. So two libraries in one namespace never touch each
other's rows, even on the same relative path or the same provider id.

Put libraries that should show together in one namespace. Put
libraries that should never meet on a screen in different namespaces.

## How a title is played

When a person plays a title from the browser, the operator creates a
`Play` for `media-operator`. The media reference in it names the
namespace and the claim, never the volume behind it:

    claim://media/movies-pvc//media/movies/Some Film (1999)/Some Film (1999).mkv

The claim is the one the screen already mounts read-only, so
`media-operator` plays from the same claim, and no second claim is
created. A path outside the library's root is refused.
