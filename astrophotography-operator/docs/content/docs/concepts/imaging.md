---
title: Imaging
weight: 12
toc: true
---

# Imaging

When you reserve a telescope for `astrophotography-operator`, the
operator drives it the way you would from KStars: it chooses a target,
slews to it, and takes each frame the target's sequences want. It
drives the telescope's INDI server as one more client, so you can
watch the night in KStars at the same time. Each frame lands once,
under its final name, in the reservation's outbox, and the operator
counts it there until promotion moves it to your archive.

This version slews, guides with PHD2, dithers, and exposes. It does
not center the target with a plate solve yet, so the mount goes to the
coordinates with no solve, and the `Imaging` condition says `not
centered`. A `Target` that states `centering` is imaged the same way,
and the field takes effect when centering is built.

## The runner of a reservation

The operator runs one runner for each `Reservation` whose
`spec.holder` is `astrophotography-operator` and whose phase is
`Ready`. The runner reads only what `observatory-operator` publishes:

* the reservation's `status.endpoint`, the telescope's INDI server;
* `status.session`, the devices of the session, its outbox, and the
  merged FITS keywords of each imaging camera, as they were when the
  session started;
* each device's `status.indiDevice`, its name on the server, such as
  `CCD Simulator`;
* the session's `Guider`, if the telescope has one, and the `Guider`'s
  `status.endpoint`, PHD2's event server.

The imaging camera of a target is the `Camera` on the target's
`opticalTrain` that does not guide. The filter wheel is the
`FilterWheel` on the same train, and the mount is the session's
`Mount`. A device whose driver does not run yet has no
`status.indiDevice`, and the runner waits for it.

A reservation with no `spec.outbox` images nothing, because a frame
must land in a claim that outlives the camera's pod. It gets the
`Warning` `AstrophotographyNoOutbox`.

## Which target, and the claim

The runner takes the eligible target of its namespace with the highest
`priority`, then the oldest, then the first name. A target is eligible
when it is not suspended, its `Complete` condition is not `True`, a
sequence has work left, its `opticalTrain` is on the session, and no
other `Ready` reservation images it. A target with no `opticalTrain`
is eligible on a session with exactly one imaging camera.

The runner claims the target by writing its reservation's name into
the target's `status.reservation`, with the version of the target it
read. When two telescopes choose one target at the same moment, the
API server takes one write and refuses the other, and the second
runner chooses again. When no target is eligible, the reservation gets
the `Event` `AstrophotographyIdle`, and the runner waits for a change.

## The order of a target's work

1. Claim the target. `Imaging` turns `True` with the reason `Slewing`.
2. Stop PHD2 with `stop_capture` if it loops or guides, and wait until
   it reports the stop, so that it does not chase the stars while the
   mount moves.
3. Unpark the mount if it is parked, with the `Event`
   `AstrophotographyUnparked` on the reservation, and turn tracking on.
   A mount that already tracks gets no write.
4. Convert the target's J2000 coordinates to the equinox of the date,
   which an INDI mount takes in `EQUATORIAL_EOD_COORD`, slew, and wait
   for the mount to report the end of the slew, and then 5 seconds
   more for the tube to settle.
5. Guide, if the session has a `Guider`: `loop`, `find_star`, and
   `guide`, with `Imaging` `True` and the reason `Guiding`, and wait for
   PHD2's `SettleDone` ([Guiding and dithering](#guiding-and-dithering)).
6. Take one frame of the first sequence with work left, and then the
   next, with `Imaging` `True` and the reason `Exposing`. After every
   `dither.every` frames, dither and wait for `SettleDone`.
7. When no sequence has work left, release the claim. `Imaging` turns
   `False` with the reason `SequencesDone`, and the runner chooses the
   next target.

Before each frame the runner reads the target again, so an edit takes
effect at the next frame: new coordinates stop PHD2 and slew the mount
again, and a target you suspend is released with the reason
`Suspended`.

