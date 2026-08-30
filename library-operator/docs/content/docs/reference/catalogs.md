---
title: Catalogs
weight: 20
toc: true
---

<!-- Generated from deploy/catalogs-crd.yaml by crdref. Do not edit. -->

A `Catalog` is a namespace's shared catalog: one Corrosion cluster that
every `Library` in the namespace writes into. Declare one `Catalog` in a
namespace. It sizes the durable volume every catalog agent takes, and it
owns the namespace's catalog `Service` and `EndpointSlice`.

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
`StorageClass`.

The namespace's shared catalog. Declare one Catalog in a namespace, and every Library in it writes into that catalog.

## spec

Where the catalog is stored and how large each agent's copy is.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--storage"></span>`storage` | [object](#specstorage) | yes | The volume every catalog agent in the namespace takes. |

### spec.storage

The volume every catalog agent in the namespace takes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specstorage--size"></span>`size` | string | no | The size of each agent's catalog volume. Small by default. Default: `1Gi`. |
| <span id="specstorage--storageclassname"></span>`storageClassName` | string | no | The StorageClass each agent's catalog volume binds to. Omitted, the cluster's default binds it. |

## status

The cluster the Catalog stands, written only by the library operator.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--members"></span>`members` | []string | no | The catalog agent pods that are members of the namespace's cluster. |
| <span id="status--storagesize"></span>`storageSize` | string | no | The storage size the agents were given. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The typed observations the operator keeps on this Catalog, in the standard Kubernetes form. Ready is True when the Catalog stands the namespace's cluster, and False with the reason ManyCatalogs when the namespace holds more than one Catalog. |

### status.conditions[]

The typed observations the operator keeps on this Catalog, in the standard Kubernetes form. Ready is True when the Catalog stands the namespace's cluster, and False with the reason ManyCatalogs when the namespace holds more than one Catalog.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
