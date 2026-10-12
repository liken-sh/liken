# The astrophotography-operator design

Not built. `astrophotography-operator` images the targets a person
declares, on the telescopes that `observatory-operator` controls. A
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
  not dark enough, and scores the rest. It chooses one exposure at a
  time, so a cloud or a failed solve costs only that exposure.
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

## The plans

Each plan below is a stub: the problem and what the design
conversation of 2026-10-12 settled. They are in the order they build
on each other. `observatory-operator` plans 19 and 20 come before plan
01, its plan 18 before plan 04, and its plan 21 before plan 05.

| Plan | Subject |
|---|---|
| [01](01-run-a-targets-sequence.md) | `Target`, and running its sequences inside a reservation |
| [02](02-solving-and-centering.md) | The `astrometry` image, the `Solver`, and centering |
| [03](03-promotion-to-the-archive.md) | Moving frames from the outbox to the target's archive |
| [04](04-the-scheduler.md) | Choosing the next target from the sky, the horizon, and the work left |
| [05](05-reservations-on-demand.md) | Creating and ending reservations as the sky allows |
| [06](06-grading-policies.md) | Rejecting frames by a person's policy |
| [07](07-calibration-frames.md) | Capturing and keeping darks and flats |
