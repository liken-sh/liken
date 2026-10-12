# 01, Run a target's sequence

Proposed on 2026-10-12, and designed on 2026-10-12. Not built. [The
design](#the-design) answers what the design conversation left open,
from probes of the pinned `indi-simulators` image on the laptop.
[Plan 08](08-the-component-and-the-first-session.md) gives the
component that holds it and the order of the build.

## The problem

A person knows what they want to image: sixty five-minute frames of M42
through each of three filters, say. Today they sit at KStars and drive
each exposure, or write an Ekos sequence for one night at a time. The
operator should take that statement of intent and carry it out on a
telescope that `observatory-operator` controls, across as many nights
as it takes.

## Decisions

- **A `Target` states the intent.** It names an object, its
  coordinates, and its sequences, each a filter, an exposure, and the
  number of frames wanted. A target lasts as long as the work does, and
  its status keeps the progress.
- **There is no session resource.** A `Reservation` is one continuous
  block of a telescope's time. A reservation serves many targets, and a
  target is imaged across many reservations. A night's work is the
  frames of one reservation, found by a query, so nothing else needs to
  group it. The name `Session` stays unused.
- **In this plan, a person creates the reservation** and names this
  operator as its holder. The scheduler of [plan
  04](04-the-scheduler.md) and the reservations of [plan
  05](05-reservations-on-demand.md) build on a sequence that already
  runs by hand.
- **The operator drives the telescope over INDI and PHD2's API, not
  through Kubernetes objects.** An exposure, a dither, and a guide pulse
  happen too often to be objects. The `Telescope`'s status names the
  INDI server, and each device's status names its INDI device.
- **The driver writes each frame once, complete, into the outbox.**
  Before each exposure the operator sets the camera's directory and
  file prefix inside the reservation's outbox
  ([`observatory-operator` plan
  19](../../observatory-operator/plans/completed/19-the-frames-outbox.md)), and
  the keywords it owns, such as the target, through the declared
  keywords of [`observatory-operator` plan
  20](../../observatory-operator/plans/completed/20-declared-fits-keywords.md).
  The operator never asks the server for a frame's bytes.
- **Each frame has a global identity, written in its header.** The
  file name is the person's format and can change; the identity cannot.
  Frames are immutable once written, so a copy of a frame anywhere is
  the same frame.
- **A frame's identity is a UUIDv7 in the keyword `FRAMEID`**,
  decided on 2026-10-12. The operator writes it through `FITS_HEADER`
  before each exposure, so the driver writes it into the frame when it
  creates the file. A UUIDv7 starts with the time it was made, so
  identities sort in the order the frames were taken. The camera and
  `DATE-OBS` were set aside, because `INSTRUME` names the INDI driver
  and two cameras of one model share it. A check of N.I.N.A.'s FITS
  and XISF headers and of SGP found no keyword for a frame's identity
  to reuse. The nearest convention, `DATAMD5` from `fitsmd5`, is a hash
  of the data, so it names the pixels and not the exposure.
- **Frames are records, not objects.** A night makes hundreds of
  frames, so each one is a record in a `Catalog`, keyed by its identity
  and its reservation, and the target's status carries only the counts.

## What the driver does

On 2026-10-12, the CCD simulator of the pinned `indi-simulators` image
answered three questions that bore on the identity
([`observatory-operator` plan
20](../../observatory-operator/plans/completed/20-declared-fits-keywords.md)
has the probe):

- `DATE-OBS` has millisecond precision, such as
  `2026-10-12T20:08:26.864`.
- `INSTRUME` is the name of the INDI device, such as `CCD Simulator`.
  Two cameras of one model run under one driver name, so they report
  the same `INSTRUME` unless a person declares their own, and the
  camera and the start time alone do not tell two such cameras apart.
- A keyword written through `FITS_HEADER` before an exposure is on
  that exposure's frame, and a second write of the same name replaces
  the value. So a keyword that changes with every frame, such as a
  UUID, costs one write before each exposure.

`INDI_CLEAR` removes every keyword that a client wrote, those of
`observatory-operator` included. After a clear, this operator writes
the session's list from `status.session.fitsKeywords` again with its
own, so a keyword of one target never reaches the frames of the next.

A second probe on 2026-10-12 ran `indiserver` from the same image in
Docker with the telescope, CCD, and focuser simulators, and set the
upload settings the way this operator will:

- **A prefix with no pattern is the file's name.** With `UPLOAD_MODE`
  set to `UPLOAD_LOCAL`, `UPLOAD_DIR` set to `/out/m42/Light/L`, and
  `UPLOAD_PREFIX` set to `M42_L_5s_first`, the driver saved
  `/out/m42/Light/L/M42_L_5s_first.fits`. It created the missing
  directories itself.
- **A second exposure with the same prefix replaces the first frame.**
  The driver wrote a 3 s frame over a 5 s frame of the same name, with
  no message. So the operator gives every exposure a name that no other
  exposure has.
- **The driver replaces two patterns in a prefix.** `XXX` became the
  next free number, `001`, and `ISO8601` became the time, so
  `a_ISO8601_b_XXX` was saved as `a_2026-10-12T23-28-48.991_b_001.fits`.
- **A long name is kept whole.** A directory and prefix of 127
  characters was saved in full.
- **The driver reports the file.** A client receives the message
  `[INFO] Image saved to /out/pole/pole.fits`, and `CCD_EXPOSURE` goes
  from `Busy` to `Ok`. The probe did not record which of the two came
  first; the build checks that the file is closed when the property is
  `Ok`, because promotion reads the file after it.
- **`FRAMEID` fits the header.** A 36-character UUID written through
  `FITS_HEADER` reached the header as
  `FRAMEID = '0199d3a0-0000-7000-8000-000000000001' / frame identity`.

## The design

### The `Target` resource

A `Target` is in the API group `astrophotography.liken.sh`, version
`v1alpha1`, namespaced, in the category `astro` with the kinds of
`observatory-operator`, so `kubectl get astro` lists both. It is
imaged only by reservations in its own namespace, because the outbox
and the archive are claims, and a pod can mount only claims of its own
namespace.

```yaml
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Target
metadata:
  name: m42
  namespace: observatory
spec:
  object: M42
  coordinates: {ra: "05:35:17.3", dec: "-05:23:28"}
  priority: 0
  suspend: false
  opticalTrain: east-imaging
  sequences:
    - {filter: Luminance, exposure: 300s, count: 60}
    - {filter: Red, exposure: 300s, count: 20, binning: 2, gain: 100, offset: 10}
  dither: {every: 2, pixels: 5}
  centering: {solver: lab, tolerance: 30}
  archive:
    claimName: archive
    path: astro
    format: "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}"
    promotion: Continuous
  fitsKeywords:
    - {name: PROGRAM, value: Orion widefield}
```

- **`object`** is the name of the object, and the value of `OBJECT` on
  each frame. The rule of `fitsKeywords` that refuses a `/` applies to
  it too.
- **`coordinates`** are J2000 (ICRS), as every catalog gives them:
  `ra` in hours as `HH:MM:SS.s`, and `dec` in degrees as
  `±DD:MM:SS.s`. Each frame's header reports J2000 too, in `OBJCTRA`
  and `OBJCTDEC`. One form, and the form of SIMBAD and KStars, keeps a
  person from pasting decimal hours into a field that reads degrees.
  The CRD checks each with a pattern and a range.
- **`priority`** and **`suspend`** decide which targets a reservation
  images, as the next section states. `suspend` works like a `Job`'s:
  a suspended target keeps its progress and is not imaged.
- **`opticalTrain`** is optional. A target that names a train is imaged
  only on that train's telescope. A target with no train is imaged on
  any telescope whose session has exactly one imaging camera.
- **`sequences`** is a list. Each sequence has a `filter`, an
  `exposure` as a duration such as `300s` or `500ms`, a `count`, and
  optional `binning`, `gain`, and `offset`. A field that is absent
  leaves the camera's own value, so a camera with no gain property
  works. `filter` is a name in the filter wheel's `FILTER_NAME`, the
  name that the driver writes into `FILTER`. A sequence with no filter
  takes the light path as it is.
- **`dither`** is optional: after every `every` frames, PHD2 moves the
  lock position by up to `pixels` guide camera pixels, with `raOnly`
  for a mount that guides in RA alone. With no `dither`, the operator
  does not dither.
- **`centering`** is optional, and [plan 02](02-solving-and-centering.md)
  designs it. With no `centering`, the operator slews and does not
  solve.
- **`archive`** is the claim and the file names, which [plan
  03](03-promotion-to-the-archive.md) designs. `archive.claimName`
  cannot change after the target is created, because the counts come
  from the archive's catalog. A person who wants a new archive creates
  a new target.
- **`fitsKeywords`** is the target's level of the declared keywords,
  in the shape of `observatory-operator`'s field. The rules refuse the
  keywords that the driver measures, from the same list
  (`observatory/fitskeywords.go`), and also `OBJECT` and `FRAMEID`,
  which this operator writes. A test holds the copy of the rules
  equal to `observatory-operator`'s, as that operator's test holds the
  `Reservation`'s copy.

The printer columns are the object, the progress as archived of
wanted frames, such as `37/180`, the reservation that images it now,
and `Complete`.

### Which targets a reservation images

A `Reservation` whose `spec.holder` is `astrophotography-operator`
images the targets of its namespace. A target is eligible when it is
not suspended, its `Complete` condition is not `True`, its
`opticalTrain` is one of the session's trains or empty, and no other
`Ready` reservation images it now. The runner takes the eligible
target with the highest `priority`, then the oldest, then the first
name, and stays on it until its sequences have no work left or it
fails. Then it takes the next one.

The target finds its reservations, and not the reverse, for three
reasons. A list of targets on the `Reservation` would put imaging into
`observatory-operator`'s resource, which knows nothing of targets. A
list of reservations on the `Target` would need an edit every night,
because a reservation lasts one night. And every target of a namespace
being eligible is how pods meet nodes: [plan 04](04-the-scheduler.md)
replaces only the order, with a choice for each exposure, and keeps
the rule of eligibility. Until then, a person suspends a target that
is below the horizon.

A runner claims a target by writing its own reservation into the
target's `status.reservation`, with the `resourceVersion` it read. Two
telescopes that choose one target at the same moment get one success
and one `409`, and the second runner chooses again. A
`status.reservation` that names a reservation that is not `Ready`
claims nothing.

Within a target, the runner exposes the first sequence in the list
that has work left, until it has none, and then the next. A filter
change costs a refocus on a real rig, so one filter at a time is the
cheapest order, and plan 04 can change it.

### What the runner reads from `observatory-operator`

The runner starts when a holder's reservation is `Ready` with an
outbox. It reads only what the reservation and the devices publish:

- `status.endpoint` of the `Reservation` is the telescope's INDI
  server.
- `status.session.devices` names each device of the session, and
  `Guides` marks the guide camera. The imaging camera is the `Camera`
  on the target's train that does not guide. The filter wheel and the
  focuser are the devices of the same train, and the mount is the
  session's `Mount`.
- Each device's `status.indiDevice` is its name on that server, such
  as `CCD Simulator`, and a camera's `status.info` gives its pixel size
  and size in pixels.
- `status.session.guiders` names the `Guider`, and its
  `status.endpoint` is PHD2's event server on port 4400.
- `status.session.fitsKeywords` holds the merged keywords of each
  imaging camera.
- `status.session.trains` and `status.session.tubes` give the focal
  length, for the plate scale of [plan 02](02-solving-and-centering.md).

A reservation with no outbox gets one `Event`,
`AstrophotographyNoOutbox`, and the runner images nothing, because a
frame must land in a claim that outlives the pod.

### The order of operations for one target

1. **Claim** the target, and set its `Imaging` condition `True` with
   the reason `Slewing`.
2. **Stop guiding**, if PHD2 guides, with `stop_capture`.
3. **Unpark and track.** If `TELESCOPE_PARK` is parked, unpark it, and
   set `TELESCOPE_TRACK_STATE` on. The procedures of
   `observatory-operator` park the mount at the end of the
   reservation.
4. **Slew.** Convert the J2000 coordinates to the equinox of the date,
   because an INDI mount takes `EQUATORIAL_EOD_COORD` in JNow, set
   `ON_COORD_SET` to `TRACK`, write the coordinates, and wait until the
   property is `Ok`.
5. **Center**, if `centering` is set: the loop of [plan
   02](02-solving-and-centering.md), with the reason `Centering`.
6. **Guide**, if the session has a `Guider`: `loop`, `find_star`, and
   `guide` with its settle, then wait for `SettleDone`, with the reason
   `Guiding`. This is the order that `observatory-operator`'s [plan
   09](../../observatory-operator/plans/completed/09-the-guider.md)
   drilled. A session with no `Guider` exposes unguided.
