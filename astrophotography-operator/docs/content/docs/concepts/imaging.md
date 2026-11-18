---
title: Imaging
weight: 12
toc: true
---

# Imaging

When you reserve a telescope for `astrophotography-operator`, the
operator drives it the way you would from KStars. It chooses a target,
slews to it, centers it with a plate solve when the target sets
`centering`, guides with PHD2 when the telescope has a `Guider`,
dithers, and takes each frame that the target's sequences ask for. It
connects to the telescope's INDI server as one more client, so you can
watch the night in KStars at the same time. The driver writes each
frame under its final name in the reservation's outbox, and promotion
later moves it to your archive under the same name.

## The runner of a reservation

The operator starts one runner for each `Reservation` whose
`spec.holder` is `astrophotography-operator` and whose phase is
`Ready`. The runner reads only what `observatory-operator` publishes:

* the reservation's `status.endpoint`, the address of the telescope's
  INDI server,
* `status.session`: the devices of the session, its outbox, and the
  FITS keywords of each imaging camera, as they were when the session
  started,
* each device's `status.indiDevice`, its name on the INDI server, such
  as `CCD Simulator`,
* the session's `Guider`, if the telescope has one, and the `Guider`'s
  `status.endpoint`, the address of PHD2's event server.

A target's imaging camera is the `Camera` on the target's
`opticalTrain` that does not guide. The filter wheel is the
`FilterWheel` on the same train, and the mount is the session's
`Mount`. When a device's driver is not running yet, the device has no
`status.indiDevice`, and the runner waits for it.

A reservation with no `spec.outbox` gets no images, because the frames
must go to a claim that outlives the camera's pod. The reservation
gets the `Warning` `AstrophotographyNoOutbox` instead.

## Which target, and the claim

The runner chooses the eligible target of its namespace with the
highest `priority`. When priorities are equal, it chooses the oldest
target, and then the first by name. A target is eligible when all of
these are true:

* it is not suspended and not being deleted,
* its `Complete` condition is not `True`,
* a sequence has frames left to take,
* its `opticalTrain` is on the session,
* no other `Ready` reservation images it,
* it is above the telescope's horizon ([The horizon](#the-horizon)).

A target with no `opticalTrain` is eligible on a session that has
exactly one imaging camera.

The runner claims the target by writing the reservation's name to the
target's `status.reservation`, with the `resourceVersion` it read. When
two telescopes choose the same target at the same moment, the API
server accepts one write and rejects the other, and the second runner
chooses again. When no target is eligible, the reservation gets the
`Event` `AstrophotographyIdle`, and the runner waits for a change.

## The order of a target's work

1. Claim the target.
2. If the train has a `FilterWheel`, check that the wheel's
   `FILTER_NAME` has the filter of each sequence with frames left, and
   the centering filter when the target sets `centering`. When one is
   missing, `Imaging` turns `False` with `NoFilter` before the mount
   moves, and the runner releases the target and chooses the next one.
   A finished sequence needs no filter, so you can take its filter out
   of the wheel.
3. Unless PHD2 is stopped, send `stop_capture` and wait until PHD2
   reports that it stopped, so that PHD2 does not send guide
   corrections against the slew.
4. `Imaging` turns `True` with the reason `Slewing`. If the mount is
   parked, unpark it and post the `Event` `AstrophotographyUnparked`
   on the reservation. Turn tracking on if it is off.
5. Convert the target's J2000 coordinates to the equinox of the date,
   which INDI mounts expect in `EQUATORIAL_EOD_COORD`. Slew, wait for
   the mount to report the end of the slew, and then wait 5 seconds
   more for the tube to settle.
