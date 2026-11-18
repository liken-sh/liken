---
title: Image a target
weight: 40
description: "Image a Target on a telescope that observatory-operator runs. Create the outbox claim and reserve the telescope with holder astrophotography-operator. Then follow the night through the Imaging condition, its reasons Slewing, Centering, Guiding, and Exposing, the counts in status.sequences, and the Events, as the operator slews, centers, guides, exposes, dithers, and promotes each frame to the archive. Then end the night. Use when a person wants the operator to take frames, or wants to know what the operator does during a night."
---

# Image a target

At the end of this guide, a telescope takes the frames of a `Target`
while you watch, and each frame moves from the reservation's outbox to
your archive. You see each step of the night in the target's status.

You need:

* A `Target`, with its archive claim
  ([Declare a target](/docs/guides/declare-a-target/)). This guide
  uses `m42` in the namespace `observatory`.
* A telescope that `observatory-operator` runs, such as the `east`
  telescope of its example observatory of INDI simulators.
* A cluster that can pull the image
  `ghcr.io/liken-sh/astrophotography-operator-worker`. For each
  reservation, the operator starts a worker pod that mounts the outbox.
  The worker reports the outbox's free space and finds the stars in a
  centering frame. The operator starts no exposure until the worker
  responds.
* For a target with `centering`, a `Solver` that is `Ready`
  ([Set up a solver](/docs/guides/set-up-a-solver/)). Without one, the
  operator images the target without centering it.

## Create the outbox claim

The camera writes each frame into the reservation's outbox, a claim on
storage near the camera, and the operator moves each frame from there
to the archive. A `local-path` claim keeps the frames on the machine at the
pier while the night runs:

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
      storage: 10Gi
EOF
```

Give it room for more than three frames of your camera, because the
operator does not start an exposure while less than three frames of
space are free. Do not use `ReadWriteOncePod`, because the camera, the
worker, and the promotion `Job` all mount the claim.

## Reserve the telescope

A `Reservation` gives the telescope to the operator for a night. These
fields matter to this operator:

* `holder` must be `astrophotography-operator`. The operator ignores a
  reservation with any other holder.
* `outbox.claimName` is the claim you created above. A reservation with
  no outbox images nothing.
* `end` is when the night ends. The operator does not end a
  reservation itself, so without an `end` the telescope stays unparked
  and tracking after the last frame, until you delete the reservation.
  Set it near the next dawn at your site.

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: observatory.liken.sh/v1alpha1
kind: Reservation
metadata:
  name: east-tonight
spec:
  telescope: east
  holder: astrophotography-operator
  end: 2026-10-14T06:30:00-04:00
  outbox:
    claimName: frames
EOF
```