7. **Expose** each frame, with the reason `Exposing`:
   1. Ask the worker of [plan 02](02-solving-and-centering.md) for the
      free space of the outbox. With less than three frames' size
      free, do not expose: see [plan 03](03-promotion-to-the-archive.md).
   2. Set `UPLOAD_MODE` to `UPLOAD_LOCAL`, and `UPLOAD_SETTINGS` to the
      frame's directory and name in the outbox (below).
   3. Set `CCD_FRAME_TYPE` to light, and `CCD_BINNING`, `CCD_GAIN`,
      and `CCD_OFFSET` where the sequence states them.
   4. Set `FILTER_SLOT` to the slot whose `FILTER_NAME` is the
      sequence's filter, and wait until it is `Ok`.
   5. Write the keywords: `INDI_CLEAR`, then the merged list (below),
      one `FITS_HEADER` write each, each settled.
   6. Record the frame in `status.exposure`: its `FRAMEID`, the
      sequence, the file, the reservation, and the start time.
   7. Set `CCD_EXPOSURE`, and wait until it is `Ok`. Check that the
      driver's message names the file the operator set.
   8. Clear `status.exposure`, count the frame as pending, and wake
      the promotion of [plan 03](03-promotion-to-the-archive.md).
   9. Dither, if `dither.every` frames passed since the last dither,
      and wait for `SettleDone`.
