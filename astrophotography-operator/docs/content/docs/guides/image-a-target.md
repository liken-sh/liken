---
title: Image a target
weight: 20
description: "Image a Target on a telescope that observatory-operator runs: declare the target and its claims, reserve the telescope for astrophotography-operator, follow the Imaging condition and the counts, and end the night. Use when a person wants the operator to take frames, when a target does not start, or when Imaging is False with a reason such as OutboxFull, ExposureFailed, NoFilter, SlewFailed, CenteringFailed, or GuidingFailed."
---

# Image a target

At the end of this guide, a telescope takes the frames of a `Target`
while you watch, and each frame waits in the reservation's outbox
until promotion moves it to your archive. You need:

* `astrophotography-operator` installed beside `observatory-operator`
  ([Install the operator](/docs/guides/install/)), and a telescope
  that `observatory-operator` runs, such as its example observatory of
  INDI simulators.
* The image `astrophotography-operator-worker` in reach of the
  cluster. The operator starts a worker for the reservation, a pod
  named `<reservation>-astrophotography` that mounts the outbox and
  answers for its free space, and the runner exposes only when the
  worker answers. [Solvers](/docs/concepts/solvers/) says where the
  worker runs and what it needs.
* For a centered target, a `Solver` that is `Ready`
  ([Set up a solver](/docs/guides/set-up-a-solver/)). Without one,
  leave out `centering`, and the frames are taken where the slew
  leaves the mount.

The operator slews, centers with a plate solve when the target asks,
guides with PHD2 when the telescope has a `Guider`, dithers when the
target asks, and exposes. A telescope with no `Guider` takes unguided
frames.
[Imaging](/docs/concepts/imaging/) explains each step.

## Declare the claims and the target

The outbox is where the camera writes each frame, and the archive is
where the frames stay. Both are claims in the namespace of your
observatory. This guide uses `observatory` and `local-path` claims:

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: frames
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: archive
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: local-path
  resources:
    requests:
      storage: 1Gi
---
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
  centering: {solver: lab, tolerance: 30}
  archive:
    claimName: archive
    path: astro
EOF
```

Each `filter` is a name in the filter wheel's `FILTER_NAME`. A target
with no `opticalTrain` is imaged on a telescope with exactly one
imaging camera. `centering` names the `Solver` and the largest error,
in arcseconds, that counts as centered.

## Reserve the telescope

Give the telescope to the operator with a `Reservation` whose holder
is `astrophotography-operator` and whose outbox is the claim above:

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: observatory.liken.sh/v1alpha1
kind: Reservation
metadata:
  name: east-tonight
spec:
  telescope: east
  holder: astrophotography-operator
  outbox:
    claimName: frames
EOF
```

`observatory-operator` starts the devices. When the reservation is
`Ready`, the operator claims the eligible target with the highest
priority:

```sh
kubectl get reservation east-tonight -n observatory
kubectl get target m42 -n observatory
```

The target's `RESERVATION` column names `east-tonight` while the
telescope images it.

## Follow the night

The `Imaging` condition says what the telescope does with the target,
and its message names the reservation and the sequence:

```sh
kubectl wait target/m42 -n observatory --for=condition=Imaging --timeout=10m
kubectl get target m42 -n observatory -o jsonpath='{.status.conditions[?(@.type=="Imaging")]}'
```

`status.sequences` counts the frames of each sequence: `pending` are
in the outbox, and `archived` are in the archive's catalog. The frame
in flight is in `status.exposure`:

```sh
kubectl get target m42 -n observatory -o jsonpath='{.status.sequences}'
kubectl get target m42 -n observatory -o jsonpath='{.status.exposure.file}'
```

`kubectl describe target m42 -n observatory` lists the transitions of
the night as `Event`s: `Slewing`, `Centering` when the target states
`centering`, `Guiding` when the telescope has a `Guider`, `Exposing`,
and `SequencesDone` when the sequences have no work left. The
reservation's own `Event`s say when the operator unparked the mount,
found the outbox full, imaged a target without centering it, or had
no target to image.

The result of the centering is in `status.centering`: the rounds, the
solved center, the error in arcseconds, and the error of each round in
`roundErrors`:

```sh
kubectl get target m42 -n observatory -o jsonpath='{.status.centering}'
```

To stop imaging a target and keep its progress, suspend it. The
operator finishes the frame in flight and releases the target:

```sh
kubectl patch target m42 -n observatory --type merge -p '{"spec":{"suspend":true}}'
```

## End the night

Delete the reservation, or let its `spec.end` pass:

```sh
kubectl delete reservation east-tonight -n observatory
```

The operator releases the target with `Imaging` `False` and the reason
`Waiting`, and `observatory-operator` parks the mount through its
procedures. A frame in flight does not count. If the driver wrote it
anyway, promotion finds it in the outbox. The next reservation of the
namespace takes the target again where it stopped.

## When Imaging is False

| Reason | What it means | What to do |
|---|---|---|
| `OutboxFull` | the outbox has less than three frames' size free | Wait for promotion to move frames. With `promotion: AfterReservation`, nothing frees space before the reservation ends: give the outbox more space. |
| `ExposureFailed` | the last exposure gave no frame; the message says why | Read the message. The runner tries the next frame after a minute. A driver that saves the frame under another name means a client changed the camera's upload settings. |
| `GuidingFailed` | PHD2 did not answer, found no star, refused a guide or a dither, or did not settle; the message gives PHD2's reason | Read the message. Clouds hide the star more often than anything else. The runner guides again a minute later and keeps the target. If PHD2 did not stop for a slew, the runner released the target: check PHD2 in its window or with KStars, then edit the target. |
| `NoFilter` | a sequence names a filter the wheel does not have, or the train has no `FilterWheel` | Fix the filter's name in the target. The edit makes the target eligible again. |
| `SlewFailed` | the mount refused to unpark, to track, or to slew, or did not stop in 10 minutes, or the coordinates do not read | Check the mount in KStars, then edit the target, or reserve again. |
| `CenteringFailed` | no centering frame solved, the rounds ran out outside the tolerance, or the worker, the solver, the camera, or the mount failed; the message says which | Read the message and `status.centering`. A frame with no solution can need a longer `centering.exposure`, or the `Solver`'s index files do not fit the field. Edit the target to make it eligible again. |
| `Waiting` | the reservation that imaged the target ended | Reserve the telescope again. |
| `Suspended` | you suspended the target | Set `suspend` to `false`. |
| `SequencesDone` | every frame the sequences want is pending or archived | Nothing. `Complete` turns `True` when promotion archives the last frame. |
