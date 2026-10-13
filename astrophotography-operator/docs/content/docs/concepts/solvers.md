---
title: Solvers
weight: 20
---

# Solvers

A mount's slew lands near a target, not on it. To center the target,
the operator will take a short frame, plate-solve it to find where
the telescope points, sync the mount to that position, and slew
again, until the error is within the target's
`spec.centering.tolerance`. A `Solver` is the plate solver that a
target names in `spec.centering.solver`. The operator does not solve
yet, so this page gives the design that the resource is built for.
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

So the operator will run two kinds of pod. A worker beside each
reservation's outbox finds the stars in each frame with
SourceExtractor, and one solver pod for each `Solver` holds the index
files and solves the lists. Only kilobytes cross the network between
nodes, and the index files exist once in the cluster.

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

The solver will read every index file in the directory when its pod
starts, and list each series in `status.indexes` with its range of
scales. To add index files, copy them into the claim and restart the
solver's `Deployment`.

## The condition

`Ready` will be `True` with the reason `Loaded` when the solver's pod
is ready and holds at least one index file. Otherwise it is `False`
with the reason `ClaimMissing`, `NoIndexFiles`, or `PodNotReady`.
