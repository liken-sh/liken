---
title: The archive
weight: 15
toc: true
---

# The archive

The archive is the claim where a target's frames are kept, under file
names you choose. The camera first writes each frame into the
reservation's outbox, a claim close to the camera, because writing
straight to a network share can make every exposure wait. The operator
then moves each frame to the target's archive, the claim in
`spec.archive.claimName`, and records it in the target's catalog.

```yaml
spec:
  archive:
    claimName: archive
    path: astro
    format: "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}"
    promotion: Continuous
```

## How a frame moves

The operator moves frames with a promotion `Job` in the target's
namespace. The `Job` runs the operator's own image with the argument
`promote`, mounts the outbox claim at `/outbox` and the archive claim
at `/archive`, and moves every frame of its target that it finds in
the outbox. For each frame, sorted by name, it:

1. reads the frame's header, and leaves a frame that is shorter than
   its header and data say, because the driver is still writing it;
2. copies the frame to a hidden `.part` file beside its name in the
   archive, and hashes the bytes with SHA-256 as it reads them;
3. reads the copy back and hashes it again, and removes the copy when
   the two hashes differ;
4. renames the copy to the frame's name with a rename that refuses to
   replace a file;
5. appends the frame's line to the catalog;
6. deletes the frame from the outbox.

Each step syncs what it wrote before the next step starts. So when a
pod stops partway, the frame is whole in the outbox, the archive, or
both, and the next `Job` finishes the move with no second copy and no
second catalog line. A program that reads the archive, such as a
stacking tool, never finds a partial frame, because a frame appears
there only by the rename.

When `archive.claimName` is the outbox's own claim, the `Job` mounts
the claim once. It hashes the frame, records it, and renames it from
the outbox's directory into `archive.path`. No byte is copied.

`archive.promotion` decides when the operator starts a `Job`:

- `Continuous`, the default, starts one after each exposure, so the
  outbox stays small.
- `AfterReservation` starts one when the reservation that writes to the
  outbox is no longer activating, ready, or deactivating, so copying
  does not use the network while the telescope images.

Only one `Job` runs for a target at a time, because only one writer may
append to the target's catalog. Only one runs for an outbox claim at a
time, so only one copy at a time uses the network. Kubernetes stops a
`Job` after an hour, retries a failed pod twice, and deletes the `Job`
an hour after it finishes. A pod that the kubelet ends, such as for an
eviction, a preemption, or the node's shutdown, has the condition
`DisruptionTarget`. The `Job`'s `podFailurePolicy` ignores that pod, so
the `Job` runs a new pod and does not count it as one of the retries.

## Where the `Job` runs

The `Job` does not set a node. The scheduler places its pod on a node
that can mount both claims, so a `local-path` outbox puts it on the
node that has the outbox's directory. The archive must be mountable on
that node too: a `ReadWriteMany` network share, or a `local-path` claim
on the same node. Two `ReadWriteOnce` claims on two nodes cannot be
mounted in one pod, and the operator does not copy between two pods. When no node can
mount both claims, the pod stays `Pending`, and `ArchiveAvailable`
turns `False` with the reason `Unschedulable` and the scheduler's
message.

The pod runs as user and group 1000, the same user as the camera
driver, with `fsGroup` 1000 and `fsGroupChangePolicy: OnRootMismatch`.
So the first `Job` that mounts a new archive claim changes the claim's
group to 1000.

## The names of the frames

`archive.format` is each frame's file name, relative to
`archive.path` and with no extension, because the driver adds `.fits`.
The operator renders the name before each exposure, the camera writes
the frame into the outbox under that name, and the frame keeps it in
the archive.

The tokens are values of the target, the object, the sequence's
settings, the resources that took the frame, the night, the time, the
frame's number, and its `FRAMEID`. [Choose the file names](/docs/guides/find-your-frames/#choose-the-file-names)
lists each token and its value.

Each character of a value outside `A-Z`, `a-z`, `0-9`, `.`, `_`, `+`,
and `-` becomes `_`, so an object named `M 42` gives `M_42`. `XXX` and
`ISO8601` in a value become lower case, because the driver replaces
those patterns in a file name. A part of the path that starts with `.`
starts with `_` instead, so no value can climb out of `archive.path`.

