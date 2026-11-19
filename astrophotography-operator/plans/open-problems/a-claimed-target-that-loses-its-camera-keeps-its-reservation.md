# A claimed target that loses its camera keeps its reservation

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test in the runner's world starts one Ready reservation, with a
target that the reservation has already claimed in
`status.reservation` and whose `spec.opticalTrain` is a train that the
session has no camera on. After a minute of the fake clock, the test
reads the target and wants no claim. It failed with:

```
after a minute the target is claimed by "east-tonight", want no claim because the session has no camera on west-imaging
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When you move a claimed target to a train that the reservation has no
camera on, the target stays claimed until that reservation is no
longer Ready, so the other telescope's reservation does not take it.

- Does the reservation give the target up at once, and with which
  `Imaging` reason, such as `NoTrain`? Or does it keep the claim, as
  now?
- Does it also give the target up between two frames, while it is
  imaging that target, or only when its work on the target ends?
