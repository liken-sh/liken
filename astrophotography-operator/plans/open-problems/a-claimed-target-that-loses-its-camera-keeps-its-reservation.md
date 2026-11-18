# A claimed target that loses its camera keeps its reservation

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

A runner claims a `Target` by writing its reservation's name to
`status.reservation`. Other Ready reservations skip a target that a
Ready reservation has claimed. `claimNext` in `selection.go` calls
`releaseIneligible` for each target that `eligible` refuses, but
`releaseIneligible` releases the target only when it is being
deleted, is suspended, or has no frames left. For every other reason
that `eligible` refuses a target, the claim stays.

An example: a person edits `spec.opticalTrain` of a claimed target to
a train of another telescope. `cameraFor` finds no camera in the
reservation's session, so `eligible` returns false, and
`releaseIneligible` returns nil. `status.reservation` still names the
reservation, so the reservation of the other telescope skips the
target until the first reservation is no longer Ready.

During work, `next` in `imaging.go` reads the target again before
each frame, but it checks only suspend, frames left, deletion, the
horizon, and the coordinates. It does not check the train, and
`work.camera` keeps the camera that `prepare` chose. So an edit of
`spec.opticalTrain` during work does not move the target until its
work ends.

## Evidence

- `selection.go`, `eligible`: returns false when `cameraFor` fails.
- `selection.go`, `releaseIneligible`: the `switch` has cases for
  `DeletionTimestamp`, `Spec.Suspend`, and no frames left, and
  returns nil otherwise.
- `imaging.go`, `prepare`: fails the target with `NoTrain` when
  `cameraFor` fails, but `claimNext` never reaches `prepare` for a
  target that `eligible` refused.

## What would confirm it

A test with two Ready reservations on two telescopes, in which the
first reservation has claimed a target and the test then changes the
target's `spec.opticalTrain` to a train of the second telescope. The
bug is real if the second reservation does not claim the target while
the first stays Ready.

## What a fix has to decide

- Whether `releaseIneligible` releases a claimed target for every
  reason that `eligible` refuses it, and with which reason for the
  `Imaging` condition, such as `NoTrain`.
- Whether `next` checks the train before each frame, so an edit
  during work releases the target at the next frame.
