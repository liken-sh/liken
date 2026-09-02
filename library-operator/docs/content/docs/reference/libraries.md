---
title: Libraries
weight: 10
toc: true
---

<!-- Generated from deploy/libraries-crd.yaml by crdref. Do not edit. -->

A `Library` is one root directory on one volume, holding media of one
kind. It names a `PersistentVolumeClaim` in its namespace, a directory
inside it, and the kind that directory holds. The operator reconciles
it into a `CronJob` whose `Job`s walk the volume into the namespace's
catalog, and it reports back what the catalog holds: how many titles,
how many folders the walk could not identify, and when it last walked.
An import can rescan one folder at once through the webhook address in
the status.

A namespace may hold many libraries, of any mix of kinds, and one
volume may hold several libraries, each with its own root. The kind
and the storage are immutable: a different volume or a different kind
is a different `Library`.

    apiVersion: library.liken.sh/v1alpha1
    kind: Library
    metadata:
      name: movies
      namespace: media
    spec:
      storage:
        claim: movies
        root: /
      kind: movies
      movies: {}

The block named by `kind` must be present and the other kinds' blocks
must not. Each block holds that kind's own settings, and an empty
block is a complete one.

A Library is a volume of media of one kind, indexed into the catalog the screens read. Create one for each volume and kind you hold: a Library of movies over the movie volume, a Library of series over the series volume.

## spec

