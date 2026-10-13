---
title: The archive
weight: 15
toc: true
---

# The archive

Your frames are worth keeping for years, in a place you choose, under
names you choose. The camera writes each frame into the reservation's
outbox, a claim close to the camera, because a frame written straight
to a network share can make every exposure wait. The operator then
moves each frame to the target's archive, the claim in
`spec.archive.claimName`, and records it in the target's catalog. A
frame is never lost on the way and never overwritten in the archive.

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
pod stops at any step, the frame is in at least one verified place,
and the next `Job` finishes the work with no second copy and no second
catalog line. A program that reads the archive, such as a stacking
tool, never finds a partial frame, because a frame appears there only
by the rename.

When `archive.claimName` is the outbox's own claim, the `Job` mounts
the claim once. It hashes the frame, records it, and renames it from
the outbox's directory into `archive.path`. No byte is copied.

`archive.promotion` decides when the operator starts a `Job`:

- `Continuous`, the default, starts one after each exposure, so the
  outbox stays small.
- `AfterReservation` starts one when the reservation that writes to the
  outbox is no longer activating, ready, or deactivating, so the link
  stays quiet while the telescope works.

One `Job` runs for a target at a time, because one `Job` writes the
target's catalog. One runs for an outbox claim at a time, so the link
carries one copy at a time. A `Job` stops after an hour, its pod tries
three times, and Kubernetes deletes the `Job` an hour after it ends.

## Where the `Job` runs

The `Job` names no node. The scheduler places its pod where both
claims can be mounted, so a `local-path` outbox puts it on the machine
at the pier. The archive must be mountable on that machine too: a
`ReadWriteMany` network share, or a `local-path` claim on the same
node. Two `ReadWriteOnce` claims on two nodes cannot meet in one pod,
and the operator does not copy between two pods. When no node can
mount both claims, the pod stays `Pending`, and `ArchiveAvailable`
turns `False` with the reason `Unschedulable` and the scheduler's
message.

The pod runs as user and group 1000, the driver's user, with
`fsGroup` 1000. A new archive claim is changed to that group once, the
first time a `Job` mounts it.

## The names of the frames

`archive.format` is each frame's file name, relative to
`archive.path` and with no extension, because the driver adds `.fits`.
The operator renders the name before each exposure, the camera writes
the frame into the outbox under that name, and the frame keeps it in
the archive.

The tokens name the target, the object, the sequence's settings, the
resources that took the frame, the night, the time, and the frame's
number and `FRAMEID`. [Choose the file names](/docs/guides/find-your-frames/#choose-the-file-names)
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
gives the odds of two names meeting.

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

Every name is camelCase English for what the value means, and a
quantity whose unit you could guess wrong carries the unit in its name.
[Check the frames](/docs/guides/find-your-frames/#check-the-frames)
uses `jq` and `sha256sum` to check each file against its line.

## The counts

A frame counts toward a sequence by its header: the same filter,
exposure, binning, gain, and offset. A field that the sequence leaves
out matches any value, and a frame counts toward the first sequence it
matches.

- `archived` in `status.sequences` is the catalog's count. Each `Job`
  reports the counts of the whole catalog, not of its own run, so a
  report that the operator missed is made up by the next one.
- `pending` in `status.sequences` is the frames in an outbox and not
  yet in the archive. The runner adds one after each exposure, and
  each report takes off the frames that reached the catalog.
- `status.outboxes` lists each outbox claim that holds frames of the
  target, with its `pending` frames and the frames that the last `Job`
  `left` there.
- `status.promotion.applied` is the number of the last `Job` whose
  report the status holds. The `Job`s of a target are named
  `<Target>-promote-<number>`.

The work left of a sequence is `count - archived - pending`. When the
runner counts a frame while a `Job` moves it, `pending` counts the
frame once too often, and the next `Job` corrects it. A target can end
with more frames than it asked for, and never with fewer.

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

## When a frame does not move

`ArchiveAvailable` is `True` with the reason `Promoted` after a `Job`
that moved every frame. It turns `False` with the reasons
`ClaimMissing`, `Unschedulable`, `CopyFailed`, `VerifyFailed`, and
`NameConflict`, and a frame that failed stays in the outbox until the
next `Job` of the target moves it.
[A frame does not reach the archive](/docs/guides/troubleshoot/#a-frame-does-not-reach-the-archive)
gives the cause and the fix for each reason.

The operator's metrics count `astrophotography_frames_archived_total`,
`astrophotography_promotion_bytes_total`, and
`astrophotography_promotion_failures_total{reason}`
([Metrics](/docs/reference/metrics/)).