8. **Move on** when the target has no work left: clear
   `status.reservation`, set `Imaging` `False` with the reason
   `SequencesDone`, and choose the next target.

Each step settles each write with the INDI client's `Settle`, which
reads the answer from the updates after the write. A step that runs
again after a restart reads the device first, so a write goes out only
when it is still needed, the rule of `observatory-operator`'s runner.

The operator writes every setting and every keyword before every
exposure, not once for each target. `observatory-operator` writes the
upload settings and its keywords again when a camera's driver comes
back in a new pod, and a person in KStars can change a setting during
the night. A full write before each frame states the frame's header in
full, so no earlier write can leak into it. Plan 20's drill measured 2
to 43 ms for each settled keyword, so ten keywords cost well under a
second against an exposure of minutes.

A `StarLost` during an exposure does not stop the exposure; [plan
06](06-grading-policies.md) decides whether the frame counts. The
runner runs `find_star` and `guide` again before the next frame.

### The keywords of a frame

After `INDI_CLEAR`, the operator writes one merged list, in this order
of precedence, the most specific last:

1. The session's keywords for the imaging camera, from
   `status.session.fitsKeywords` of the `Reservation`.
2. The target's `fitsKeywords`.
3. `OBJECT`, from `spec.object`.
4. `FRAMEID`, a new UUIDv7 for this exposure.

