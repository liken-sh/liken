# outbox.TargetGlob has no caller

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TargetGlob` in `outbox/outbox.go` returns the pattern
`/outbox/*/*/<target>` for a target's directory under every camera
and every reservation. Only its own test,
`TestTargetGlobMatchesEveryCameraAndReservation` in
`outbox/outbox_test.go`, calls it. The promotion `Job` builds the same
pattern itself: `frames` in `promote/promote.go` calls
`filepath.Glob(filepath.Join(r.Outbox, "*", "*", r.Target))`, with the
`--outbox` flag in place of `outbox.Root`. So the layout of the
outbox is written in two places, and a change to one leaves the other
behind.

## Evidence

A search of the component for `TargetGlob` finds the declaration and
the test only.

## What would confirm it

Delete `TargetGlob` and its test, and build the module. The build
passes if nothing else reads it.

## What a fix has to decide

- Whether `TargetGlob` takes the outbox's root as a parameter and
  `frames` calls it, so the outbox package holds the one copy of the
  layout.
- Or whether `TargetGlob` and its test are deleted.
