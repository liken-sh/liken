# The astrophotography-operator design

Plans 01, 02, and 03 are built, through the pieces of [plan
08](completed/08-the-component-and-the-first-session.md), and the first imaging
session ran on the QEMU dev cluster on 2026-10-12: [the first
session](completed/08-the-component-and-the-first-session.md#the-first-session)
records it, and the operator was released in `2026.10.13-001`. Plan
04's horizon filter is built, and the rest of plans 04 to 07 is not.

`astrophotography-operator` images the targets a person declares, on
the telescopes that `observatory-operator` controls. A
person states what they want, such as sixty five-minute frames of M42
through each of three filters, and the operator fits that work into
the nights the sky allows, and delivers each frame, fully labeled, to
the person's archive. This is mode 2 of [root plan
74](../../plans/74-astrophotography.md).

## What it does

- **Targets.** A `Target` states an object and the frames wanted of
  it: each sequence a filter, an exposure, and a count. A target
  lasts as long as the work does, across as many nights as it takes,
  and its status keeps the progress.
- **Running a sequence.** Within a `Reservation` that names this
  operator as its holder, the operator slews, centers, guides, and
  exposes, and switches targets as the night moves. A reservation is
  one continuous block of the telescope's time, and many targets share
  it. There is no separate session object: a night's work is the
  frames of one reservation.
- **Choosing targets.** The operator places targets on the telescope's
  time the way a scheduler places workloads on nodes. It computes the
  sky from the site's coordinates and the date, removes the targets
  that are below the telescope's horizon, too close to the moon, or
  not dark enough, and orders the rest by priority and then by how
  little of their season is left for their work. It chooses after
  each frame, so a cloud or a failed solve costs only that frame, and
  it switches only when the gain beats the time the switch costs.
- **Targets by name.** A person can name an object, such as M42, in
  place of its coordinates, and the operator looks it up in a catalog
  in its image.
- **Reservations on demand.** When a telescope allows it, the operator
  creates a reservation when some target can be imaged and ends it
  when none can. A person's own reservation always takes the telescope
  first.
- **Solving and guiding.** Plate solving centers each target.
  `observatory-operator` already runs PHD2 for each telescope, and
  this operator drives it.
- **Frames to storage.** The camera writes each frame once, into the
  reservation's outbox on the machine at the pier. The operator then
  promotes it to the archive that its target names, under the
  person's file names.
- **Labeled frames.** Every frame carries the standard FITS keywords
  and the person's own, as the camera writes it. A frame never changes
  after it reaches the archive.
- **Grading.** A person can declare a policy that rejects poor frames,
  and a rejected frame does not count toward the target.
- **Calibration frames.** The operator captures and keeps darks and
  flats over time.

## What it does not do

- Develop or stack frames. The archive is the hand-off to the tools
  that do.
- Provide a screen or another interface to control or watch a night.
  The resources, their conditions, and their `Event`s are the
  interface.

## How it is tested

The INDI simulators give a happy-path night with no hardware: the CCD
simulator draws catalog stars where the mount points, so solves are
real, and it sizes its stars from the focuser simulator's position, so
focus and grading measures respond. PHD2 guides the guide camera
simulator, and the weather simulator can turn unsafe. On the dev
cluster's QEMU fleet, a whole night needs no network, once the images
are loaded and the `Solver`'s claim holds index files from the laptop.
The lab registry of the dev cluster serves the images that the laptop
built, and [plan 08](completed/08-the-component-and-the-first-session.md) holds
the drill of the first session.

The simulators do not model the time of day or clouds. The CCD
simulator draws stars at noon and below the horizon, so the scheduler's
geometry is proved by tests with a fake clock, and a drill in daylight
puts the simulated site where it is night. Clouds that dim the stars,
a replugged camera, and a slow link to a network share wait for the
physical testbed.

## What is hard to change later

Every plan below adds to what exists: a field, a procedure step, a
resource, or a pod. A plan that proves wrong can be dropped without
undoing the ones before it, and booking a telescope by hand keeps
working whatever happens to [plan 05](05-reservations-on-demand.md).
The exception is what the operator writes into frames: the FITS
keywords, the frame's identity, and the suffix it may add to a file
name. Archived frames never change, so those three deserve the most
care.

## The plans

Each plan below records a problem and the decisions that the design
conversation of 2026-10-12 settled. Plans 01, 02, and 03 are designed,
and together they make one imaging session; plans 04 to 07 leave the
implementation to be designed. [Plan
08](completed/08-the-component-and-the-first-session.md) builds first: it gives
the component's scaffold, its import of `observatory-operator`'s
`indi`, `phd2`, and `observatory` packages, the order in which agents
build the pieces of plans 01 to 03, and the drill of the first session.
The plans are otherwise in the order they build on each other.
`observatory-operator` plans 19 and 20 come before plan 01, its plan
18 before plan 04, and its plan 21 before plan 05.

| Plan | Subject |
|---|---|
| [01](completed/01-run-a-targets-sequence.md) | Built: `Target`, running its sequences inside a reservation, and the frame catalog |
| [02](completed/02-solving-and-centering.md) | Built: the `astrometry` image, the `Solver`, the worker, and centering |
| [03](completed/03-promotion-to-the-archive.md) | Built: moving frames from the outbox to the target's archive, and verifying them |
| [04](04-the-scheduler.md) | Choosing the next target from the sky, the horizon, and the work left. The horizon filter is built |
| [05](05-reservations-on-demand.md) | Creating and ending reservations as the sky allows |
| [06](06-grading-policies.md) | Rejecting frames by a person's policy |
| [07](07-calibration-frames.md) | Capturing and keeping darks and flats |
| [08](completed/08-the-component-and-the-first-session.md) | The component's scaffold, the order of the build, and the drill of the first session |
| [09](09-a-targets-position-by-name.md) | A target's position by name, from a catalog in the image |
| [10](10-planetary-and-solar-imaging.md) | Planetary and solar imaging, a stub |