## Guiding and dithering

`observatory-operator` runs PHD2 for the telescope's `Guider`,
connects it to the guide camera and the mount, and leaves it idle,
because the holder of the reservation calibrates and guides. The runner
is that holder. It keeps one connection to PHD2's event server open for
the whole reservation, at the `Guider`'s `status.endpoint`, and dials
again after a backoff of 1 second that doubles up to a minute when the
connection drops. A telescope with more than one `Guider` guides with
the first by name.

Before each frame, the runner checks that PHD2 guides a star that
settled. When it does not, as after a slew, it sends `loop`, waits for
the first guide frame, sends `find_star`, which selects the best star,
and sends `guide`. PHD2 calibrates first when it holds no calibration,
which takes a minute or two. Then the runner waits for `SettleDone`,
because a frame taken while the star moves is blurred. The settle asks
for the star to stay within 1.5 guide camera pixels of the lock
position for 8 seconds, and PHD2 fails the settle after a minute. A
`Target` cannot change these values.

A `Target` with `dither` moves the lock position after every
`dither.every` frames, by up to `dither.pixels` guide camera pixels,
in right ascension alone with `raOnly`. A hot pixel or another fixed
pattern of the sensor then falls on another part of the sky in each
frame, and stacking removes it. The runner waits for `SettleDone`
before the next frame. A dither changes no condition, so it posts no
`Event`.

A star that PHD2 loses during an exposure does not stop the exposure,
and the frame counts. Unless PHD2 finds the star again on its own, the
runner sends no dither, and finds a star and guides again before the
next frame, with the reason `Guiding` once more.

A session with no `Guider` exposes unguided and does not dither, and
the `Imaging` message says so: `Reservation east-tonight exposes
Luminance 300s, unguided because the telescope has no Guider, not
centered.` With a `Guider`, the message reads `guided by Guider east,
dithered every 2 frames`.

## One frame

