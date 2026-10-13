---
title: Image a target
weight: 20
description: "Image a Target on a telescope that observatory-operator runs: declare the target and its claims, reserve the telescope for astrophotography-operator, follow the Imaging condition and the counts, and end the night. Use when a person wants the operator to take frames, when a target does not start, or when Imaging is False with a reason such as OutboxFull, ExposureFailed, NoFilter, or SlewFailed."
---

# Image a target

At the end of this guide, a telescope takes the frames of a `Target`
while you watch, and each frame waits in the reservation's outbox
until promotion moves it to your archive. You need:

* `astrophotography-operator` installed beside `observatory-operator`
  ([Install the operator](/docs/guides/install/)), and a telescope
  that `observatory-operator` runs, such as its example observatory of
  INDI simulators.
* The reservation's worker, the pod that answers for the outbox's
  free space. The operator does not start the worker yet, so in this
  version every exposure fails with `ExposureFailed` and the message
  names the worker. The worker arrives with plate solving.

The operator slews and exposes. It does not center with a plate solve
and does not guide yet, so the frames are unguided.
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
  archive:
    claimName: archive
    path: astro
EOF
```

Each `filter` is a name in the filter wheel's `FILTER_NAME`. A target
with no `opticalTrain` is imaged on a telescope with exactly one
imaging camera.

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
the night as `Event`s: `Slewing`, `Exposing`, and `SequencesDone` when
the sequences have no work left. The reservation's own `Event`s say
when the operator unparked the mount, found the outbox full, or had
no target to image.

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
| `NoFilter` | a sequence names a filter the wheel does not have, or the train has no `FilterWheel` | Fix the filter's name in the target. The edit makes the target eligible again. |
| `SlewFailed` | the mount refused to unpark, to track, or to slew, or did not stop in 10 minutes, or the coordinates do not read | Check the mount in KStars, then edit the target, or reserve again. |
| `Waiting` | the reservation that imaged the target ended | Reserve the telescope again. |
| `Suspended` | you suspended the target | Set `suspend` to `false`. |
| `SequencesDone` | every frame the sequences want is pending or archived | Nothing. `Complete` turns `True` when promotion archives the last frame. |
