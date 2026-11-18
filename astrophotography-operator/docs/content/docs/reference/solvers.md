---
title: Solver
weight: 105
toc: true
---

<!-- Generated from deploy/solvers-crd.yaml by crdref. Do not edit. -->

# `Solver`

A `Solver` plate-solves the frames of the targets in its namespace whose `spec.centering.solver` is the name of this `Solver`. `astrophotography-operator` runs one astrometry.net pod that reads the index files from the claim in `spec.indexFiles`, and a `Service` for that pod. A worker beside each reservation's outbox finds the stars in each frame, so the solver receives a list of stars and never the frame itself.

## spec

The claim that holds the index files, and how the solver searches.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--indexfiles"></span>`indexFiles` | [object](#specindexfiles) | yes | A claim, and a directory in it, that holds astrometry.net's index files. The solver reads every `index-*.fits` file in the directory and watches the directory, so it loads a file that you copy in later when the writer closes the file. The watch does not report a write that another node makes to a network file system, such as NFS. After such a write, restart the solver's `Deployment`. |
| <span id="spec--search"></span>`search` | [object](#specsearch) | no | How far from the expected center the solver searches. The operator first asks for a search within the first radius around the position where the frame should point. When the frame does not solve, it tries each next radius, and then the whole sky when `blind` is `true`. A frame that cannot be solved uses up to 60 seconds of the solver's CPU time in each attempt, which delays the solves of every other telescope. |

### spec.indexFiles

A claim, and a directory in it, that holds astrometry.net's index files. The solver reads every `index-*.fits` file in the directory and watches the directory, so it loads a file that you copy in later when the writer closes the file. The watch does not report a write that another node makes to a network file system, such as NFS. After such a write, restart the solver's `Deployment`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specindexfiles--claimname"></span>`claimName` | string | yes | The name of a `PersistentVolumeClaim` in the solver's namespace. The solver's pod mounts it read-only. |
| <span id="specindexfiles--path"></span>`path` | string | no | The directory in the claim that holds the index files. The default is the root of the claim. Default: ``. |

### spec.search

How far from the expected center the solver searches. The operator first asks for a search within the first radius around the position where the frame should point. When the frame does not solve, it tries each next radius, and then the whole sky when `blind` is `true`. A frame that cannot be solved uses up to 60 seconds of the solver's CPU time in each attempt, which delays the solves of every other telescope.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsearch--radii"></span>`radii` | []number | no | The search radius of each attempt, in degrees, in the order that the operator tries them. The default is `[5, 10]`. |
| <span id="specsearch--blind"></span>`blind` | boolean | no | Whether to search the whole sky after the attempt with the last radius fails. The default is `true`. Default: `true`. |

## status

The state that `astrophotography-operator` reports. Only the operator writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the status reflects. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The solver's `Service`. It is set while the pod is ready. |
| <span id="status--indexes"></span>`indexes` | [\[\]object](#statusindexes) | no | The series of index files in the claim. The solver reads the series from the header of each file, and updates this list when a file is added to or removed from the directory. |
| <span id="status--series"></span>`series` | string | no | The series in `status.indexes`, joined by commas, such as `4207,4208`. The `INDEXES` column of `kubectl get` shows this field. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` with the reason `Loaded` when the solver's pod is ready and has loaded at least one index file. Otherwise it is `False` with the reason `ClaimMissing`, `NoIndexFiles`, or `PodNotReady`. |

### status.endpoint

The solver's `Service`. It is set while the pod is ready.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusendpoint--service"></span>`service` | string | yes | The name of the `Service`. |
| <span id="statusendpoint--host"></span>`host` | string | yes | The DNS name of the `Service` in the cluster. |
| <span id="statusendpoint--port"></span>`port` | integer | yes | The `Service`'s port. |

### status.indexes[]

One series of index files and its files in the claim.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusindexes--id"></span>`id` | integer | yes | The series number, such as `4207`, from the `INDEXID` header of each file. |
| <span id="statusindexes--files"></span>`files` | integer | yes | The number of files of the series in the claim. |
| <span id="statusindexes--scalearcmin"></span>`scaleArcmin` | [object](#statusindexesscalearcmin) | yes | The range of sizes of the star quads in the series, in arcminutes. |

#### status.indexes[].scaleArcmin

The range of sizes of the star quads in the series, in arcminutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusindexesscalearcmin--low"></span>`low` | number | yes | The smallest size, in arcminutes. |
| <span id="statusindexesscalearcmin--high"></span>`high` | number | yes | The largest size, in arcminutes. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The type of the condition, such as `Ready`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition is met: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | A one-word CamelCase reason for the status. |
| <span id="statusconditions--message"></span>`message` | string | yes | A message for a person that gives details of the reason. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write that sets the same `status` keeps this time. |
