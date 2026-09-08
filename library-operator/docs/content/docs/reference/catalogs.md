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
| <span id="spec--storage"></span>`storage` | [object](#specstorage) | yes | The catalog of record every other agent copies, held on one claim that every durable copy mounts. The size is also the size of every working copy, and the class is the default class for the progress and libraries claims. |
| <span id="spec--progress"></span>`progress` | [object](#specprogress) | no | The claim the progress store runs on: who watched what, and how far. Each field defaults to the field of the same name under storage, so a Catalog that names neither keeps the store on the catalog's class at the catalog's size. |
| <span id="spec--libraries"></span>`libraries` | [object](#speclibraries) | no | The claims each Library's scan and enrichment Jobs run on. Each is a working copy of the whole catalog that a Job rebuilds from the catalog of record, so a namespace that keeps the catalog of record on a durable class keeps these on a node-local class such as local-path. |
| <span id="spec--screens"></span>`screens` | [object](#specscreens) | no | The settings every screen pod in the namespace takes. |
| <span id="spec--jellyfin"></span>`jellyfin` | [object](#specjellyfin) | no | The Jellyfin server this namespace keeps playback progress with, in both directions. A Catalog that names one makes the operator stand a pod and a Service named after the Catalog with the suffix -jellyfin, beside the progress store. The pod records what Jellyfin reports into the progress store, and writes what a screen played back to Jellyfin. Omitted, neither stands, and the operator deletes the pair it stood before. |

### spec.storage

The catalog of record every other agent copies, held on one claim that every durable copy mounts. The size is also the size of every working copy, and the class is the default class for the progress and libraries claims.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specstorage--size"></span>`size` | string | no | The size of each agent's catalog volume. Small by default. Default: `1Gi`. |
| <span id="specstorage--storageclassname"></span>`storageClassName` | string | no | The StorageClass each agent's catalog volume binds to. Omitted, the cluster's default binds it. A class the per-node driver serves, whose provisioner is per-node.liken.sh, lets the namespace stand more than one copy of the catalog, because every copy then holds a directory of its own on the node it runs on. The operator reads the provisioner of the class and never matches its name. |
| <span id="specstorage--claimname"></span>`claimName` | string | no | An existing PersistentVolumeClaim in this namespace for every copy of the catalog to mount, in place of the one the operator provisions. The operator creates no claim and no volume when it is set. |
| <span id="specstorage--replicas"></span>`replicas` | integer | no | How many durable copies of the catalog the namespace stands. The copies are peers that Corrosion syncs from one another, and every copy mounts one claim of storage.size, named after the Catalog with the suffix -catalog. More than one copy needs a per-node class. On any other class the namespace stands one copy, and the Ready condition is False with the reason ClassNotPerNode. No two copies share a node, so a copy the scheduler cannot place stays Pending and the Catalog is not Ready. A copy on a node that stays NotReady for ten minutes is deleted and stood again elsewhere. It takes the claim with it only on a class that binds the claim to the node. A copy taken away by a lower count loses its pod alone, because the claim serves the copies that remain. Default: `1`. |

### spec.progress

The claim the progress store runs on: who watched what, and how far. Each field defaults to the field of the same name under storage, so a Catalog that names neither keeps the store on the catalog's class at the catalog's size.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specprogress--size"></span>`size` | string | no | The size of the progress claim, in a binary unit such as 256Mi. The progress rows are small next to the catalog, so a namespace that keeps both central stores on a durable class names a smaller size here. Omitted, the claim takes storage.size. A size change reaches a new claim and not a standing one, because a bound claim's spec is immutable; delete the standing claim and the next pass creates it at the new size. |
| <span id="specprogress--storageclassname"></span>`storageClassName` | string | no | The StorageClass the progress claim binds to. Omitted, the claim takes storage.storageClassName, and when that is also omitted the cluster's default binds it. A per-node class lets the namespace stand more than one copy of the progress store, on the same terms as storage.storageClassName. |
| <span id="specprogress--replicas"></span>`replicas` | integer | no | How many durable copies of the progress store the namespace stands, on the same terms as storage.replicas, against the class this block names. Every copy mounts one claim, named after the Catalog with the suffix -progress. More than one copy needs a per-node class here as well, and on any other class the Ready condition is False with the reason ClassNotPerNode. The first copy records what crosses the bus, and every copy after it holds the rows. Default: `1`. |

### spec.libraries

The claims each Library's scan and enrichment Jobs run on. Each is a working copy of the whole catalog that a Job rebuilds from the catalog of record, so a namespace that keeps the catalog of record on a durable class keeps these on a node-local class such as local-path.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speclibraries--storageclassname"></span>`storageClassName` | string | no | The StorageClass a Library's scan and enrichment claims bind to. Omitted, they take storage.storageClassName, and when that is also omitted the cluster's default binds them. There is no size here: every agent holds the whole catalog, so both claims take storage.size. |

### spec.screens

The settings every screen pod in the namespace takes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specscreens--storageclassname"></span>`storageClassName` | string | no | The StorageClass both of a screen's claims bind to. Omitted, the cluster's default binds them. A node-local class such as local-path is the right one, because a screen pod is already pinned to the machine that holds its display. On such a class, a screen the scheduler refuses for five minutes loses its pod and both claims, and the next pass creates them again where the display is. On a per-node class the claims pin the pod to no node, and the operator never deletes them. |
| <span id="specscreens--artcache"></span>`artCache` | [object](#specscreensartcache) | no | The volume each screen's browser keeps its scaled art on: posters, backdrops, episode stills, logos, and headshots. A screen that restarts draws the wall from art it already scaled. |

#### spec.screens.artCache

The volume each screen's browser keeps its scaled art on: posters, backdrops, episode stills, logos, and headshots. A screen that restarts draws the wall from art it already scaled.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specscreensartcache--size"></span>`size` | string | no | The size of each screen's art claim, in a binary unit such as 2Gi. The browser is told to keep 128 MiB under it. A size change reaches new screens and not standing ones, because a bound claim's spec is immutable; delete a standing screen's claim and the next pass creates it at the new size. Default: `2Gi`. |

### spec.jellyfin

The Jellyfin server this namespace keeps playback progress with, in both directions. A Catalog that names one makes the operator stand a pod and a Service named after the Catalog with the suffix -jellyfin, beside the progress store. The pod records what Jellyfin reports into the progress store, and writes what a screen played back to Jellyfin. Omitted, neither stands, and the operator deletes the pair it stood before.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specjellyfin--url"></span>`url` | string | yes | The Jellyfin server's address, as the cluster reaches it, such as http://jellyfin.jellyfin.svc:8096. |
| <span id="specjellyfin--secretref"></span>`secretRef` | [object](#specjellyfinsecretref) | yes | The Secret in this namespace that holds a Jellyfin API key, and the key inside it. The API key is one an administrator issues on the server, because the pod writes the playback position of any user. |

#### spec.jellyfin.secretRef

The Secret in this namespace that holds a Jellyfin API key, and the key inside it. The API key is one an administrator issues on the server, because the pod writes the playback position of any user.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specjellyfinsecretref--name"></span>`name` | string | yes | The Secret's name, in this Catalog's own namespace. |
| <span id="specjellyfinsecretref--key"></span>`key` | string | no | The key inside that Secret. When omitted, token. Default: `token`. |

## status

The cluster the Catalog stands, written only by the library operator.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--members"></span>`members` | []string | no | The pods that are members of the namespace's catalog cluster: the durable copies of the catalog, the pods of the Jobs that are running, and the screen pods. |
| <span id="status--storagesize"></span>`storageSize` | string | no | The storage size the agents were given. |
| <span id="status--replicas"></span>`replicas` | [object](#statusreplicas) | no | The durable copies of the namespace's two stores: for each, the count that is up beside the count the Catalog asks for. |
| <span id="status--screens"></span>`screens` | [\[\]object](#statusscreens) | no | One entry per screen pod in the namespace, in Player order: the Player it draws for, the claim its catalog agent runs on, the claim its art cache is on, the node it runs on, and its phase. A screen whose namespace has no single Catalog runs on emptyDirs and names neither claim. |
| <span id="status--jellyfin"></span>`jellyfin` | [object](#statusjellyfin) | no | The one-time backfill of the progress the namespace's Jellyfin server already held before this operator recorded it: the server it ran against, where it stands, and when it finished. Present only while spec.jellyfin names a server. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The typed observations the operator keeps on this Catalog, in the standard Kubernetes form. Ready is True when every durable copy of the catalog runs with every container ready. It is False with the reason ClassNotPerNode when the Catalog asks for copies of a store on a class that cannot hold more than one, and otherwise False with the reason PodPending, PodFailed, or ManyCatalogs, naming the first copy that is not up. |

### status.replicas

The durable copies of the namespace's two stores: for each, the count that is up beside the count the Catalog asks for.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreplicas--catalog"></span>`catalog` | [object](#statusreplicascatalog) | no | The copies of the catalog. |
| <span id="statusreplicas--progress"></span>`progress` | [object](#statusreplicasprogress) | no | The copies of the progress store. |

#### status.replicas.catalog

The copies of the catalog.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreplicascatalog--ready"></span>`ready` | integer | no | How many copies run with every container ready. |
| <span id="statusreplicascatalog--wanted"></span>`wanted` | integer | no | How many copies the Catalog asks for, from storage.replicas. The count is the one the Catalog states, so a Catalog whose class cannot hold more than one copy reports the copies it asked for beside the one that is up. |

#### status.replicas.progress

The copies of the progress store.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreplicasprogress--ready"></span>`ready` | integer | no | How many copies run with every container ready. |
| <span id="statusreplicasprogress--wanted"></span>`wanted` | integer | no | How many copies the Catalog asks for, from progress.replicas. |

### status.screens[]

One entry per screen pod in the namespace, in Player order: the Player it draws for, the claim its catalog agent runs on, the claim its art cache is on, the node it runs on, and its phase. A screen whose namespace has no single Catalog runs on emptyDirs and names neither claim.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusscreens--player"></span>`player` | string | no | The Player the screen draws for. |
| <span id="statusscreens--claim"></span>`claim` | string | no | The claim the screen's catalog agent runs on, or empty for a screen on an emptyDir. |
| <span id="statusscreens--artclaim"></span>`artClaim` | string | no | The claim the screen's art cache is on, or empty for a screen on an emptyDir. |
| <span id="statusscreens--node"></span>`node` | string | no | The node the screen pod runs on. On a node-local class it is the node both of its claims are bound to. |
| <span id="statusscreens--phase"></span>`phase` | string | no | The screen pod's phase, as the kubelet reports it. |

### status.jellyfin

The one-time backfill of the progress the namespace's Jellyfin server already held before this operator recorded it: the server it ran against, where it stands, and when it finished. Present only while spec.jellyfin names a server.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusjellyfin--server"></span>`server` | string | no | The server address the backfill ran against, from spec.jellyfin.url. A Catalog that changes the address runs the backfill again against the new one. |
| <span id="statusjellyfin--backfill"></span>`backfill` | string | no | Where the backfill stands. Pending waits for every durable copy of the progress store to be up. Running means the Job is running. Failed means the Job failed past its backoff limit, and the operator deletes it and creates it again after a wait that doubles each time. Finished means the Job exited zero. To run the backfill again against the same server, clear status.jellyfin. |
| <span id="statusjellyfin--backfilled"></span>`backfilled` | string | no | When the backfill finished, in UTC. Absent until it did. |

### status.conditions[]

The typed observations the operator keeps on this Catalog, in the standard Kubernetes form. Ready is True when every durable copy of the catalog runs with every container ready. It is False with the reason ClassNotPerNode when the Catalog asks for copies of a store on a class that cannot hold more than one, and otherwise False with the reason PodPending, PodFailed, or ManyCatalogs, naming the first copy that is not up.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