The volume this library covers, the kind of media it holds, and the settings for that kind.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--storage"></span>`storage` | [object](#specstorage) | yes | Where the media is: a claim, and a directory inside it. |
| <span id="spec--kind"></span>`kind` | string | yes | What this library holds. The kind selects the scanner that walks the volume and the shape the catalog stores each title in. The settings block of the same name must be present, and no other. One of: `movies`, `series`. |
| <span id="spec--movies"></span>`movies` | [object](#specmovies) | no | The settings for a library of movies, one folder per title. Present exactly when kind is movies, and empty is a complete block: every setting has a default. |
| <span id="spec--series"></span>`series` | [object](#specseries) | no | The settings for a library of series, one folder per series with a folder per season inside it. Present exactly when kind is series, and empty is a complete block. |
| <span id="spec--sources"></span>`sources` | []string | no | The metadata providers to ask about a title, in the order they are asked: the first that answers supplies the title's metadata. Enrichment reads this list. Nothing acts on it yet, and a library that omits it takes what the files themselves carry. |
| <span id="spec--scan"></span>`scan` | [object](#specscan) | no | When the full walk of this library runs. |
| <span id="spec--ignore"></span>`ignore` | []string | no | Path components to skip. The scanner leaves out any folder whose name matches an entry, and everything under it, so a volume's non-media folders such as a recycle bin or a staging directory stay out of the catalog. |

### spec.storage

Where the media is: a claim, and a directory inside it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specstorage--claim"></span>`claim` | string | yes | The PersistentVolumeClaim in this namespace that holds the media. Any volume the cluster can mount will do, an NFS export or a CSI volume or a disk on one node. The scanner mounts it read-only and writes nothing to it. The operator reads the PersistentVolume behind the claim and reports it in the status, because playing a title needs to know how the volume is served. |
| <span id="specstorage--root"></span>`root` | string | no | The directory inside the claim this library starts at, as an absolute path from the root of the volume. One volume may hold several libraries, each with its own root, such as /movies beside /kids-movies. Omitted, it is /, the whole volume. Default: `/`. |

### spec.movies

The settings for a library of movies, one folder per title. Present exactly when kind is movies, and empty is a complete block: every setting has a default.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specmovies--image"></span>`image` | string | no | The scanner image to run in place of the one this project ships for movies. Set it to run a scanner of your own, which must speak the scanner contract: mount the volume read-only, write through the catalog sidecar, and write its runs row last. Omitted, the operator runs the project's own image. |

### spec.series

The settings for a library of series, one folder per series with a folder per season inside it. Present exactly when kind is series, and empty is a complete block.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specseries--image"></span>`image` | string | no | The scanner image to run in place of the one this project ships for series, on the same terms as the movies image. |

### spec.scan

When the full walk of this library runs.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specscan--schedule"></span>`schedule` | string | no | The cron expression the full walk runs on, in the cluster's time zone; omitted, once an hour on the hour. Default: `0 * * * *`. |

## status

What the volume resolved to and what the namespace's reporter says about this library, written only by the library operator. No pod it creates holds an API credential. The catalog pod publishes a retained report on the bus, and the operator folds that report in here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--volume"></span>`volume` | [object](#statusvolume) | no | The PersistentVolume the claim is bound to. It is absent until the claim binds. Playing a title from this library needs the volume's kind and address, so the operator reports them here and no reader has to follow the claim to its volume. |
| <span id="status--phase"></span>`phase` | string | no | What this library is doing. Scanning while a walk runs, Idle between walks, Pending while the storage, the catalog pod, or the schedule is not ready, and Offline when the namespace's reporter has left the bus. Departing means the Library is deleted and the operator holds it open while a cleanup Job takes this library's rows out of the namespace's catalog; the Departing condition says which step that teardown has reached. |
| <span id="status--titles"></span>`titles` | integer | no | How many titles the scanner's last walk cataloged. |
| <span id="status--items"></span>`items` | integer | no | How many entries this library holds: its movies, or its series and their episodes counted together. |
| <span id="status--files"></span>`files` | integer | no | How many files this library holds: the video files and everything beside them, the sidecars, the artwork, the subtitles, and the trickplay directories. |
| <span id="status--unidentified"></span>`unidentified` | integer | no | How many folders the last walk could not identify: no sidecar file, and no confident parse of the folder name. They are cataloged under their folder names, so they are still browsable. |
| <span id="status--removedlastsweep"></span>`removedLastSweep` | integer | no | How many catalog rows the scanner's last full sweep removed. A mass delete that a partial walk caused shows here, without a shell. |
| <span id="status--lastwalk"></span>`lastWalk` | string | no | When the scanner last finished a full walk of the volume. |
| <span id="status--lastchange"></span>`lastChange` | string | no | When the scanner last wrote a change to the catalog. A walk that finds nothing new moves lastWalk and leaves this alone. |
| <span id="status--runs"></span>`runs` | [\[\]object](#statusruns) | no | The last run of each worker of this library, as the namespace's reporter published it. |
| <span id="status--webhook"></span>`webhook` | string | no | The address you give to Radarr, Sonarr, or Jellyfin so that an import rescans that one folder at once; it names the operator's own Service and this Library, so it holds for the life of the Library, and it is reported once the storage is bound and the namespace holds one Catalog. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The typed observations the operator keeps on this library, in the standard Kubernetes form. Bound reports the storage: True when the claim exists, is bound, and its PersistentVolume was read, and False with the reason ClaimNotFound, ClaimUnbound, or VolumeNotFound. Ready reports the scanning path: True when the namespace's catalog pod runs with every container ready, the schedule stands, and the reporter has reported this library, and False with the reason NotBound, NoCatalog, ManyCatalogs, CatalogPending, ScanPending, Offline, or NoReport. Departing reports the teardown of a deleted Library: True for as long as the operator's finalizer holds the object open, with the reason ScanRunning, Sweeping, AwaitingEcho, or Blocked, and a message that names what the teardown waits on. |

### status.volume

The PersistentVolume the claim is bound to. It is absent until the claim binds. Playing a title from this library needs the volume's kind and address, so the operator reports them here and no reader has to follow the claim to its volume.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusvolume--name"></span>`name` | string | no | The PersistentVolume's name. |
| <span id="statusvolume--type"></span>`type` | string | no | How the volume is served, which is the name of the source key on the PersistentVolume, such as nfs, csi, local, or hostPath. |
| <span id="statusvolume--server"></span>`server` | string | no | The NFS server that exports the volume. Only an nfs volume has one. |
| <span id="statusvolume--path"></span>`path` | string | no | The path the NFS server exports. Only an nfs volume has one. A title's media reference is this path and the title's own path under it. |

### status.runs[]

The last run of each worker of this library, as the namespace's reporter published it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusruns--worker"></span>`worker` | string | yes | Which worker ran: scan or cleanup. |
| <span id="statusruns--job"></span>`job` | string | no | The name of the Job that ran, for kubectl describe and logs. |
| <span id="statusruns--started"></span>`started` | string | no | When that Job started its work. |
| <span id="statusruns--finished"></span>`finished` | string | no | When that Job wrote its last row, which is what a Job waits to see echoed before it exits. |
| <span id="statusruns--unidentified"></span>`unidentified` | integer | no | How many folders that run could not identify. |
| <span id="statusruns--removed"></span>`removed` | integer | no | How many rows that run removed. |

### status.conditions[]

The typed observations the operator keeps on this library, in the standard Kubernetes form. Bound reports the storage: True when the claim exists, is bound, and its PersistentVolume was read, and False with the reason ClaimNotFound, ClaimUnbound, or VolumeNotFound. Ready reports the scanning path: True when the namespace's catalog pod runs with every container ready, the schedule stands, and the reporter has reported this library, and False with the reason NotBound, NoCatalog, ManyCatalogs, CatalogPending, ScanPending, Offline, or NoReport. Departing reports the teardown of a deleted Library: True for as long as the operator's finalizer holds the object open, with the reason ScanRunning, Sweeping, AwaitingEcho, or Blocked, and a message that names what the teardown waits on.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list, so a library carries one entry for each type. The description of the conditions field lists the types this operator publishes. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. Bound and Ready state a healthy fact, so True is the good verdict for both. Departing states work in progress, so its True says the teardown is running, and its reason says how far. Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. The generation counts spec edits, so a reader can tell a verdict on the spec as it stands from a verdict on an earlier spec. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads, such as the claim that is not bound or the container that will not start. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips, not on every write, so it answers how long a library has been Ready. |

## On the bus

No pod the operator creates holds an API credential. The namespace's
catalog pod publishes one report per `Library` on `media-operator`'s
bus, under this operator's own topic base, `liken/library` by default,
and the operator folds each report into that `Library`'s status.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the catalog pod | yes | the library's report |
| `catalogs/{namespace}/availability` | the catalog pod | yes | `online` or `offline` |

### `status`

The report, as the operator writes it into the status: the counts, the
rows the last sweep removed, the two times, and the last run of each
worker. A scan `Job` writes its `runs` row as its last catalog write
and exits only when this report names that `Job`, so a report is also
the proof that the catalog pod holds every row the `Job` wrote.

    {
      "titles": 412,
      "unidentified": 9,
      "removedLastSweep": 3,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z",
      "runs": [
        {"worker": "scan", "job": "movies-scan-29310740",
         "started": "2026-08-29T21:03:02Z", "finished": "2026-08-29T21:04:11Z",
         "unidentified": 9, "removed": 3}
      ]
    }

### `availability`

`online` while the catalog pod's reporter runs. The reporter names this
topic as its MQTT Last Will with `offline` as the payload, so a pod the
kubelet killed reads `offline`, and every `Library` of the namespace
reads `Offline` with it. The reports it left behind stand: the counts
describe the catalog, and they hold until the next run replaces them.
