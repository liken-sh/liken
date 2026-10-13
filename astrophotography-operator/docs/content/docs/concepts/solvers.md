---
title: Solvers
weight: 20
---

# Solvers

A mount's slew lands near a target, not on it. To center the target,
the operator takes a short frame, plate-solves it to find where the
telescope points, syncs the mount to that position, and slews again,
until the error is within the target's `spec.centering.tolerance`. A
`Solver` is the plate solver that a target names in
`spec.centering.solver`. [Imaging](/docs/concepts/imaging/#centering)
gives the rounds of the centering,
[Set up a solver](/docs/guides/set-up-a-solver/) gives the steps, and
[`Solver`](/docs/reference/solvers/) lists every field.

```yaml
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Solver
metadata:
  name: lab
  namespace: observatory
spec:
  indexFiles:
    claimName: astrometry-indexes
```

## Why a solve is split in two

The solver is [astrometry.net](https://astrometry.net/). It needs
index files, which run to gigabytes for a wide range of fields, and a
frame of a camera is tens of megabytes on the machine at the pier.
astrometry.net already splits its work: finding the stars reads the
whole frame and writes a list of star positions of a few kilobytes,
and solving reads only that list.

So the operator runs two kinds of pod, both in the image
`astrophotography-operator-worker`, which holds astrometry.net and
SourceExtractor beside the operator's own program:

* A worker for each reservation the operator holds, beside the
  reservation's outbox. Its `Deployment` and `Service` are named
  `<reservation>-astrophotography`, in the reservation's namespace. It
  mounts the outbox claim at `/outbox`, finds the stars of a frame with
  SourceExtractor, and writes the star list with `solve-field`. A
  `local-path` outbox is bound to the pier machine, so the worker
  starts on the camera's node and the frames never cross the network.
* One solver for each `Solver`, which holds the index files. Its
  `Deployment` and `Service` are named `<solver>-solver`, in the
  `Solver`'s namespace. It runs one `astrometry-engine` for each star
  list it receives, and one at a time, so each request waits its turn.

The operator carries each star list from the worker to the solver, so
the two never talk to each other, and only kilobytes cross the
network between nodes. Both serve plain HTTP on port `8080`, to the
operator alone. If you write a `NetworkPolicy` for the namespace,
allow the operator's pod in `liken-system` to reach port `8080` of the
pods with the labels `astrophotography.liken.sh/solver` and
`astrophotography.liken.sh/worker`.

A worker runs while its reservation's phase is `Activating` or
`Ready`, and its session records an outbox. The operator deletes the
worker when the reservation ends, fails, or names another holder.
When the worker's pod cannot start, such as for an outbox claim whose
access mode is `ReadWriteOncePod`, the reservation gets the `Warning`
`AstrophotographyWorkerPending` with the scheduler's message.

## The index files

`spec.indexFiles` is a claim of yours, and a directory in it, that
holds astrometry.net's index files, the `index-*.fits` files. You fill
the claim yourself, so a night runs with no network. Each series of
index files matches quads of stars of one range of sizes, and the
series to choose follows from the size of your camera's field: a
series whose quads are wider than the field's short side cannot solve
it. A refractor of 480 mm and the CCD simulator's pixels of 5.2 µm
see a field of 47.7 by 38.1 arcminutes. The 2MASS series 4207, of 22
to 30 arcminutes, and 4208, of 30 to 42 arcminutes, each solved that
field in about a second on a laptop, and 4209, of 42 to 60
arcminutes, did not. The 13 files of 4207 and 4208 take 236 MiB.

The solver reads the header of every index file in the directory when
its pod starts, and the operator lists each series in
`status.indexes` with its range of scales. The operator reads the
list once for each pod, from the pod's own address, when the pod
turns ready, because the `Service` routes to a new pod only some
seconds later. The solver reads each
file's series from its header, so only the pattern `index-*.fits`
matters in a file's name. To add index files, copy them into the
claim and restart the solver's `Deployment`.

## The search

The operator tells the solver where it expects the frame to point,
and how far from there to search. A wider search costs more time for
a frame that cannot solve, and each search of a field that does not
solve holds the solver for up to 60 seconds of CPU time, while every
other telescope's solve waits. `spec.search.radii` lists the radius of
each attempt, in degrees, and the default is `[5, 10]`. When a frame
does not solve within one radius, the operator asks the worker for
the star list again with the next radius. After the last radius, when
`spec.search.blind` is `true`, which is the default, it asks for a
solve of the whole sky. A frame in which SourceExtractor finds no star
ends the attempts at once.

A frame of the CCD simulator whose header pointed 6.8° from the stars
it drew did not solve at a radius of 5°: the engine searched for 58
seconds and found nothing. At 10°, it solved.

## The condition

`Ready` is `True` with the reason `Loaded` when the solver's pod is
ready and holds at least one index file. Its message counts the files
and names the series. Otherwise it is `False` with one of these
reasons:

| Reason | What it means |
|---|---|
| `ClaimMissing` | The claim that `spec.indexFiles.claimName` names does not exist, so the scheduler cannot place the pod. |
| `NoIndexFiles` | The pod started, and the directory holds no `index-*.fits` file. |
| `PodNotReady` | The pod does not exist yet, waits, or does not answer. The message gives the scheduler's reason or the error. |

Each transition posts one `Event` on the `Solver`. It is a `Warning`
for `ClaimMissing`, for `NoIndexFiles`, and for a `PodNotReady` whose
pod cannot start or does not answer, and `Normal` for a pod that is
starting.
