# 01, Run a target's sequence

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the resources are not
designed yet.

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
  19](../../observatory-operator/plans/19-the-frames-outbox.md)), and
  the keywords it owns, such as the target, through the declared
  keywords of [`observatory-operator` plan
  20](../../observatory-operator/plans/20-declared-fits-keywords.md).
  The operator never asks the server for a frame's bytes.
- **Each frame has a global identity, written in its header.** The
  file name is the person's format and can change; the identity cannot.
  Frames are immutable once written, so a copy of a frame anywhere is
  the same frame.
- **Frames are records, not objects.** A night makes hundreds of
  frames, so each one is a record in a `Catalog`, keyed by its identity
  and its reservation, and the target's status carries only the counts.

## Still open

The `Target` resource, how the identity is formed, and the frame
record. Two ways to form the identity were discussed: a UUID in a
keyword of our own, or the camera and the exposure's start time, which
are standard keywords. The identity is written into every archived
frame, so it is the hardest decision here to change later.

On 2026-10-12, the CCD simulator of the pinned `indi-simulators` image
answered three questions that bear on the choice
([`observatory-operator` plan
20](../../observatory-operator/plans/20-declared-fits-keywords.md)
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
