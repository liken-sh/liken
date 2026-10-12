# The astrophotography-operator design

Not built. `astrophotography-operator` runs a night of imaging on a
telescope that `observatory-operator` controls. It chooses the next
exposure from declared targets, centers and guides through the
telescope's INDI server and PHD2, and counts the frames that pass
grading. This is mode 2 of [root plan
74](../../plans/74-astrophotography.md), and it is the end goal of the
two components. A person can drive the same telescope from KStars
instead, which is mode 1, and `observatory-operator` built it.

## The contract with `observatory-operator`

`astrophotography-operator` depends on `observatory-operator`, and
`observatory-operator` does not depend on it. It reaches the hardware
through three things that `observatory-operator` already provides:

- A `Session` creates the telescope's `Reservation` at dusk and deletes
  it at dawn. The `Reservation` makes the `Session` the holder, and
  starts and stops the equipment ([plan
  06](../../observatory-operator/plans/completed/06-the-resources.md)).
- The `Telescope`'s status names the `Service` of its INDI server, and
  each device's status names its INDI device and the server it is on
  ([plan
  16](../../observatory-operator/plans/completed/16-kinds-are-indi-interfaces.md)).
  The operator speaks INDI with the `indi` package of [plan
  04](../../observatory-operator/plans/completed/04-the-indi-client-in-go.md).
- The `Guider` runs PHD2, and the operator drives it over PHD2's event
  API on port 4400, the same way KStars does ([plan
  09](../../observatory-operator/plans/completed/09-the-guider.md)).

An exposure, a dither, and a guide pulse happen too often to be
objects in the API, so the operator drives them over INDI and PHD2's
API, not through Kubernetes resources.

## Frames

A 26-megapixel 16-bit frame is about 52 MB of FITS. A camera driver can
send each frame to its clients as a BLOB, write it to a directory and
send only its path in `CCD_FILE_PATH` (`UPLOAD_LOCAL`), or do both. The
server sends BLOBs to a client only after the client asks with
`enableBLOB`. In mode 2 the camera writes each frame to a volume in its
own pod, and the operator never sends `enableBLOB`, so it receives the
path and none of the frame's bytes. A BLOB is base64, about 1.37 times
the frame's size, and crosses the pod network only while a client such
as KStars asks for it.

Where that volume comes from is not settled: [Where frames are
stored](open-problems/where-frames-are-stored.md).

## Grading and stacking

The planner needs the number of accepted frames for a target, not the
number taken. A frame with a passing cloud, wind shake, a lost guide
star, or drifting focus is rejected. Grading measures each frame's star
count, half-flux radius, and star shape, and compares them with the
other frames of the session.

Grading reads the whole frame, so it runs next to the frames, and it
finds each new file with an inotify watch followed by one look at the
directory. It writes one row for each frame to a `Catalog`, the SQLite
database that library-operator replicates with Corrosion. A frame is
not a Kubernetes object, because a night makes hundreds of them, and
the operator reads only the counts.

Stacking runs later, on a machine with more cores, as a Siril `Job`
that reads the accepted frames. A stack of the frames so far can run
during the night to show progress. The Indexed `Job` of
[library-operator plan
79](../../library-operator/plans/completed/79-one-video-per-index.md),
with one work item for each index, is the shape to start from.

## Centering and plate solving

astrometry.net's `solve-field` turns a frame into coordinates. It
solved a simulated frame in 1.2 seconds in the Docker tests of
`observatory-operator` [plan
03](../../observatory-operator/plans/completed/03-the-topology-by-hand.md#the-docker-tests),
and solved a frame that crossed a cluster in that plan's drill. A solve
during centering waits on the result, so the solver runs where the
frame is.

The mount reports JNow in `EQUATORIAL_EOD_COORD`, and `solve-field`
reports J2000. For a target near M42 in 2026 the difference was about
20 arcminutes in right ascension, so the operator converts between the
epochs before it centers. The `indi` package does not convert.

The solver's index files depend on the field of view, which comes from
the focal length and the camera's sensor. The `Camera`'s
`status.info` holds the sensor from `CCD_INFO` once the camera has
connected. astrometry.net recommends index files whose quads span about
10% to 100% of the field width, so a fixed rig needs a few hundred
megabytes to a few gigabytes; that range was not computed for any
specific optics. The full set of 2MASS and Tycho-2 index files in the
Ubuntu packages is 9.9 GB. The files are a per-node cache from
per-node-csi-driver, fetched from a mirror in the project's object
storage next to the release channel. The driver keeps no size budget,
so the operator deletes the index files that the telescope's optics no
longer use. data.astrometry.net serves the
Tycho-2 `4100` and 2MASS `4200` series, and links the Gaia `5200`
series to a `temp` directory on `portal.nersc.gov`, so the project
keeps its own copy.

## The planner

The planner chooses the next exposure, not the whole night. After each
exposure it filters the targets and scores the rest, the same shape as
kube-scheduler's filter and score plugins. The filters remove a target
that is not up long enough, is too close to the moon, or has no plan
that the current twilight allows. The score favors a target that sets
soonest, a target that is nearly complete, and the target already on
the camera, so the rig does not switch back and forth.

NINA's Target Scheduler plugin uses the same method
([planning engine](https://tcpalmer.github.io/nina-scheduler/concepts/planning-engine.html)).
The method keeps working when a cloud, a failed solve, or a lost guide
star breaks the plan an hour in. A night planned in advance does not.
`status` still carries the order and the window the scorer expects for
each target, as a preview that the next choice can replace.

A pass runs on events: an exposure completes, a condition of the
observatory changes, or PHD2 reports a lost guide star. Target rise and
set times and twilight are clock times, so a timer for them is correct.

Autofocus is the least certain part. It fits a V-curve to the half-flux
radius of the stars, which is simple to state and slow to tune on real
skies. The CCD simulator reads the focuser simulator's position and
seeing to set the size of its stars, so the fit can be tested before a
real night.

## The resources

A first sketch, with names that are not settled:

```yaml
kind: Target
spec:
  telescope: backyard
  coordinates: {ra: "05h35m17s", dec: "-05°23'28\""}
  rotation: 90deg
  constraints: {minAltitude: 30deg, moonSeparation: 60deg}
  plans:
    - {filter: Ha, exposure: 300s, wanted: 60}
    - {filter: OIII, exposure: 300s, wanted: 60}
status:
  plans:
    - {filter: Ha, taken: 41, accepted: 37}
```

## The screen

A screen in the house shows the session: the current target, the
progress of each plan, the last frame, and the latest stack. It takes a
`Player`'s idle screen through `spec.idle.controller`, the way the
media browser does. It never talks to INDI. A person's action edits a
resource, such as a field on the `Target` to skip it, so the operator
stays the only writer to the telescope in mode 2.

A guiding view draws from PHD2's event API, not from PHD2's window,
which cannot hide its menu bar or go full screen. The `GuideStep` event
carries each frame's error in each axis, the SNR, and the half-flux
diameter, and `get_star_image` returns the pixels around the guide
star. A screen next to the rig keeps night vision with red colors and a
low backlight, which display-operator's brightness control allows.

## Open problems

- [Where frames are stored](open-problems/where-frames-are-stored.md)
- [Where the field of view comes from](open-problems/where-the-field-of-view-comes-from.md)
- [Calibration frames](open-problems/calibration-frames.md)
