# Tests that fail under load

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

During the rewrite of the component's comments on 2026-11-18, full
runs of `go test ./...` in `astrophotography-operator/` failed on a
different test in most runs, at a load average of about 100 to 220.
Each failing test passed when it ran alone. Several agents were
editing files of the component at the same time, so some of these
failures can come from a file that was half written. Two results do
not depend on that:

- `TestTheRetryAnnotationWaitsForTheRunningJob` in
  `promotionretry_test.go` failed 3 of 60 runs on `main`, where no
  file changed, and 1 of 20 runs with `-count=20` in a worktree with
  comment changes only. The failure is that no `m42-promote-2` `Job`
  starts after `m42-promote-1` ends.
- `TestReadyFollowsTheSolversPod` in `solvers_test.go` failed once in
  a full run and passed 9 times alone.

These tests also failed once each in a full run and passed alone:
`TestFramesFollowEachOther` (`runner_test.go`),
`TestATargetThatSetsIsReleasedBeforeTheNextFrame` (`horizon_test.go`),
`TestAFailedFrameStartsNoJobUntilANewFrame` and
`TestAJobWhosePodIsGoneRunsAgain` (`promotionreport_test.go`), and
`TestADeletedTargetWaitsForItsPendingFrames`
(`targetfinalizer_test.go`).

Each of these tests runs inside a `synctest` bubble. A test in a
bubble waits on the fake clock, so its result should not depend on the
load of the machine. A failure under load means that something the
test depends on runs outside the bubble, or that the test reads the
state before every goroutine of the operator is durably blocked.

## Evidence

For the retry test, one suspected order: the annotation reaches
`retry` in `promotionretry.go` before the operator's store shows
`m42-promote-1`, so `retry` finds nothing left to clear, removes the
annotation, and the retry is used up. `currentJobs` in `promotion.go`
adds the Jobs that the loop created and the watch has not delivered,
so this order is not shown by the code, and the cause is unknown.

## What would confirm it

Run each test with `-count=200` under load, with `-race`, on a tree
that nobody edits, and record the failures. For a test that fails,
log the order of the fake API server's requests in the failing run
and compare it with a passing run.

## What a fix has to decide

- For each test that fails, whether the cause is in the operator, such
  as a retry that can be lost, or in the test's fakes, such as a
  goroutine or a connection outside the bubble.
- Whether the fix belongs in a shared fake, such as `fakeapi_test.go`,
  so that every test that uses it gains the fix.
