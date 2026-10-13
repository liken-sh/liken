---
title: Solver
weight: 105
toc: true
---

<!-- Generated from deploy/solvers-crd.yaml by crdref. Do not edit. -->

# `Solver`

A `Solver` plate-solves the frames of the targets in its namespace that name it in `spec.centering.solver`. `astrophotography-operator` runs one pod of astrometry.net that reads the index files on the claim that `spec.indexFiles` names, and a `Service` in front of it. Only star lists of a few kilobytes reach the solver, because a worker beside each reservation's outbox finds the stars in each frame.

## spec

Where the index files are.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--indexfiles"></span>`indexFiles` | [object](#specindexfiles) | yes | A claim, and a directory in it, that holds astrometry.net's index files. The solver reads every `index-*.fits` in the directory when its pod starts. To add index files, copy them into the directory and restart the solver's `Deployment`. |
| <span id="spec--search"></span>`search` | [object](#specsearch) | no | How far from the expected center a solve looks. The operator asks the solver for the first radius around the position it expects. While the frame does not solve, it asks for each next radius, and then, when `blind` is `true`, for a solve of the whole sky. A field that cannot solve holds the solver for up to 60 s of CPU time at each attempt, so each attempt delays every other telescope's solve. |

### spec.indexFiles

A claim, and a directory in it, that holds astrometry.net's index files. The solver reads every `index-*.fits` in the directory when its pod starts. To add index files, copy them into the directory and restart the solver's `Deployment`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specindexfiles--claimname"></span>`claimName` | string | yes | A `PersistentVolumeClaim` in the solver's namespace. The solver's pod mounts it read-only. |
| <span id="specindexfiles--path"></span>`path` | string | no | The directory in the claim that holds the index files. The default is the claim's root. Default: ``. |

### spec.search

How far from the expected center a solve looks. The operator asks the solver for the first radius around the position it expects. While the frame does not solve, it asks for each next radius, and then, when `blind` is `true`, for a solve of the whole sky. A field that cannot solve holds the solver for up to 60 s of CPU time at each attempt, so each attempt delays every other telescope's solve.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsearch--radii"></span>`radii` | []number | no | The radius of each attempt, in degrees, in order. The default is `[5, 10]`. A frame of the CCD simulator whose header pointed 6.8° from the stars it drew did not solve at 5°, and solved at 10°. |
| <span id="specsearch--blind"></span>`blind` | boolean | no | Whether to try a solve of the whole sky after the last radius. The default is `true`. Default: `true`. |

## status

What `astrophotography-operator` reports. A person never writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the status reflects. |
| <span id="status--endpoint"></span>`endpoint` | [object](#statusendpoint) | no | The solver's `Service`, while its pod is ready. |
| <span id="status--indexes"></span>`indexes` | [\[\]object](#statusindexes) | no | Each series of index files in the claim, which the solver reads from the headers of each file when its pod starts. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Ready` is `True` with the reason `Loaded` when the solver's pod is ready and holds at least one index file. Otherwise it is `False` with the reason `ClaimMissing`, `NoIndexFiles`, or `PodNotReady`. |

### status.endpoint

The solver's `Service`, while its pod is ready.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusendpoint--service"></span>`service` | string | yes | The name of the `Service`. |
| <span id="statusendpoint--host"></span>`host` | string | yes | The `Service`'s DNS name in the cluster. |
| <span id="statusendpoint--port"></span>`port` | integer | yes | The `Service`'s port. |

### status.indexes[]

One series of index files.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusindexes--id"></span>`id` | integer | yes | The series, such as `4207`, from each file's `INDEXID` header. |
| <span id="statusindexes--files"></span>`files` | integer | yes | The files of the series in the claim. |
| <span id="statusindexes--scalearcmin"></span>`scaleArcmin` | [object](#statusindexesscalearcmin) | yes | The range of sizes of the series' quads of stars, in arcminutes. |

#### status.indexes[].scaleArcmin

The range of sizes of the series' quads of stars, in arcminutes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusindexesscalearcmin--low"></span>`low` | number | yes | The smallest size, in arcminutes. |
| <span id="statusindexesscalearcmin--high"></span>`high` | number | yes | The largest size, in arcminutes. |

### status.conditions[]

One condition, in the shape of `metav1.Condition`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, such as `Ready`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the condition holds: `True`, `False`, or `Unknown`. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the spec that the condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | yes | One word in CamelCase for the cause. |
| <span id="statusconditions--message"></span>`message` | string | yes | The cause, for a person to read. It can be empty. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When `status` last changed. A write with the same status keeps this time. |
