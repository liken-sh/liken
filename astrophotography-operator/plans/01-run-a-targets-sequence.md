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
are standard keywords. The second depends on how precise INDI's
`DATE-OBS` is and whether two cameras of one model report different
`INSTRUME` values; neither was checked. The identity is written into
every archived frame, so it is the hardest decision here to change
later.
