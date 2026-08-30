---
title: Libraries
weight: 10
toc: true
---

<!-- Generated from deploy/libraries-crd.yaml by crdref. Do not edit. -->

A `Library` is one root directory on one volume, holding media of one
kind. It names a `PersistentVolumeClaim` in its namespace, a directory
inside it, and the kind that directory holds. The operator reconciles
it into one scanner pod, which walks the volume into the catalog, and
it reports back what the scanner found: how many titles, how many
folders it could not identify, and when it last walked.

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
| <span id="specmovies--image"></span>`image` | string | no | The scanner image to run in place of the one this project ships for movies. Set it to run a scanner of your own, which must speak the scanner contract: mount the volume read-only, write through the catalog sidecar, and publish its report to the bus. Omitted, the operator runs the project's own image. |

### spec.series

The settings for a library of series, one folder per series with a folder per season inside it. Present exactly when kind is series, and empty is a complete block.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specseries--image"></span>`image` | string | no | The scanner image to run in place of the one this project ships for series, on the same terms as the movies image. |

## status

What the volume resolved to and what the scanner reports, written only by the library operator. The scanner pod holds no API credential. It publishes a retained report on the bus, and the operator folds that report in here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--volume"></span>`volume` | [object](#statusvolume) | no | The PersistentVolume the claim is bound to. It is absent until the claim binds. Playing a title from this library needs the volume's kind and address, so the operator reports them here and no reader has to follow the claim to its volume. |
| <span id="status--titles"></span>`titles` | integer | no | How many titles the scanner's last walk cataloged. |
| <span id="status--unidentified"></span>`unidentified` | integer | no | How many folders the last walk could not identify: no sidecar file, and no confident parse of the folder name. They are cataloged under their folder names, so they are still browsable. |
| <span id="status--lastwalk"></span>`lastWalk` | string | no | When the scanner last finished a full walk of the volume. |
| <span id="status--lastchange"></span>`lastChange` | string | no | When the scanner last wrote a change to the catalog. A walk that finds nothing new moves lastWalk and leaves this alone. |
| <span id="status--pod"></span>`pod` | string | no | The scanner pod's name, for kubectl describe and logs. The pod is owned by this Library and is deleted with it. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The typed observations the operator keeps on this library, in the standard Kubernetes form. Bound reports the storage: True when the claim exists, is bound, and its PersistentVolume was read, and False with the reason ClaimNotFound, ClaimUnbound, or VolumeNotFound. Ready reports the scanner: True when its pod runs with every container ready and the operator holds a report for this library, and False with the reason NotBound, PodPending, PodFailed, or NoReport. |

### status.volume

The PersistentVolume the claim is bound to. It is absent until the claim binds. Playing a title from this library needs the volume's kind and address, so the operator reports them here and no reader has to follow the claim to its volume.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusvolume--name"></span>`name` | string | no | The PersistentVolume's name. |
| <span id="statusvolume--type"></span>`type` | string | no | How the volume is served, which is the name of the source key on the PersistentVolume, such as nfs, csi, local, or hostPath. |
| <span id="statusvolume--server"></span>`server` | string | no | The NFS server that exports the volume. Only an nfs volume has one. |
| <span id="statusvolume--path"></span>`path` | string | no | The path the NFS server exports. Only an nfs volume has one. A title's media reference is this path and the title's own path under it. |

### status.conditions[]

The typed observations the operator keeps on this library, in the standard Kubernetes form. Bound reports the storage: True when the claim exists, is bound, and its PersistentVolume was read, and False with the reason ClaimNotFound, ClaimUnbound, or VolumeNotFound. Ready reports the scanner: True when its pod runs with every container ready and the operator holds a report for this library, and False with the reason NotBound, PodPending, PodFailed, or NoReport.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list, so a library carries one entry for each type. The description of the conditions field lists the types this operator publishes. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. Both condition types on a Library state a healthy fact, so True is the good verdict for both. Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. The generation counts spec edits, so a reader can tell a verdict on the spec as it stands from a verdict on an earlier spec. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads, such as the claim that is not bound or the container that will not start. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips, not on every write, so it answers how long a library has been Ready. |

## On the bus

The scanner pod holds no API credential. It publishes its report on
`media-operator`'s bus, under this operator's own topic base,
`liken/library` by default, and the operator folds the report into
the `Library`'s status.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `libraries/{namespace}/{name}/status` | the scanner | yes | the scanner's report |
| `libraries/{namespace}/{name}/availability` | the scanner | yes | `online` or `offline` |

### `status`

The report, as the operator writes it into the status: the two counts
and the two times.

    {
      "titles": 412,
      "unidentified": 9,
      "lastWalk": "2026-08-29T21:04:11Z",
      "lastChange": "2026-08-29T21:04:11Z"
    }

### `availability`

`online` while the scanner runs. The scanner names this topic as its
MQTT Last Will with `offline` as the payload, so a scanner the kubelet
killed reads `offline`. The report it left behind stands: the counts
describe the volume, and they hold until the next walk replaces them.