`observatory-operator` starts the equipment, runs your activation
procedures, and turns the reservation `Ready`. The reservation's
`start`, its phases, and what happens to the hardware are in
[Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/).
For a telescope that a person must set up by hand first, see
[Set up a portable telescope](https://liken.sh/observatory/docs/guides/set-up-a-portable-telescope/).

```sh
kubectl get reservation east-tonight -n observatory -w
```

## What happens during the night

When the reservation is `Ready`, the operator works through these
steps. The reason of the target's `Imaging` condition is the current
step, and each change of reason posts an `Event` on the target.
[Imaging](/docs/concepts/imaging/) gives every detail.

1. **The worker starts.** The operator creates the `Deployment`
   `east-tonight-astrophotography`, whose pod mounts the outbox.
2. **The operator picks a target.** It takes the eligible target with
   the highest priority and writes the reservation's name into the
   target's `status.reservation`. When no target is eligible, the
   reservation gets the `Event` `AstrophotographyIdle`.
3. **It checks the filters.** When a sequence with work left, or the
   centering, uses a filter that the train's `FilterWheel` does not
   have, it releases the target with `NoFilter` before the mount moves,
   and returns to step 2 for the next target.
4. **It slews.** It stops PHD2, and `Imaging` turns `True` with the
   reason `Slewing`. It unparks the mount if it is parked, with the
   `Event` `AstrophotographyUnparked` on the reservation, turns
   tracking on, slews, and waits 5 seconds for the tube to settle.
5. **It centers**, when the target sets `centering`, with the reason
   `Centering`. It takes a short frame, plate-solves it, syncs the
   mount to the solved position, and slews again, until the error is
   within the tolerance. The result is in `status.centering`.
6. **It guides**, when the telescope has a `Guider`, with the reason
   `Guiding`. It starts PHD2 looping, selects a star, starts guiding,
   and waits for the star to settle. PHD2 calibrates first when it has
   no calibration.
7. **It exposes** one frame of the first sequence with work left, with
   the reason `Exposing`. The camera writes the frame into the outbox,
   at `/outbox/<Camera>/<Reservation>/<Target>/<name>.fits`, under its
   final name. The frame being exposed is in `status.exposure`. When the
   frame is saved, the sequence's `pending` count goes up by one.
8. **It dithers** after every `dither.every` frames, and waits for the
   star to settle before the next frame. A dither posts no `Event`.
9. **It moves the frames to the archive.** With the default
   `promotion: Continuous`, each new frame starts a promotion `Job`,
   `m42-promote-<number>`. The `Job` copies the frame, checks its
   SHA-256, records it in the catalog, and deletes it from the outbox.
   `pending` goes down and `archived` goes up, and the
   `ArchiveAvailable` condition turns `True` with the reason
   `Promoted`. With `AfterReservation`, the `Job` starts when the
   reservation ends.
10. **It moves on.** Steps 7 to 9 repeat until no sequence has work
   left. Then the operator releases the target with `Imaging` `False`
   and the reason `SequencesDone`, and returns to step 2 for the next
   target. `Complete` turns `True` when the last frame is in the
   archive.

## Follow the night

Watch the targets of the namespace, with the `Imaging` reason:

```sh
kubectl get targets -n observatory -o wide -w
```

```
NAME   OBJECT   PROGRESS   RESERVATION    COMPLETE   AGE   IMAGING    PRIORITY
m42    M42      0/5        east-tonight   False      5m    Exposing   0
```

The condition's message gives the reservation, the sequence, and how
the frame is guided and centered:

```sh
kubectl get target m42 -n observatory \
  -o jsonpath='{.status.conditions[?(@.type=="Imaging")].message}'
```

```
Reservation east-tonight exposes Luminance 10s, guided by Guider east, centered within 12.3 arcseconds in 2 rounds
```

The counts of each sequence, and the frame in flight:

```sh
kubectl get target m42 -n observatory -o jsonpath='{.status.sequences}'
kubectl get target m42 -n observatory -o jsonpath='{.status.exposure.file}'
```

`pending` counts the frames in the outbox, and `archived` counts the
frames in the archive's catalog.

The target's `Event`s record each change
of its conditions, and the reservation's `Event`s whose reason starts
with `Astrophotography` record what the operator did with the
telescope:

```sh
kubectl describe target m42 -n observatory
kubectl describe reservation east-tonight -n observatory
```

You can also watch the night in KStars, connected to the telescope's
INDI server as
[Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/#connect-kstars)
describes. Do not change the camera's upload
settings from KStars or Ekos during the night, because the operator
then cannot find the frame, and the exposure fails.

## End the night

The night ends at the reservation's `end`. To end it early, delete the
reservation:

```sh
kubectl delete reservation east-tonight -n observatory
```

[Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/#end-the-night)
gives what `observatory-operator` does with the equipment at the end.

When the reservation ends, the operator stops and releases
the target with `Imaging` `False` and the reason `Waiting`. The frame
being exposed at that moment does not count. If the camera's driver
saves it anyway, the next promotion finds it in the outbox and counts
it. With
`promotion: AfterReservation`, the promotion `Job` starts now.

The target keeps its progress. The next reservation in the namespace
picks it up where it stopped. To check the frames,
[find and verify them](/docs/guides/find-your-frames/).

When `Imaging` is `False` with a reason other than `Waiting`,
`Suspended`, or `SequencesDone`, or a frame does not reach the
archive, see [Troubleshoot](/docs/guides/troubleshoot/).
