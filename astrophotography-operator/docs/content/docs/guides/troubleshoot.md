---
title: Troubleshoot
weight: 60
description: "Find why a Target does not start, stops, or does not reach the archive, from its conditions and Events and the reservation's Events, and fix it: a target with no reservation, Imaging False with OutboxFull, ExposureFailed, GuidingFailed, NoFilter, NoTrain, SlewFailed, or CenteringFailed, ArchiveAvailable False with ClaimMissing, Unschedulable, CopyFailed, VerifyFailed, or NameConflict, the Events AstrophotographyNoOutbox, AstrophotographyIdle, AstrophotographyWorkerPending, AstrophotographyOutboxFull, AstrophotographyNotCentered, and PromotionReportLost, a Solver that is not Ready, and an outbox claim that does not delete. Use when a target does not image, when frames stay pending, or when a condition is False."
---

# Troubleshoot

This guide starts from what the target and the reservation report, and
gives the cause and the fix for each reason. Problems with the
equipment itself, such as a reservation that is `Failed` or a device
that does not connect, are `observatory-operator`'s:
[its troubleshoot guide](https://liken.sh/observatory/docs/guides/troubleshoot/)
covers them.

Four commands show nearly everything. The examples use the target
`m42` and the reservation `east-tonight` in the namespace
`observatory`:

```sh
kubectl get targets -n observatory -o wide
kubectl describe target m42 -n observatory
kubectl describe reservation east-tonight -n observatory
kubectl logs -n liken-system deployment/astrophotography-operator
```

The target's `Event`s are the transitions of its conditions, with the
reason and the message. The reservation's `Event`s with the prefix
`Astrophotography` are what the operator did with the telescope. The
API server deletes an `Event` an hour after its last write, and the
log keeps every attempt.

## A target does not start

The target's `RESERVATION` column is empty while a reservation is
`Ready`. Check each of these:

* The reservation's `spec.holder` is exactly
  `astrophotography-operator`, and it is in the target's namespace.
* The reservation is `Ready`:
  `kubectl get reservation east-tonight -n observatory`. Until then,
  `observatory-operator` is still starting the equipment.
* The reservation has an outbox. Without one, it gets the `Warning`
  `AstrophotographyNoOutbox` and images nothing. `spec.outbox` takes
  effect at the next session, so end the reservation and create it
  again with the outbox.
* The target's `opticalTrain` is one of the session's trains:
  `kubectl get reservation east-tonight -n observatory -o jsonpath='{.status.session.trains}'`.
  A target with no `opticalTrain` needs a telescope with exactly one
  imaging camera.
* The target is not suspended, and `Complete` is not `True`.
* No other `Ready` reservation holds the target. A target's
  `RESERVATION` names the one that holds it.
* The target did not fail earlier on this reservation. A target that
  failed with `NoFilter`, `NoTrain`, `SlewFailed`, or `CenteringFailed`,
  or because PHD2 did not stop, is not taken again by the same
  reservation until you edit its `spec`.

When no target is eligible, the reservation gets the `Event`
`AstrophotographyIdle`. When another target has a higher `priority`,
it goes first. [Declare a target](/docs/guides/declare-a-target/#choose-which-target-goes-first)
explains the order.

## Imaging is False

The reason of the `Imaging` condition says why the target does not
image now, and its message gives the detail:

```sh
kubectl get target m42 -n observatory \
  -o jsonpath='{.status.conditions[?(@.type=="Imaging")]}'
```

| Reason | What it means | What to do |
|---|---|---|
| `Waiting` | The reservation that imaged the target ended. | Reserve the telescope again. The target keeps its progress. |
| `Suspended` | `spec.suspend` is `true`. | Set it to `false`. |
| `SequencesDone` | Every frame the sequences want is pending or archived. | Nothing. `Complete` turns `True` when the last frame reaches the archive. If it stays `False`, see [A frame does not reach the archive](#a-frame-does-not-reach-the-archive). |
| `OutboxFull` | The outbox has less than three frames' size free. The reservation gets the `Warning` `AstrophotographyOutboxFull`. The operator keeps the target and exposes again when space is free. | With `promotion: Continuous`, check that promotion moves frames out. With `AfterReservation`, nothing frees space before the reservation ends, so give the outbox more space. |
| `ExposureFailed` | The last exposure gave no frame: the driver answered `Alert`, did not finish within the exposure plus 2 minutes, saved the frame under another name, or the worker did not answer. The operator keeps the target and starts the next frame a minute later. | Read the message. A frame under another name means a client, such as Ekos, changed the camera's upload settings. A worker that does not answer is often still starting; see `AstrophotographyWorkerPending` below. |
| `GuidingFailed` | PHD2 did not answer, found no star, refused a guide or a dither, or did not settle. The message gives PHD2's reason. The operator keeps the target and guides again a minute later. | Clouds hide the star most often. Check PHD2 in KStars, as [Guide with PHD2](https://liken.sh/observatory/docs/guides/guide-with-phd2/) describes. When PHD2 did not stop for a slew, the operator released the target: edit the target to make it eligible again. |
| `NoFilter` | A sequence names a filter that the wheel does not have, or the train has no `FilterWheel`. The operator released the target. | Fix the filter's name in the target. The names are in the `FilterWheel`'s `spec.filters`. |
| `NoTrain` | The session has no imaging camera on the target's `opticalTrain`. The operator released the target. | Fix `opticalTrain` in the target, or reserve the telescope that has the train. |
| `SlewFailed` | The mount refused to unpark, to track, or to slew, did not stop within 10 minutes, or the session has no `Mount`. The operator released the target. | Check the mount in KStars or in its resource's status, then edit the target or reserve again. |
| `CenteringFailed` | No centering frame solved, the rounds ran out outside the tolerance, or the worker, the solver, the camera, or the mount failed. The message says which. The operator released the target. | Read the message and `status.centering`. A frame that does not solve can need a longer `centering.exposure`, or index files that fit the field ([Set up a solver](/docs/guides/set-up-a-solver/#choose-the-index-files)). A tolerance smaller than the mount can reach needs a larger `centering.tolerance` or more `centering.attempts`. |

The frames of a target that the operator released stay counted. The
next target of the namespace takes the telescope.

## Run a released target again

A target that failed with `NoFilter`, `NoTrain`, `SlewFailed`,
`CenteringFailed`, or a PHD2 that did not stop is not taken again by
the same reservation until its `spec` changes. The fix is often an
edit of the target, which makes it eligible at once. When the cause
was elsewhere, such as a mount you fixed by hand, a new reservation
takes the target again, and so does the same reservation after a
restart of the operator:

```sh
kubectl -n liken-system rollout restart deployment astrophotography-operator
```

## The reservation's Events

| `Event` | Type | What it means | What to do |
|---|---|---|---|
| `AstrophotographyNoOutbox` | `Warning` | The reservation has no outbox, so the operator images nothing. | Create the reservation again with `spec.outbox`. |
| `AstrophotographyIdle` | `Normal` | No target of the namespace is eligible. | See [A target does not start](#a-target-does-not-start). When every target is done, end the reservation. |
| `AstrophotographyUnparked` | `Normal` | The operator unparked the mount before a slew. | Nothing. |
| `AstrophotographyWorkerPending` | `Warning` | The worker's pod cannot start. The message gives the scheduler's reason. | An outbox claim with the access mode `ReadWriteOncePod` cannot work, because the camera's pod already mounts it. Use `ReadWriteOnce`. Otherwise check that the worker image `ghcr.io/liken-sh/astrophotography-operator-worker` can be pulled. |
| `AstrophotographyOutboxFull` | `Warning` | A target found the outbox full. | See `OutboxFull` above. |
| `AstrophotographyNotCentered` | `Warning` | The target names a `Solver` that is missing or not `Ready`, so the operator images it without centering. | See [A Solver is not Ready](#a-solver-is-not-ready). A `Solver` that turns `Ready` centers the next claim of the target. |

## A frame does not reach the archive

Frames stay in `pending`, or `ArchiveAvailable` is `False`. The
operator moves frames with promotion `Job`s named
`<Target>-promote-<number>` in the target's namespace:

```sh
kubectl get target m42 -n observatory \
  -o jsonpath='{.status.conditions[?(@.type=="ArchiveAvailable")]}'
kubectl get jobs -n observatory -l app.kubernetes.io/managed-by=astrophotography-operator
```

| Reason | What happened | What to do |
|---|---|---|
| `ClaimMissing` | The claim in `archive.claimName` does not exist. | Create the claim in the target's namespace. The next frame starts a `Job`. |
| `Unschedulable` | No node can mount both the outbox and the archive. | Use a `ReadWriteMany` archive, or a `local-path` archive on the outbox's node. [Where the `Job` runs](/docs/concepts/archive/#where-the-job-runs) explains why. |
| `CopyFailed` | A read, a write, or a rename failed, or the `Job` failed. | Read the `Job`'s log: `kubectl logs -n observatory job/m42-promote-<number>`. |
| `VerifyFailed` | The copy read back with another hash, or the frame differs from its catalog line. | Check the archive's storage and the network between the nodes. |
| `NameConflict` | Another file has the frame's name in the archive. | Move the other file away. The operator never replaces a file. |

A frame that failed stays in the outbox. The operator does not start a
`Job` again for that frame alone. The next `Job` of the target moves it: after the next exposure, or after the operator restarts. With
`promotion: AfterReservation`, no `Job` starts while the reservation
that writes to the outbox is activating, ready, or deactivating.

The `Normal` `Event` `PromotionReportLost` on the target means that the
pod of a `Job` that succeeded was deleted before the operator read its
report. The operator runs the `Job` again. The new `Job` finds the
frames in the catalog, moves nothing, and reports the counts again.

## A Solver is not Ready

`kubectl get solver -n observatory` shows the reason.
[Set up a solver](/docs/guides/set-up-a-solver/#when-the-solver-is-not-ready)
gives the fix for `ClaimMissing`, `NoIndexFiles`, and `PodNotReady`.

A `Solver` that is `Ready` but lists fewer series or files than the
claim holds has left out a file. The solver loads a file only when
its writer closes it, so a copy that is still running is not loaded
yet. A file that does not read is left out, and the solver's log names
it:

```sh
kubectl logs -n observatory deployment/lab-solver | grep 'left out'
```

A file that a pod on another node wrote to a network file system, such
as NFS, does not reach the solver's watch. Restart the solver's
`Deployment` to read it.

## An outbox claim does not delete

The operator puts the finalizer `astrophotography.liken.sh/frames` on
each outbox claim that holds frames of a target, so a delete of the
claim waits until those frames are in the archive. Kubernetes starts
no new pod on a claim that is being deleted, so no promotion `Job` can
move the frames after the delete starts. To keep the frames, let
promotion move them before you delete the claim: check that
`status.outboxes` of each target shows no `pending` frame on it. To
delete the claim and its frames, remove the finalizer. Check its
place in the list first, because the patch removes the first entry:

```sh
kubectl -n observatory get pvc frames -o jsonpath='{.metadata.finalizers}'
kubectl -n observatory patch pvc frames --type=json \
  -p='[{"op":"remove","path":"/metadata/finalizers/0"}]'
```

While the operator is not running, nobody removes the finalizer for
you.
