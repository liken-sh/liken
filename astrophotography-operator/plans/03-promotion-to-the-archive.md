# 03, Promotion to the archive

Proposed on 2026-10-12, and designed on 2026-10-12. Not built. [The
design](#the-design) answers what the design conversation left open,
and changes two decisions on evidence: frames carry no FITS
checksums to check, and the same claim still runs a `Job`, which
renames and does not copy.

## The problem

Frames are worth keeping for years, in a place the person chooses,
under names the person chooses. But a frame written straight to a
network share can make the next exposure wait, and that wait repeats
all night. So the camera writes to the reservation's outbox, close to
the camera, and the frames move to their long-term home afterward
without risk of loss on the way.

## Decisions

- **Each `Target` names its archive, as the name of a claim.** A
  target's frames belong together across every night it is imaged, so
  the archive belongs to the target, not to a night. The outbox belongs
  to the `Reservation` ([`observatory-operator` plan
  19](../../observatory-operator/plans/completed/19-the-frames-outbox.md)). When
  the two are the same claim, no promotion runs.
- **The person owns the file names.** A target states the format, and
  the operator may add a suffix only to make a name unique. Because the
  camera writes into the outbox under the final name, promotion copies
  without renaming.
- **Promotion is a `Job` that copies, verifies, and only then
  deletes.** It copies each frame to the archive, reads the copy back
  and checks it against the checksums of the FITS checksum convention,
  and removes the frame from the outbox only after the check passes. A
  crash at any point leaves each frame in at least one verified place,
  and a second run does no harm, because each frame has its own
  identity.
- **The person sets the pacing**: promote during the night, so the
  outbox stays small, or after it, so the link stays quiet while the
  telescope works.
- **A frame never changes after promotion.** What the operator learns
  later, such as a grade, is a record keyed by the frame's identity.
- **The operator puts a finalizer on the outbox claim**, so deleting
  the claim waits until every frame in it is promoted. Uninstalling the
  operator removes that finalizer.

## Considered and set aside

- **A finalizer on the camera's pod.** It keeps the pod's object, but
  the kubelet still stops its containers, and the frames are on the
  claim, which outlives the pod anyway.
- **INDI writing straight to the network share.** It costs a wait on
  every exposure.


## What the frames carry

The header of every probe frame of [plan 01](01-run-a-targets-sequence.md)
and of `observatory-operator`'s `indi/testdata/ccd/frame-header.txt`
holds no `CHECKSUM` and no `DATASUM`: the INDI driver does not write
the FITS checksum convention. Adding them would edit the frame after
the driver wrote it, which the decisions forbid. So the check is a
hash of the bytes, which the catalog keeps.

The driver also replaces a frame of the same name with no message
([plan 01](01-run-a-targets-sequence.md#what-the-driver-does)), so a
name must be unique before the exposure, not only before the
promotion.

## The design

### The fields

```yaml
spec:
  archive:
    claimName: archive
    path: astro
    format: "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}"
    promotion: Continuous
```

- **`claimName`** is a `PersistentVolumeClaim` in the target's
  namespace, and cannot change after the target is created.
- **`path`** is a directory in the claim, empty by default. The frames
  and the catalog of [plan 01](01-run-a-targets-sequence.md#the-frame-catalog)
  are under it.
- **`format`** is the person's file name, a path relative to `path`
  with no extension. The driver adds `.fits`. The default is the
  example above.
- **`promotion`** is the pacing: `Continuous`, the default, promotes
  while the night runs, so the outbox stays small, and
  `AfterReservation` promotes when the reservation ends, so the link
  stays quiet while the telescope works.

### The tokens of a name

| Token | Value |
|---|---|
| `{target}` | the `Target`'s name |
| `{object}` | `spec.object` |
| `{filter}` | the sequence's filter |
| `{exposure}` | the exposure in seconds, such as `300s` or `0.5s` |
| `{binning}` | such as `1x1` |
| `{gain}`, `{offset}` | the sequence's values, or `default` |
| `{camera}`, `{telescope}`, `{train}`, `{reservation}` | the names of those resources |
| `{night}` | the date of the night, as `YYYY-MM-DD`: the local mean solar date at the observatory's longitude, 12 hours before the exposure, so one night has one date |
| `{datetime}` | the time the operator requested the exposure, UTC, as `20261012T231506Z` |
| `{n}` | the frame's number in its sequence, `archived + pending + 1`, four digits |
| `{frameid}` | the frame's `FRAMEID` |

The operator renders the name before each exposure, because the driver
takes it before the exposure. `{datetime}` is therefore the request's
time, and `DATE-OBS` in the header is the driver's measured start.

- **A token's value is safe in a path.** Each character outside `A-Z`,
  `a-z`, `0-9`, `.`, `_`, `+`, and `-` becomes `_`, so an object named
  `M 42` gives `M_42`. `XXX` and `ISO8601` in a value become lower
  case, because the driver replaces those patterns in a prefix.
- **The format's own text** is refused by the CRD unless it holds only
  those characters, `/`, and known tokens, and it is refused when it
  starts with `/` or `catalog/`, or holds `..`, `XXX`, or `ISO8601`.
- **The suffix.** Unless the format holds `{frameid}`, the operator
  adds `_` and the last 12 hexadecimal digits of the `FRAMEID` to the
  name, such as `M42_Luminance_300s_20261012T231506Z_4f2a9c01d3e7`.
  Those digits are random, 48 bits, so two frames of one target meet
  with a chance near 2 in a billion at 1,000 frames. The suffix is the
  only thing the operator adds, and its one purpose is a name that no
  other frame has. `{n}` is not enough: a restart can count one frame
  twice, and a second frame of the same name would replace the first.

The file's place in the outbox is
`/outbox/<Camera>/<Reservation>/<Target>/<name>.fits`, and its place in
the archive is `<path>/<name>.fits`. So promotion copies without
renaming the file, as decided.

### The promotion `Job`

The operator runs a `Job` for each target and outbox claim, in the
target's namespace, with the operator's own image and binary in its
`promote` mode:

- **When.** With `Continuous`, after each exposure of the target, if no
  promotion `Job` of the target runs. With `AfterReservation`, when the
  reservation that wrote the frames is `Released`. And with either,
  when the operator starts and a target lists an outbox in
  `status.outboxes`. A `Job` promotes every frame of its target that is
  in the outbox when it reaches it, and exits.
- **One at a time.** At most one promotion `Job` runs for a target,
  because one `Job` writes the target's catalog. At most one runs for an
  outbox claim, so the link carries one copy at a time.
- **Where.** The pod mounts the outbox at `/outbox` and the archive at
  `/archive`. The scheduler places it by the claims: a `local-path`
  outbox puts it on the pier machine. So the archive must be mountable
  on the outbox's node: a `ReadWriteMany` network share, or a
  `local-path` claim on the same node. A pod that cannot start sets
  `ArchiveAvailable` `False` with the reason `Unschedulable` and the
  scheduler's message. Two `ReadWriteOnce` claims on two nodes cannot
  meet in one pod; the manual says so, and this plan does not stream
  frames between two pods.
- **The pod** runs as user and group 1000 with `fsGroup` 1000 and
  `fsGroupChangePolicy: OnRootMismatch`, the driver's user, on a
  read-only root, with no capabilities. The `Job` has a `backoffLimit`
  of 2, an `activeDeadlineSeconds` of one hour, and a
  `ttlSecondsAfterFinished` of one hour.
- **Before the `Job`,** the operator reads the archive claim. A claim
  that does not exist sets `ArchiveAvailable` `False` with the reason
  `ClaimMissing`, and no `Job` starts.

A `Job` for each frame was set aside: a night of short exposures would
start hundreds of pods, and several of them would write one catalog. An
Indexed `Job` was set aside too: the directory of the target in the
outbox is already the list of work, in the order the frames were
taken, and one copier keeps one writer and one copy on the link.

### Each frame

For each `.fits` file in `/outbox/*/*/<Target>/`, sorted by name:

1. **Read the header**: `FRAMEID`, `FILTER`, `EXPTIME`, `XBINNING`,
   `GAIN`, `OFFSET`, `DATE-OBS`, and the sizes. A file shorter than
   its header and data, padded to 2,880 bytes, is incomplete: the `Job`
   leaves it and reports it. A frame with no `FRAMEID` is not this
   operator's, and the `Job` leaves it.
2. **Copy** to `<dir>/.<name>.part` in the archive, created with
   `O_EXCL`, hashing the bytes with SHA-256 as they are read, then
   `fsync`.
3. **Verify:** read the copy back and hash it. A different hash removes
   the copy, reports `VerifyFailed`, and leaves the frame.
4. **Place** the copy under its name with a rename that refuses to
   replace a file (`renameat2` with `RENAME_NOREPLACE`), and `fsync` the
   directory.
5. **Record** one line in the catalog, with the hash, and `fsync` it.
6. **Delete** the frame from the outbox, and `fsync` the directory.

A crash at any point leaves every frame in at least one verified
place, and the next `Job` finishes the work:

| The `Job` finds | It does |
|---|---|
| a `.part` file | deletes it, and copies again |
| the frame in the outbox and the same file in the archive, with no catalog line | hashes both; if they match, records and deletes |
| the frame in the outbox, and its `FRAMEID` in the catalog | checks the archive's file against the recorded hash, and deletes the frame |
| another file with the frame's name in the archive | leaves both, and reports `NameConflict` |

A name never replaces a file, so no frame in the archive is ever
overwritten. Readers of the archive, such as a stacking tool that
watches it, never see a partial frame, because a frame appears there
only by a rename.

### The same claim

When `archive.claimName` is the outbox's claim, the `Job` still runs,
and mounts the claim once. It does not copy: it hashes the frame,
records it, and renames it from the outbox's directory into `<path>`.
This changes the decision that no promotion runs. The camera still
writes into the outbox's directory, so the archive's tree shows only
complete frames, and the catalog keeps one writer and one form of
record whatever the claims are. A rename in one file system is atomic
and moves no bytes.

### The report

The `Job`'s container writes a report to its termination message
before it exits, and the operator reads it from the pod's status when
the `Job` ends. The report holds the catalog's totals for each
combination of filter, exposure, binning, gain, and offset, the
frames still in the outbox and why, and each failure with its frame.
The totals come from reading the whole catalog, not from this run, so
a report that the operator never reads is corrected by the next one.
The termination message holds at most 4,096 bytes, which fits about 25
sequences; a target with more reports the first 25 and says so.

The operator writes `archived` and `pending` into the target's status
from the report, and `ArchiveAvailable` from its failures.

### The outbox's claim and its space

- **The finalizer.** The operator adds
  `astrophotography.liken.sh/frames` to each outbox claim that a
  target's `status.outboxes` names, and removes it when no target names
  the claim. A delete of the claim then waits until its frames are
  promoted. Uninstalling the operator removes the finalizer.
- **Space.** Before each exposure, the runner asks the worker for the
  free bytes of the outbox (plan 02). With less than three frames
  free, it does not expose: `Imaging` turns `False` with the reason
  `OutboxFull`, the reservation gets the `Warning`
  `AstrophotographyOutboxFull`, and a promotion starts. The end of the
  promotion `Job` wakes the runner, which asks again. With
  `AfterReservation`, nothing frees space before the end, and the
  message says so. A frame's size is the camera's width × height ×
  bytes per pixel / binning², from `status.info`, plus three blocks of
  2,880 bytes for the header. The free space is what `statfs` reports for the file
  system, which for `local-path` is the node's pool of claims and not
  the claim's request.

## Still open

- Copying between two `ReadWriteOnce` claims on two nodes, which needs
  a pod at each end.
- A pacing tied to the hours of the day, such as "after dawn".