The merge happens before the writes, so the header holds one card for
each name, in the order the levels wrote them. A centering exposure
gets a new `FRAMEID` too, so a centering frame never carries the
identity of a light frame.

### The frame's file in the outbox

`observatory-operator` points each camera at `/outbox/<Camera>`. This
operator writes inside that directory, one directory for each
reservation and target:

```
/outbox/<Camera>/<Reservation>/<Target>/<the name from the format>.fits
/outbox/<Camera>/<Reservation>/_centering/<FRAMEID>.fits
```

The name comes from the target's `archive.format`, which [plan
03](03-promotion-to-the-archive.md) designs, so the file keeps its name
when it moves to the archive. The directories tell the promotion `Job`
which target and reservation a frame belongs to without a list. A
Kubernetes name cannot hold `_`, so `_centering` never meets a target's
directory.

### The frame catalog

The `Catalog` of the decisions is a file, not a resource: one JSON
Lines file for each target, in the target's archive, at
`<archive.path>/catalog/<Target>.jsonl`. Each line is one frame,
appended when the frame reaches the archive and is verified. A line
holds the frame's `FRAMEID`, target, reservation, telescope, camera,
and train, its path in the archive, its size and SHA-256, and from
its header `DATE-OBS`, `FILTER`, `EXPTIME`, `XBINNING`, `GAIN`, and
`OFFSET`. [Plan 03](03-promotion-to-the-archive.md) designs the writer.

The catalog lives in the archive for four reasons. It outlives the
cluster and the operator, beside the frames it describes, and a backup
of the archive takes it along. It is one writer's append-only file, so
it needs no database and works on a network share. `jq` and `grep`
answer the query of the decisions: the frames of one reservation are
`jq 'select(.reservation == "east-tonight")'`. And a frame's record
never edits the frame. Later records, such as a grade from [plan
06](06-grading-policies.md), are new lines keyed by `FRAMEID`.

These homes were set aside:

- **A resource for each frame.** The decisions rejected frames as
  objects: a night makes hundreds, and etcd keeps every write.
- **A `ConfigMap`.** Its limit of 1 MiB fills in a few thousand frames,
  and it puts the archive's index in etcd, apart from the archive.
- **`library-operator`'s `Catalog`.** It is a SQLite database that
  Corrosion replicates, with a member pod in every pod that writes. It
  would make this operator depend on the media library, and add pods
  to a pier machine with little memory.
