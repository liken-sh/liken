---
title: Catalogs
weight: 20
toc: true
---

<!-- Generated from deploy/catalogs-crd.yaml by crdref. Do not edit. -->

A `Catalog` is a namespace's shared catalog: one Corrosion cluster that
every `Library` in the namespace writes into. Declare one `Catalog` in a
namespace. It stands the catalog pod, the one standing member of that
cluster, which holds the namespace's catalog on a durable claim and
reports what it holds over the bus. It sizes that claim, the claim
every `Library`'s `Job`s take, and the claim every screen's agent runs
on, and it owns the pod, its claim, and the namespace's catalog
`Service` and `EndpointSlice`.

Each catalog agent holds the whole namespace's catalog, because the
cluster gossips every row to every peer. So one size covers the whole
namespace, on the `Catalog`, in place of a size on each `Library`.

    apiVersion: library.liken.sh/v1alpha1
    kind: Catalog
    metadata:
      name: catalog
      namespace: media
    spec:
      storage:
        size: 1Gi

A namespace has exactly one `Catalog`. A `Library` in a namespace with no
`Catalog` waits until one exists, and more than one `Catalog` marks every
`Catalog` in the namespace `Blocked` and stands no cluster. An empty
`storageClassName` binds each catalog volume to the cluster's default
`StorageClass`. A `claimName` names an existing claim for the catalog
pod to mount in place of the one the operator provisions. A SQLite
file on a claim served over NFS can corrupt when its node is lost, so
prefer a `StorageClass` that binds node-local storage. A
`spec.screens.storageClassName` classes the screens' claims apart from
the durable one, and omitted, the cluster's default binds them too.

The namespace's shared catalog. Declare one Catalog in a namespace, and every Library in it writes into that catalog.

## spec

Where the catalog is stored and how large each agent's copy is.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--storage"></span>`storage` | [object](#specstorage) | yes | The catalog pod's claim, the catalog of record every other agent copies. Its size is also the size of every copy, and its class is the default class for the progress and libraries claims. |
| <span id="spec--progress"></span>`progress` | [object](#specprogress) | no | The claim the progress store runs on: who watched what, and how far. Each field defaults to the field of the same name under storage, so a Catalog that names neither keeps the store on the catalog's class at the catalog's size. |
| <span id="spec--libraries"></span>`libraries` | [object](#speclibraries) | no | The claims each Library's scan and enrichment Jobs run on. Each is a working copy of the whole catalog that a Job rebuilds from the catalog of record, so a namespace that keeps the catalog of record on a durable class keeps these on a node-local class such as local-path. |
| <span id="spec--screens"></span>`screens` | [object](#specscreens) | no | The settings every screen pod in the namespace takes. |

### spec.storage

The catalog pod's claim, the catalog of record every other agent copies. Its size is also the size of every copy, and its class is the default class for the progress and libraries claims.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specstorage--size"></span>`size` | string | no | The size of each agent's catalog volume. Small by default. Default: `1Gi`. |
| <span id="specstorage--storageclassname"></span>`storageClassName` | string | no | The StorageClass each agent's catalog volume binds to. Omitted, the cluster's default binds it. |
| <span id="specstorage--claimname"></span>`claimName` | string | no | An existing PersistentVolumeClaim in this namespace for the catalog pod to mount, in place of the one the operator provisions; the operator creates none when it is set. |

### spec.progress

The claim the progress store runs on: who watched what, and how far. Each field defaults to the field of the same name under storage, so a Catalog that names neither keeps the store on the catalog's class at the catalog's size.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specprogress--size"></span>`size` | string | no | The size of the progress claim, in a binary unit such as 256Mi. The progress rows are small next to the catalog, so a namespace that keeps both central stores on a durable class names a smaller size here. Omitted, the claim takes storage.size. A size change reaches a new claim and not a standing one, because a bound claim's spec is immutable; delete the standing claim and the next pass creates it at the new size. |
| <span id="specprogress--storageclassname"></span>`storageClassName` | string | no | The StorageClass the progress claim binds to. Omitted, the claim takes storage.storageClassName, and when that is also omitted the cluster's default binds it. |

### spec.libraries

The claims each Library's scan and enrichment Jobs run on. Each is a working copy of the whole catalog that a Job rebuilds from the catalog of record, so a namespace that keeps the catalog of record on a durable class keeps these on a node-local class such as local-path.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speclibraries--storageclassname"></span>`storageClassName` | string | no | The StorageClass a Library's scan and enrichment claims bind to. Omitted, they take storage.storageClassName, and when that is also omitted the cluster's default binds them. There is no size here: every agent holds the whole catalog, so both claims take storage.size. |

### spec.screens

The settings every screen pod in the namespace takes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specscreens--storageclassname"></span>`storageClassName` | string | no | The StorageClass both of a screen's claims bind to. Omitted, the cluster's default binds them. A node-local class such as local-path is the right one, because a screen pod is already pinned to the machine that holds its display. |
| <span id="specscreens--artcache"></span>`artCache` | [object](#specscreensartcache) | no | The volume each screen's browser keeps its scaled art on: posters, backdrops, episode stills, logos, and headshots. A screen that restarts draws the wall from art it already scaled. |

#### spec.screens.artCache

The volume each screen's browser keeps its scaled art on: posters, backdrops, episode stills, logos, and headshots. A screen that restarts draws the wall from art it already scaled.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specscreensartcache--size"></span>`size` | string | no | The size of each screen's art claim, in a binary unit such as 2Gi. The browser is told to keep 128 MiB under it. A size change reaches new screens and not standing ones, because a bound claim's spec is immutable; delete a standing screen's claim and the next pass creates it at the new size. Default: `2Gi`. |

## status

The cluster the Catalog stands, written only by the library operator.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--members"></span>`members` | []string | no | The pods that are members of the namespace's catalog cluster: the catalog pod, the pods of the Jobs that are running, and the screen pods. |
| <span id="status--storagesize"></span>`storageSize` | string | no | The storage size the agents were given. |
| <span id="status--screens"></span>`screens` | [\[\]object](#statusscreens) | no | One entry per screen pod in the namespace, in Player order: the Player it draws for, the claim its catalog agent runs on, the claim its art cache is on, the node it runs on, and its phase. A screen whose namespace has no single Catalog runs on emptyDirs and names neither claim. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The typed observations the operator keeps on this Catalog, in the standard Kubernetes form; Ready is True when the catalog pod runs with every container ready, and False with the reason PodPending, PodFailed, or ManyCatalogs. |

### status.screens[]

One entry per screen pod in the namespace, in Player order: the Player it draws for, the claim its catalog agent runs on, the claim its art cache is on, the node it runs on, and its phase. A screen whose namespace has no single Catalog runs on emptyDirs and names neither claim.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusscreens--player"></span>`player` | string | no | The Player the screen draws for. |
| <span id="statusscreens--claim"></span>`claim` | string | no | The claim the screen's catalog agent runs on, or empty for a screen on an emptyDir. |
| <span id="statusscreens--artclaim"></span>`artClaim` | string | no | The claim the screen's art cache is on, or empty for a screen on an emptyDir. |
| <span id="statusscreens--node"></span>`node` | string | no | The node the screen pod runs on, which is the node both of its claims are bound to. |
| <span id="statusscreens--phase"></span>`phase` | string | no | The screen pod's phase, as the kubelet reports it. |

### status.conditions[]

The typed observations the operator keeps on this Catalog, in the standard Kubernetes form; Ready is True when the catalog pod runs with every container ready, and False with the reason PodPending, PodFailed, or ManyCatalogs.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
