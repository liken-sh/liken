---
title: Solvers
weight: 20
---

# Solvers

A slew leaves the telescope near a target, not exactly on it. To center
the target, the operator takes a short frame, plate-solves it to find
where the telescope points, syncs the mount to that position, and
slews again, until the error is within the target's
`spec.centering.tolerance`. A `Solver` is the plate solver that a
target selects in `spec.centering.solver`.
[Imaging](/docs/concepts/imaging/#centering) describes each round of
centering, [Set up a solver](/docs/guides/set-up-a-solver/) gives the
steps, and [`Solver`](/docs/reference/solvers/) lists every field.

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

The solver is [astrometry.net](https://astrometry.net/). Its index
files can take gigabytes, and a single frame from a camera can be tens
of megabytes on the machine at the pier. astrometry.net already works
in two steps: finding the stars reads the whole frame and writes a
list of star positions of a few kilobytes, and solving reads only that
list.

So the operator runs two kinds of pod, both from the image
`astrophotography-operator-worker`, which contains astrometry.net and
SourceExtractor:

* A worker for each reservation, next to the reservation's outbox. Its
  `Deployment` and `Service` are named `<reservation>-astrophotography`,
  in the reservation's namespace. It mounts the outbox claim at
  `/outbox`, finds the stars of a frame with SourceExtractor, and
  writes the star list with `solve-field`. A `local-path` outbox is
  bound to the pier machine, so the worker runs on the camera's node
  and the frames never cross the network.
* A solver for each `Solver`, which mounts the index files. Its
  `Deployment` and `Service` are named `<solver>-solver`, in the
  `Solver`'s namespace. It runs `astrometry-engine` on one star list at
  a time, so concurrent requests queue.

The operator copies each star list from the worker to the solver, so
only kilobytes cross the network between nodes, and the worker and the
solver never connect to each other. Both serve plain HTTP on port
`8080`, and only the operator connects to them. If you write a
`NetworkPolicy` for the namespace, allow the operator's pod in
`liken-system` to reach port `8080` of the pods with the labels
`astrophotography.liken.sh/solver` and
`astrophotography.liken.sh/worker`.

A worker runs while its reservation's phase is `Activating` or
`Ready` and the reservation's session has an outbox. The operator
deletes the worker when the reservation ends, fails, or gets another
holder. When the worker's pod cannot start, such as when the outbox
claim's access mode is `ReadWriteOncePod`, the reservation gets the
`Warning` `AstrophotographyWorkerPending` with the scheduler's
message.

## The index files

`spec.indexFiles.claimName` names a `PersistentVolumeClaim` that holds
astrometry.net's index files, the `index-*.fits` files, and the
optional `spec.indexFiles.path` names a directory in it. You fill the
claim yourself, so a night needs no network access.

Each series of index files matches quads of stars in one range of
sizes, and a quad's stars must all fall in the frame. So choose series
whose quads fit inside your camera's field. For example, a 480 mm
refractor with the CCD simulator's 5.2 µm pixels has a field of 47.7
by 38.1 arcminutes, and the 2MASS series 4207 (quads of 22 to 30
arcminutes) and 4208 (30 to 42 arcminutes) both fit in it.

The solver reads the header of every index file in the directory to
find its series, so a file's name only has to match `index-*.fits`.
The operator lists each series in the `Solver`'s `status.indexes` with
its range of scales.

The solver's pod watches the directory with inotify, so you can add
index files while it runs, and a `Job` can download them into the
claim. A file is added to the list when the program that writes it
closes it or renames it into the directory. It is removed when it is
deleted, renamed away, or modified in place. A file that is shorter
than its header says, such as one still downloading when the pod
starts, stays off the list until its writer closes it, so the engine
never reads a partial file. inotify
reports writes from any pod on the solver's node, but not writes that
another node makes to a network file system such as NFS. After such a
copy, restart the solver's `Deployment`.

The operator keeps one streaming request open to each solver pod. The
pod sends its list of index files when it becomes ready and again each
time the list changes, and the operator writes the list into the
`Solver`'s status. The request goes to the pod's own address, because
the `Service` routes to a new pod only some seconds after the pod
becomes ready.

## The search

The operator tells the solver where it expects the frame to point and
how far from that point to search. A wider search takes longer on a
frame that cannot solve. A field that does not solve can occupy the
solver for up to 60 seconds of CPU time, and every other telescope's
solve waits behind it.

`spec.search.radii` lists the search radius of each attempt, in
degrees, and defaults to `[5, 10]`. When a frame does not solve within
one radius, the operator asks the worker for the star list again and
tries the next radius. After the last radius, when
`spec.search.blind` is `true`, which is the default, it asks the
solver to search the whole sky. When SourceExtractor finds no star in
a frame, the attempts stop at once. A frame whose header position is
farther from the real position than a radius cannot solve at that
radius.

## The condition

`Ready` is `True` with the reason `Loaded` when the solver's pod is
ready and has read at least one index file. Its message gives the
number of files and lists the series. Otherwise `Ready` is `False`
with one of these reasons:

| Reason | What it means |
|---|---|
| `ClaimMissing` | The claim that `spec.indexFiles.claimName` names does not exist, so the scheduler cannot place the pod. |
| `NoIndexFiles` | The pod started, and the directory has no `index-*.fits` file that the solver could read. |
| `PodNotReady` | The pod does not exist yet, is waiting to start, or does not respond. The message gives the scheduler's reason or the error. |

Each transition posts one `Event` on the `Solver`. The `Event` is a
`Warning` for `ClaimMissing`, for `NoIndexFiles`, and for a
`PodNotReady` whose pod cannot start or does not respond. It is
`Normal` for a pod that is still starting.