- **A SQLite file in the archive.** SQLite's locks are unreliable on a
  network share, where many archives live.

### The target's status

```yaml
status:
  observedGeneration: 3
  reservation: east-tonight
  sequences:
    - {filter: Luminance, exposure: 300s, binning: 1, count: 60, archived: 37, pending: 2}
  outboxes:
    - {claimName: frames, pending: 2}
  exposure:
    frameID: 0199d3a0-...
    sequence: 0
    reservation: east-tonight
    file: east-main/east-tonight/m42/M42/2026-10-12/Luminance/M42_Luminance_300s_...fits
    startTime: "2026-10-12T23:21:06Z"
  centering: {...}
  conditions: [...]
```

- **`archived`** is the count of the archive's catalog. It comes only
  from the report of a promotion `Job` (plan 03), and each report
  carries the catalog's totals, not a change, so a lost report is
  corrected by the next one.
- **`pending`** counts the frames this target has in an outbox and not
  yet in the archive. `outboxes` lists the claims that hold them, so a
  frame that waits in an outbox is never forgotten, even after the
  reservation that wrote it ends.
- **Work left** is `count − archived − pending`. A target can end with
  more frames than it asked for when a promotion lags, and never with
  fewer.
- **A frame counts toward a sequence by its header**, not by its place
  in the list: the same filter, exposure, binning, gain, and offset.
  So an edit of the sequences keeps the frames that still match. A
  frame that matches no sequence stays in the catalog and counts
  nowhere.
- **`exposure`** is the frame in flight, written before the exposure
  starts and cleared after it ends. The next section uses it.

The conditions:

| Condition | True | False reasons |
|---|---|---|
| `Complete` | every sequence has `archived` at least `count` | `WorkLeft` |
| `Imaging` | a reservation images it; the reason is `Slewing`, `Centering`, `Guiding`, or `Exposing`, and the message names the reservation and the sequence | `Waiting`, `Suspended`, `SequencesDone`, `NoTrain`, `NoFilter`, `SlewFailed`, `CenteringFailed` (plan 02), `GuidingFailed`, `OutboxFull` (plan 03) |
| `ArchiveAvailable` | the last promotion succeeded | `ClaimMissing`, `Unschedulable`, `CopyFailed`, `VerifyFailed`, `NameConflict` (plan 03) |

Each transition posts one `Event`, under the rule of root plan 78.
An exposure and a dither change no condition, so neither posts an
`Event`: a target posts a few in a night, not one for each frame. The
runner's own actions on the `Reservation` post `Event`s on it with the
prefix `Astrophotography`: `AstrophotographyNoOutbox`,
`AstrophotographyUnparked`, and `AstrophotographyIdle` when no target is
eligible. The operator writes no status of `observatory-operator`'s
resources.

### Restarts

The record of a night is in the targets' statuses and in the files,
and the operator holds nothing else that it cannot read again.

- **The operator restarts during an exposure.** The driver keeps
  exposing and writes the frame, because every setting went out
  before the exposure began. The new runner reads `CCD_EXPOSURE`
  before it writes anything. While it is `Busy`, the runner waits for
  it to end, and then counts the frame that `status.exposure` names.
  When it is not `Busy`, the runner asks the worker whether the file
  that `status.exposure` names exists, and counts it if it does. Then
  it clears `status.exposure`.
- **The counts after a restart.** The runner reads `pending` again
  from the worker's list of the target's directory in the outbox, and
  `archived` from the next promotion report.
- **Centering and guiding after a restart.** The runner centers again,
  because the mount can have moved, and guides again unless PHD2
  reports `Guiding`.
- **The reservation ends during an exposure.** `observatory-operator`'s
  `Abort` step stops the devices. The runner stops, clears
  `status.reservation` and `status.exposure`, and does not count the
  frame. If the driver wrote it anyway, the promotion finds it in the
  outbox and the catalog counts it.

## Still open

- Whether `observatory-operator` refuses `FRAMEID` in its own
  `spec.fitsKeywords`, so a frame of a desktop session never carries a
  declared identity. [Plan 08](08-the-component-and-the-first-session.md)
  lists it as a small change to that operator.
- Which form of `IMAGETYP` a person chooses. The driver writes the
  SBFITSEXT form, `Light Frame`, and [plan 07](07-calibration-frames.md)
  decides the forms when it adds darks and flats.
- A meridian flip, a dome that follows the mount, and autofocus. None
  is in the first session, and each adds a step to the order above.