6. If the target sets `centering`, center it, with the reason
   `Centering` ([Centering](#centering)).
7. If the session has a `Guider`, start guiding, with the reason
   `Guiding` ([Guiding and dithering](#guiding-and-dithering)).
8. Take the frames of the first sequence that has frames left, then
   the next sequence, with the reason `Exposing`. After every `dither.every` frames, dither.
9. When every sequence is done, release the claim. `Imaging` turns
   `False` with the reason `SequencesDone`, and the runner chooses the
   next target.

The runner reads the target again before each frame, so an edit takes
effect at the next frame. When you change the coordinates, the runner
stops PHD2, slews again, and centers again if the target sets
`centering`. The runner releases a target when you suspend it, with the
reason `Suspended`, when you delete it, with the reason `Deleting`, and
when it sets below the horizon, with the reason `BelowHorizon`. A
deleted target's finalizer keeps it until promotion has moved its
pending frames
([Delete a target](/docs/guides/declare-a-target/#delete-a-target)).

## The horizon

A frame of a target behind trees or a roof has no stars in it, so the
runner images a target only while it is above the horizon of the
reservation's telescope. That horizon is the `Horizon` that the
`Telescope`'s `spec.horizon` names
([Save the horizon of each place](https://liken.sh/observatory/docs/guides/describe-your-equipment/#save-the-horizon-of-each-place)).
When the telescope names no `Horizon`, or names one that does not
exist, the horizon is 0° at every azimuth.

The runner computes the target's altitude and azimuth from its
coordinates, the time, and the site of the session's `Observatory`,
with the formulas of Meeus's *Astronomical Algorithms*. The altitude
is geometric, with no correction for refraction. Refraction raises a
star at the horizon by about half a degree, so the runner treats a
target as set a little before it disappears from view. Between two
points of the `Horizon`'s profile, the horizon's altitude is
interpolated linearly in azimuth, and the last point connects to the
first across 360°, the same way N.I.N.A. reads the file.

The runner checks the horizon at two times:

* **Before it claims a target.** The runner does not claim a target
  below the horizon, and the mount does not move. The target's
  `Imaging` turns `False` with the reason `BelowHorizon`. The message
  gives the target's altitude and azimuth, the horizon's altitude at
  that azimuth, and when the target rises, such as `rises above it at
  2026-10-14T03:12:00Z`. The runner leaves the condition alone on a
  target that another `Ready` reservation images.
* **Before each frame.** When a target sets during its sequences, the
  runner releases it with `BelowHorizon` before the next frame. The
  frames already taken still count, and the runner chooses the next
  target right away.

When no target is eligible, the runner waits for a change in the
cluster or until the first target below the horizon rises, whichever
comes first. It finds the rise time to the minute, within the next day,
and sets a timer for it. A target that does not rise within a day gets
no timer, and its message says `It does not rise above it within a
day.`

## Centering

A slew leaves the telescope near the target, not exactly on it. When a
target sets `spec.centering`, the runner centers it after each slew,
both when it claims the target and when you edit the coordinates
during the night. Each round of centering goes like this:

1. The runner takes a centering frame with the binning, gain, and
   offset of the first sequence. The filter is `centering.filter`, or
   the first sequence's filter when the target sets none. The exposure
   is `centering.exposure`, 5 seconds by default. The frame is written
   to `/outbox/<Camera>/<Reservation>/_centering/<FRAMEID>.fits`. It
   does not count toward a sequence, and promotion never moves it.
2. The reservation's worker finds the frame's stars, and the `Solver`
   that `centering.solver` names solves them
   ([Solvers](/docs/concepts/solvers/)). The runner gives the solver
   the target's coordinates and the expected plate scale, ±10%. The
   plate scale in arcseconds per pixel is 206.265 × the pixel size in
   µm, from the camera's `CCD_INFO`, × the binning, ÷ the focal length
   in mm of the train's `OpticalTube`. When the frame does not solve,
   the runner takes one more frame at twice the exposure. The worker
   deletes each centering frame after its solve.
3. The error is the angle between the solved center and
   `spec.coordinates`. When the error is at most `centering.tolerance`,
   30 arcseconds by default, the target is centered, and the runner
   starts the frames.
4. Otherwise, the runner syncs the mount to the solved center: it sets
   `ON_COORD_SET` to `SYNC`, writes the solved center to
   `EQUATORIAL_EOD_COORD`, and sets `ON_COORD_SET` back to `TRACK`.
   Then it slews to the target again, waits 5 seconds, and starts the
   next round.

The sync corrects the position that the mount reports, so later slews
of the night land closer too.

`status.centering` records the last centering: the reservation, the
time, the number of rounds, the solved center, the frame's rotation and
plate scale, the final error in arcseconds, and in `roundErrors` the
error of each round that solved. The message of the `Imaging`
condition gives the result, such as `centered within 12.3 arcseconds
in 2 rounds`.

When the `Solver` is missing or not `Ready`, the runner images the
target without centering it, because frames a few arcminutes off the
target are still usable. The
`Imaging` message says `not centered because Solver <name> is not
Ready`, and the reservation gets the `Warning`
`AstrophotographyNotCentered`. Once the `Solver` is `Ready`, the next
claim of the target is centered.

## Guiding and dithering

`observatory-operator` runs PHD2 for the telescope's `Guider`,
connects it to the guide camera and the mount, and leaves it idle.
The runner does the calibration and the guiding. It keeps a connection
to PHD2's event server, at the `Guider`'s `status.endpoint`, open for
the whole reservation, and reconnects after a delay that starts at 1
second and doubles up to 1 minute. When a telescope has more than one
`Guider`, the runner uses the first by name.

Before each frame, the runner checks that PHD2 is guiding on a settled
star. When it is not, such as after a slew, the runner sends `loop`,
waits for the first guide frame, and sends `find_star` and `guide`.
When PHD2 has no calibration, it calibrates first, which takes a
minute or two. The runner then waits for PHD2's `SettleDone`, because
a frame taken while the star still moves is blurred. The runner asks
PHD2 to settle with the star within 1.5 guide camera pixels of the
lock position for 8 seconds, and PHD2 fails the settle after 1
minute. A `Target` cannot change these values.

When a `Target` sets `dither`, the runner moves the lock position after
every `dither.every` frames, by up to `dither.pixels` guide camera
pixels, in right ascension only when `raOnly` is set. A hot pixel or
other fixed pattern of the sensor then lands on a different part of
the sky in each frame, and stacking removes it. The runner waits for
`SettleDone` before the next frame. It does not dither after the
target's last frame, or before a slew to new coordinates.

A session with no `Guider` exposes unguided and does not dither. The
`Imaging` message says how the frames are taken, such as
`Reservation east-tonight exposes Luminance 300s, unguided because the
telescope has no Guider, not centered`. With a `Guider`, the middle
part reads `guided by Guider east, dithered every 2 frames`.

## One frame

Before each frame, the runner asks the reservation's worker for the
free space of the outbox. The worker is a pod that the operator runs
next to the outbox while the reservation is `Activating` or `Ready`
([Solvers](/docs/concepts/solvers/)). When less than three frames'
worth of space is free, the runner does not expose
([Problems during a night](#problems-during-a-night)). A runner that
starts before its worker is ready fails its first exposure with
`ExposureFailed` and tries again a minute later.

Then the runner writes every setting the frame depends on, in this
order, and waits for the driver to accept each one:

1. `UPLOAD_MODE` to `UPLOAD_LOCAL`, and `UPLOAD_SETTINGS` to the
   frame's directory and name in the outbox,
2. `CCD_FRAME_TYPE` to a light frame, and `CCD_BINNING`, `CCD_GAIN`,
   and `CCD_OFFSET` where the sequence sets them,
3. `FILTER_SLOT` to the slot whose name in `FILTER_NAME` is the
   sequence's filter,
4. the FITS keywords, one write each: `INDI_CLEAR`, then the session's
   keywords for the camera, the target's `fitsKeywords`, `OBJECT`, and
   last a new `FRAMEID`.

The runner writes all of these before every frame, because a person
in KStars can change a setting during the night, and
`observatory-operator` writes its own settings again when a camera's
driver restarts. `INDI_CLEAR` removes every keyword, including those
`observatory-operator` wrote, so the runner writes the session's
keywords again, and each frame's header has exactly the keywords the
runner wrote for it.

The runner records the frame in `status.exposure` and starts the
exposure. The driver sends the message `Image saved to <path>` before
it reports the end of the exposure, and the runner checks that the
path is the one it set. Then it counts the frame: in one write, it
clears `status.exposure` and adds one to the sequence's `pending` and
to the outbox claim's `pending` in `status.outboxes`. That write
starts a promotion ([The archive](/docs/concepts/archive/)).

A frame's path in the outbox is

```
/outbox/<Camera>/<Reservation>/<Target>/<name>.fits
```

where `<name>` comes from `archive.format`.

## Problems during a night

| What happens | What the runner does |
|---|---|
| The outbox has less than three frames' worth of space free | `Imaging` `False` with `OutboxFull`, and the `Warning` `AstrophotographyOutboxFull` on the reservation. The runner keeps the claim and checks again at the next change, such as the end of a promotion `Job`. |
| The driver reports `Alert` for a setting or the exposure, the exposure does not finish within the exposure time plus 2 minutes, the driver saves the frame under another name, or the worker does not respond | `Imaging` `False` with `ExposureFailed` and the cause in the message. The frame does not count. The runner aborts an unfinished exposure, keeps the claim, and starts the next frame after a minute. |
| The wheel's `FILTER_NAME` lacks the filter of a sequence with frames left, or of the centering | `Imaging` `False` with `NoFilter`, and the runner releases the target before the mount moves |
| A person renames the wheel's filters during the night, and a frame's filter is gone | `Imaging` `False` with `NoFilter` at that frame, and the runner releases the target |
| A sequence names a filter, and the train has no `FilterWheel` | `Imaging` `False` with `NoFilter` at the first frame that names a filter, after the slew and the centering, and the runner releases the target |
| The mount refuses to unpark, to track, or to slew, or does not finish a slew within 10 minutes | `Imaging` `False` with `SlewFailed`, and the runner releases the target |
| A centering frame does not solve at its exposure or at twice it, `centering.attempts` rounds (5 by default) end outside the tolerance, the plate scale is unknown, the worker or the solver returns an error, the camera fails the centering frame, or the mount refuses the sync | `Imaging` `False` with `CenteringFailed` and the cause in the message, and the runner releases the target. `status.centering` records the rounds that solved. |
| The connection to the INDI server drops | The runner reconnects with the same delays as for PHD2, finishes the frame in flight as it does after a restart, and slews again |
| PHD2 does not respond within 30 seconds, cannot loop, finds no star, refuses a guide or a dither, or does not settle | `Imaging` `False` with `GuidingFailed` and PHD2's reason in the message. A frame that already ended counts. The runner keeps the claim and guides again a minute later, before the next frame. |
| PHD2 loses its star during an exposure | The exposure continues and the frame counts. The runner skips the dither unless PHD2 finds the star again on its own, and guides again before the next frame. |
| The connection to PHD2 drops | The runner reconnects. The frame in flight counts and gets no dither. The next frame waits up to 30 seconds for PHD2, and guides again unless PHD2 reports that it is guiding. |
| PHD2 does not stop within 30 seconds of `stop_capture` | `Imaging` `False` with `GuidingFailed`, and the runner releases the target without a slew |

After the runner releases a target for `NoFilter`, `SlewFailed`,
`CenteringFailed`, or a PHD2 that did not stop, the same reservation
does not claim the target again until you edit the target. The runner
records that reservation in the target's `status.failed`, so this
survives a restart of the operator. A new reservation can claim the
target.

## Restarts and the end of a reservation

The operator keeps the state of a night in the status of the targets
and in the outbox, so it can read all of it again after a restart.

* **The operator restarts during an exposure.** The driver keeps
  exposing and writes the frame, because the runner sent every setting
  before the exposure began. The new runner reads `CCD_EXPOSURE`
  before it writes anything and waits while it is `Busy`. Then it asks
  the worker whether the file in `status.exposure` is in the outbox.
  It counts the frame if the file is there, and clears
  `status.exposure` either way, so a frame counts once.
* **The operator restarts while PHD2 is guiding.** The new runner
  stops PHD2 before it slews again, and then guides again.
* **The reservation ends.** `observatory-operator` stops the devices
  and the runner stops. The operator clears the target's
  `status.reservation` and `status.exposure`, and `Imaging` turns
  `False` with the reason `Waiting`. The frame in flight does not
  count. If the driver wrote it anyway, the next promotion moves it
  with the target's other frames and the catalog counts it.

## The conditions and Events

The runner sets the `Imaging` condition of the target it claims, and
each transition posts one `Event` on the target. A frame changes no
condition, so a night of frames posts a few `Event`s, not one for each
frame, and a failure that repeats posts one `Warning` and then one
`Event` when it recovers. Dithers post no `Event`. The runner posts
these `Event`s on the reservation:

| Reason | Type | When |
|---|---|---|
| `AstrophotographyNoOutbox` | `Warning` | the reservation has no outbox |
| `AstrophotographyIdle` | `Normal` | no target of the namespace is eligible, such as when every target is below the horizon |
| `AstrophotographyUnparked` | `Normal` | the runner unparked the mount |
| `AstrophotographyOutboxFull` | `Warning` | the outbox has too little free space for the next frame |
| `AstrophotographyWorkerPending` | `Warning` | the reservation's worker pod cannot start. The message gives the scheduler's reason. |
| `AstrophotographyNotCentered` | `Warning` | the runner images a target without centering it, because its `Solver` is missing or not `Ready` |