Before each frame, the runner asks the reservation's worker for the
free space of the outbox. The worker is a pod that the operator starts
beside the outbox while the reservation is `Activating` or `Ready`
([Solvers](/docs/concepts/solvers/)), so a runner that starts before
its worker is ready fails its first exposure with `ExposureFailed` and
tries again a minute later. With less than three frames' size free, it
does not expose (see [Problems during a night](#problems-during-a-night)).

Then it writes every setting the frame depends on, in this order, and
waits for the driver to accept each one:

1. `UPLOAD_MODE` to `UPLOAD_LOCAL`, and `UPLOAD_SETTINGS` to the
   frame's directory and name in the outbox;
2. `CCD_FRAME_TYPE` to a light frame, and `CCD_BINNING`, `CCD_GAIN`,
   and `CCD_OFFSET` where the sequence states them;
3. `FILTER_SLOT` to the slot whose name in `FILTER_NAME` is the
   sequence's filter;
4. the FITS keywords, one write each: `INDI_CLEAR`, then the session's
   keywords for the camera, the target's `fitsKeywords`, `OBJECT`, and
   last a new `FRAMEID`.

The runner writes all of them before every frame, not once for each
target, because a person in KStars can change a setting during the
night and `observatory-operator` writes its own again when a camera's
driver restarts. So each frame's header holds exactly the keywords the
runner wrote for it. `INDI_CLEAR` removes the keywords that
`observatory-operator` wrote too, which is why the session's keywords
are written again.

The runner then records the frame in `status.exposure` and starts the
exposure. The driver sends the message `Image saved to <path>` before
it reports the end of the exposure, and the runner checks that the
path is the one it set. It counts the frame: it clears
`status.exposure` and adds one to the sequence's `pending` and to the
outbox claim's `pending` in `status.outboxes`, in one write. That
write is what starts a promotion ([The archive](/docs/concepts/archive/)).

A frame's file in the outbox is

```
/outbox/<Camera>/<Reservation>/<Target>/<name>.fits
```

where the name comes from `archive.format`, so the file keeps its name
when it moves to the archive.

## Problems during a night

| What happens | What the runner does |
|---|---|
| The outbox has less than three frames' size free | `Imaging` `False` with `OutboxFull`, and the `Warning` `AstrophotographyOutboxFull` on the reservation. The runner keeps the claim and asks again on the next change, such as the end of a promotion `Job`. |
| The driver answers a setting or the exposure with `Alert`, does not finish within the exposure plus 2 minutes, saves the frame under another name, or the worker does not answer | `Imaging` `False` with `ExposureFailed` and the reason in the message. The frame does not count. The runner keeps the claim and tries the next frame after a minute. An exposure that did not finish is aborted first. |
| The sequence names a filter that the wheel does not have, or the train has no `FilterWheel` | `Imaging` `False` with `NoFilter`, and the runner releases the target |
| The mount refuses to unpark, to track, or to slew, or does not stop within 10 minutes | `Imaging` `False` with `SlewFailed`, and the runner releases the target |
| The connection to the INDI server drops | The runner connects again after a backoff of 1 second that doubles up to a minute, finishes the frame in flight as after a restart, and slews again |
| PHD2 does not answer within 30 seconds, cannot loop, finds no star, refuses a guide or a dither, or does not settle | `Imaging` `False` with `GuidingFailed` and PHD2's reason in the message. A frame that ended counts. The runner keeps the claim and guides again before the next frame, after a minute. |
| PHD2 loses its star during an exposure | The frame counts. Unless PHD2 finds the star again on its own, the runner sends no dither and guides again before the next frame. |
| The connection to PHD2 drops | The runner connects again with the same backoff as INDI's. The frame in flight counts and gets no dither. The next frame waits up to 30 seconds for PHD2, and guides again unless PHD2 reports that it guides. |
| PHD2 does not stop within 30 seconds of `stop_capture` | `Imaging` `False` with `GuidingFailed`, and the runner releases the target without a slew |

A target released for `NoFilter`, `SlewFailed`, or a PHD2 that did
not stop is not taken again by the same reservation until you edit
it. A new reservation tries it
again.

## Restarts and the end of a reservation

The record of a night is in the targets' statuses and in the outbox,
so the operator holds nothing it cannot read again.

* **The operator restarts during an exposure.** The driver keeps
  exposing and writes the frame, because every setting went out
  before the exposure began. The new runner reads `CCD_EXPOSURE`
  before it writes anything, waits while it is `Busy`, and then asks
  the worker whether the file that `status.exposure` names is in the
  outbox. It counts the frame if it is, and clears `status.exposure`
  either way, so a frame counts once.
* **The operator restarts while PHD2 guides.** The new runner stops
  PHD2 before it slews again, and then guides again.
* **The reservation ends.** `observatory-operator` stops the devices.
  The runner stops, and the operator clears `status.reservation` and
  `status.exposure` of the target. `Imaging` turns `False` with the
  reason `Waiting`. The frame in flight does not count, and if the
  driver wrote it anyway, promotion finds it in the outbox and the
  catalog counts it.

## The conditions and Events

The runner sets the `Imaging` condition of the target it claims, and
each transition posts one `Event` on the target. An exposure changes no
condition, so a night of frames posts a few `Event`s, not one for each
frame. A failure that repeats posts its first `Warning` and its
recovery. The runner's actions on the reservation post `Event`s on the
reservation with the prefix `Astrophotography`:

| Reason | Type | When |
|---|---|---|
| `AstrophotographyNoOutbox` | `Warning` | the reservation has no outbox |
| `AstrophotographyIdle` | `Normal` | no target of the namespace is eligible |
| `AstrophotographyUnparked` | `Normal` | the runner unparked the mount |
| `AstrophotographyOutboxFull` | `Warning` | a target found the outbox full |