Unless the format holds `{frameid}`, the operator adds `_` and the
last 12 hexadecimal digits of the frame's `FRAMEID` to the name,
because the driver replaces a file of the same name with no warning.
[Why a name ends with 12 digits](/docs/guides/find-your-frames/#why-a-name-ends-with-12-digits)
gives the odds that two frames get the same name.

## The catalog

Each target has one catalog, a JSON Lines file at
`<archive.path>/catalog/<Target>.jsonl` in the archive. The promotion
`Job` appends one line for each frame after the frame is verified in
the archive, and never changes a line. The catalog stays beside the
frames, so a backup of the archive takes it along.

```json
{"record":"frame","frameID":"0199d3a0-0000-7000-8000-4f2a9c01d3e7","target":"m42","reservation":"east-tonight","telescope":"east","camera":"east-main","train":"east-imaging","path":"M42/2026-10-12/Luminance/M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7.fits","sizeBytes":5760,"sha256":"9f86d0...","startTime":"2026-10-12T23:15:06.864Z","filter":"Luminance","exposureSeconds":300,"binning":1,"gain":100,"offset":10}
```

| Field | Value |
|---|---|
| `record` | `frame`. A later kind of record, such as a grade, is a new line with the same `frameID` and its own `record`. A reader skips a record it does not know. |
| `frameID` | the frame's `FRAMEID`, a UUIDv7 |
| `target`, `reservation`, `telescope`, `camera`, `train` | the resources that took the frame. `train` is absent when the camera names no train. |
| `path` | the frame's file, relative to `archive.path`, so the catalog stays true when you move the whole archive |
| `sizeBytes`, `sha256` | the size and the SHA-256 of the whole file |
| `startTime` | the header's `DATE-OBS`, the start of the exposure that the driver measured, in UTC |
| `filter` | the header's `FILTER`, absent when it has none |
| `exposureSeconds` | the header's `EXPTIME` |
| `binning` | the header's `XBINNING` |
| `gain`, `offset` | the header's `GAIN` and `OFFSET`, absent when it has none |

Field names are camelCase. A field whose unit could be misread has the
unit in its name, as in `sizeBytes` and `exposureSeconds`.
[Check the frames](/docs/guides/find-your-frames/#check-the-frames)
uses `jq` and `sha256sum` to check each file against its line.

## The counts

A frame counts toward a sequence by its header: the same filter,
exposure, binning, gain, and offset. A field that the sequence leaves
out matches any value, and a frame counts toward the first sequence it
matches.

- `archived` in `status.sequences` is the catalog's count. Each `Job`
  reports the counts of the whole catalog, not of its own run, so the
  next report corrects one that the operator missed.
- `pending` in `status.sequences` is the frames in an outbox and not
  yet in the archive. The operator adds one after each exposure, and
  each report subtracts the frames that reached the catalog.
- `status.outboxes` lists each outbox claim that holds frames of the
  target, with its `pending` frames and the frames that the last `Job`
  `left` there. When the operator counts a new frame while a `Job`
  moves it, the claim's `pending` can count that frame twice until the
  next `Job` corrects it. It never counts too few, so the claim's
  finalizer stays while a frame of the target is in it.
- `status.promotion.applied` is the number of the last `Job` whose
  report the operator has recorded in the status. The `Job`s of a
  target are named `<Target>-promote-<number>`.

The work left of a sequence is `count - archived - pending`.

The report is the termination message of the `Job`'s pod. When a
person, the garbage collector, or a lost node deletes the pod of a
`Job` that succeeded before the operator reads the report, the
operator deletes the `Job` and starts it again, and posts the `Normal`
`Event` `PromotionReportLost` on the target. The new `Job` finds the
frames in the catalog, moves nothing, and reports the counts again.

## The outbox claim's finalizer

The operator puts the finalizer `astrophotography.liken.sh/frames` on
each outbox claim that a target's `status.outboxes` names, and removes
it when no target names the claim. A delete of the claim then waits
while frames of a target are in it and not yet in the archive.

Kubernetes starts no new pod on a claim that is being deleted, so a
promotion `Job` cannot move the frames after the delete. To keep the
frames, let promotion move them before you delete the claim.
[An outbox claim does not delete](/docs/guides/troubleshoot/#an-outbox-claim-does-not-delete)
gives the command that removes the finalizer.

## The target's finalizer

The operator puts the same finalizer on each `Target`. Promotion finds
a target's waiting frames only through the target's `status.outboxes`,
so if the target were gone, nothing would move its frames out of the
outbox. A delete of the target therefore waits, with `Imaging` `False`
and the reason `Deleting`, until no reservation has claimed the target and
no frame of it is pending in an outbox claim that exists. Promotion
continues during the wait.
[A target does not delete](/docs/guides/troubleshoot/#a-target-does-not-delete)
gives the command that removes the finalizer.

## When a frame does not move

`ArchiveAvailable` is `True` with the reason `Promoted` after a `Job`
that moved every frame. It turns `False` with the reasons
`ClaimMissing`, `Unschedulable`, `CopyFailed`, `VerifyFailed`, and
`NameConflict`, and a frame that failed stays in the outbox until the
next `Job` of the target moves it. That `Job` starts after the next
exposure, or when you put the annotation
`astrophotography.liken.sh/retry` on the target after you fix the
cause.
[A frame does not reach the archive](/docs/guides/troubleshoot/#a-frame-does-not-reach-the-archive)
gives the cause and the fix for each reason.

The operator's metrics count `astrophotography_frames_archived_total`,
`astrophotography_promotion_bytes_total`, and
`astrophotography_promotion_failures_total{reason}`
([Metrics](/docs/reference/metrics/)).
