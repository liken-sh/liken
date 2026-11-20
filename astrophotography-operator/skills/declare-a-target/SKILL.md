---
name: declare-a-target
description: "Declare a Target and the archive claim for its frames: the object and its J2000 coordinates, the sequences of filter, exposure, and count, the optical train, dithering, centering, and FITS keywords. Then check its progress, choose which target goes first with priority, pause it with suspend, edit it during a night, and delete it. Use when a person wants to image an object, when the API server refuses a Target, when a target never starts, or when changing, pausing, or deleting a target."
---

This skill is the guide at https://liken.sh/astrophotography/docs/guides/declare-a-target/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Declare a target

A `Target` says what you want to image and how many frames you want.
You declare it once, and each reservation for this operator in the same
namespace images it until it has all its frames, over as many nights as
that takes. At the end of this guide you have a `Target` and the archive
claim for its frames, and the target shows how many frames it still
needs. No telescope moves until
[Image a target](https://liken.sh/astrophotography/docs/guides/image-a-target/) reserves one.

You need:

* `astrophotography-operator` installed
  ([Install the operator](https://liken.sh/astrophotography/docs/guides/install/)).
* Your equipment described in `observatory-operator`, with an
  `OpticalTrain` for the imaging camera and a `FilterWheel` on that
  train if you use filters.
  [Describe your equipment](https://liken.sh/observatory/docs/guides/describe-your-equipment/)
  covers it. This guide uses the example observatory of INDI
  simulators from
  [`observatory-operator`'s install guide](https://liken.sh/observatory/docs/guides/install/),
  in the namespace `observatory`.
* The object's J2000 coordinates, as SIMBAD or KStars print them.

The target, its archive claim, and the reservation that images it must
all be in one namespace, because the operator's pods mount the claims,
and a pod can mount only the claims of its own namespace.

## Find the train and the filter names

A target refers to the `OpticalTrain` it is imaged through, and each
sequence refers to a filter by its name in the filter wheel. List both:

```sh
kubectl get opticaltrains -n observatory
kubectl get filterwheels -n observatory \
  -o jsonpath='{range .items[*]}{.metadata.name} {.spec.opticalTrain}: {.spec.filters}{"\n"}{end}'
```

```
east east-imaging: ["Luminance","Red","Green","Blue","H_Alpha","OIII","SII","Dark"]
```

Use the train of the imaging camera, not the guide camera's. A target
with no `opticalTrain` is imaged on any telescope that has exactly one
imaging camera.

## Create the archive claim

The archive is the `PersistentVolumeClaim` that keeps the frames after
the night, together with a catalog of every frame. You choose the claim,
and you cannot change it after you create the target, because the
target's counts come from the catalog in that claim.

A promotion `Job` mounts the archive and the reservation's outbox in
one pod, so a node must be able to mount both. A `ReadWriteMany`
network share works from any node. A `local-path` claim works when the
outbox is a `local-path` claim too, because both bind to the machine
at the pier.
[Where the `Job` runs](https://liken.sh/astrophotography/docs/concepts/archive/#where-the-job-runs)
gives the detail.

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: archive
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources:
    requests:
      storage: 10Gi
EOF
```

A `local-path` archive stays `Pending` until the first promotion `Job`
mounts it.

## Write the target

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Target
metadata:
  name: m42
spec:
  object: M42
  coordinates: {ra: "05:35:17.3", dec: "-05:23:28"}
  opticalTrain: east-imaging
  sequences:
    - {filter: Luminance, exposure: 10s, count: 3}
    - {filter: Red, exposure: 10s, count: 2}
  archive:
    claimName: archive
    path: astro
EOF
```

* `object` is the object's name. It goes into each frame's header as
  `OBJECT`, and into the file names.
* `coordinates` are J2000: the right ascension in hours as
  `HH:MM:SS.s`, and the declination in degrees as `±DD:MM:SS.s`. The
  API server refuses any other form, such as decimal hours, with a
  message that gives the form.
* Each sequence is a filter, an exposure, and a count, and can add
  `binning`, `gain`, and `offset`. A field you leave out keeps the
  camera's own value. The operator finishes one sequence before it
  starts the next, because each change of filter costs a refocus.
* `archive.path` is a directory in the claim. The frames and the
  catalog go under it.

The API server fills in the defaults, such as the file name format
and `archive.promotion: Continuous`. To choose the file names, see
[Find and verify your frames](https://liken.sh/astrophotography/docs/guides/find-your-frames/#choose-the-file-names).
[Targets](https://liken.sh/astrophotography/docs/concepts/targets/) explains each field, and
[`Target`](https://liken.sh/astrophotography/docs/reference/targets/) lists them all.

The operator images a target only while it is above the telescope's
horizon. That is the `Horizon` named in the `Telescope`'s
`spec.horizon`, or 0° when `spec.horizon` is not set. A target below the
horizon gets `Imaging` `False` with the reason `BelowHorizon`, and its
message gives its altitude and when it rises.
[The horizon](https://liken.sh/astrophotography/docs/concepts/imaging/#the-horizon) gives the detail.

## Check the target

The operator writes the target's progress right after you create it:

```sh
kubectl get target m42 -n observatory
```

```
NAME   OBJECT   PROGRESS   RESERVATION   COMPLETE   AGE
m42    M42      0/5                      False      10s
```

`PROGRESS` is the number of frames in the archive, out of the number
you asked for. `Complete` is `False` with the reason `WorkLeft` until every
sequence has its count in the archive. `kubectl get target -o wide`
adds the `Imaging` reason and the priority.

## Add guiding, dithering, centering, and keywords

These fields are optional, and go in the same `spec`:

```yaml
spec:
  dither: {every: 2, pixels: 5}
  centering: {solver: lab, tolerance: 30}
  fitsKeywords:
    - {name: PROGRAM, value: Orion widefield}
```

* Guiding needs nothing in the target. When the telescope has a
  `Guider`, the operator guides every frame with PHD2.
  [Guide with PHD2](https://liken.sh/observatory/docs/guides/guide-with-phd2/)
  sets up the `Guider`.
* `dither` moves PHD2's lock position by up to `pixels` guide camera
  pixels after every `every` frames, so a hot pixel lands on a
  different part of the sky in each frame and stacking removes it. A
  telescope with no `Guider` does not dither.
* `centering` plate-solves a short frame after each slew and corrects
  the mount until the error is at most `tolerance` arcseconds.
  `solver` is the name of a `Solver`, which
  [Set up a solver](https://liken.sh/astrophotography/docs/guides/set-up-a-solver/) creates. With no
  `centering`, the frames are taken wherever the slew ends.
* `fitsKeywords` adds keywords to each frame's header, after the ones
  that `observatory-operator` writes for the observatory, the
  telescope, the camera, and the reservation, so a keyword here
  replaces one of the same name from those. The API server refuses
  `OBJECT` and `FRAMEID`, which this operator writes, and each keyword
  the camera's driver measures, such as `EXPTIME`.
  [FITS keywords](https://liken.sh/observatory/docs/concepts/fits-keywords/)
  explains which resource writes which keywords.

## Choose which target goes first

A reservation images one target at a time. From the targets it can
image, it picks the one with the highest `priority`. A tie goes to the
oldest target, then to the first name in alphabetical order. The
default priority is `0`:

```sh
kubectl patch target m42 -n observatory --type merge -p '{"spec":{"priority":10}}'
```

The reservation keeps its target until the target has no work left,
is suspended, drops below the horizon, or fails. A target with a higher
priority does not interrupt the current one. To switch at once, suspend
the current target.

## Pause a target

`suspend` stops the target from being imaged and keeps its progress:

```sh
kubectl patch target m42 -n observatory --type merge -p '{"spec":{"suspend":true}}'
```

A reservation that images the target finishes the current exposure,
releases the target with `Imaging` `False` and the reason `Suspended`,
and moves to the next target. Set `suspend` to `false` to make it
eligible again.

## Edit a target

You can edit a target at any time. During a night, the operator reads
the target again before each frame:

* A change to `sequences` takes effect at the next frame. A frame
  counts toward a sequence by its filter, exposure, binning, gain, and
  offset, so you can reorder sequences or raise a count, and the frames
  that still match keep counting.
* New `coordinates` stop guiding, slew the mount again, and center the
  target again when the target sets `centering`.
* A new `archive.format` applies to the next frames. Frames already
  taken keep their names.
* `archive.claimName` cannot change. To use another claim, create
  another target.

When a target fails on a reservation, with a reason such as `NoFilter`
or `SlewFailed`, that reservation does not pick it again until you edit
the target's spec. `status.failed.reservation` is the name of that
reservation, and other reservations can still pick the target.
[Troubleshoot](https://liken.sh/astrophotography/docs/guides/troubleshoot/) gives the fix for each
reason.

## Delete a target

Delete a target when you no longer need its status. The frames and the
catalog in the archive stay:

```sh
kubectl delete target m42 -n observatory
```

The operator puts the finalizer `astrophotography.liken.sh/frames` on
each target, so the delete waits while frames of the target are still
in an outbox. During the wait, no reservation picks the target, a
reservation that images it finishes the current exposure and releases
it, and promotion moves the remaining frames to the archive. `Imaging`
is `False` with the reason `Deleting`, and the target gets the `Event`
`Deleting`. The operator removes the finalizer, and Kubernetes deletes
the target, when no entry of `status.outboxes` has `pending` frames:

```sh
kubectl get target m42 -n observatory -o jsonpath='{.status.outboxes}'
```

With `promotion: AfterReservation`, the frames move only after the
reservation that writes to the outbox ends, so the delete also waits
for that reservation to end. When a frame cannot reach the archive, the delete waits until
you fix the cause, or until you remove the finalizer:
[A target does not delete](https://liken.sh/astrophotography/docs/guides/troubleshoot/#a-target-does-not-delete)
gives both.
