# 04, The scheduler

Proposed on 2026-10-12. The horizon filter is built (2026-10-13): a
runner does not claim a target below the telescope's `Horizon`, or
below 0° when the telescope names none, releases a target that sets
before its next frame, and waits for the first rise on a timer. The
sky's geometry is a port of Meeus in `sky/`, with the rise found by a
search at one-minute steps. This plan records the problem and the
decisions of the design conversation; the other filters and the score
are not designed yet.

## The problem

Inside a reservation, the operator has to choose what to image next.
The right answer changes through the night: targets rise and set, the
moon moves, twilight ends and begins, and part of the sky is behind
trees. A plan made at dusk breaks the first time a cloud passes or a
solve fails.

## Decisions

- **Choosing a target is a placement problem.** Targets state what they
  need, and the telescope and the sky state what they offer at each
  moment, the way pods and nodes do for kube-scheduler. After each
  exposure the operator filters out the targets that cannot be imaged
  now and scores the rest.
- **It plans one exposure at a time.** A cloud or a lost guide star
  costs that exposure and not the night. N.I.N.A.'s Target Scheduler
  uses the same method. The status can still show the order the score
  expects, as a preview that the next choice may replace.
- **The operator computes the sky itself**, from the site's coordinates
  and the time: each target's altitude and azimuth, twilight, and the
  moon.
- **The telescope's horizon comes from `observatory-operator`** ([plan
  18](../../observatory-operator/plans/completed/18-saved-horizons.md)), because
  the horizon is a fact about where the telescope stands.
- **The score favors** a target that sets soon, one that is nearly
  done, and the one already in the camera, so the telescope does not
  switch back and forth.
- **A pass runs on events**: an exposure ends, a condition of the
  observatory changes, or PHD2 loses its star. Rise, set, and twilight
  are clock times, so a timer for each is correct.

## Still open

The filters, the score, and the Go source of the sky's geometry. A Go
port of Meeus's *Astronomical Algorithms* exists; whether it is
maintained, and whether it covers rising and setting, was not checked.
