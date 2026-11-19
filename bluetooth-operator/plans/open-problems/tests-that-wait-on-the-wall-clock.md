# Tests that wait on the wall clock

Open bug, recorded on 2026-11-18. Each wait below was checked against
the code on 2026-11-19, at `relay_test.go:31`, `cli/pair_test.go:114`,
and `connect_test.go:284,318`. No unit test can fail on them, because
each one is in a test's own code.

The root `AGENTS.md` says that a test waits on the fake clock of a
`synctest` bubble, never on real time, and the `testing` skill lists
"Sleep or poll on the wall clock to let something happen" among what
a test never does. Two helpers of this component poll on the wall
clock, and two tests bound a wait with a real timer:

- `waitFor` in `relay_test.go` calls `time.Sleep(time.Millisecond)`
  until a condition holds, for up to 2 seconds. `relay_test.go`,
  `wake_test.go`, and `claimsettings_test.go` call it.
- `waitFor` in `cli/pair_test.go` calls `time.Sleep(5 *
  time.Millisecond)` until a condition holds, for up to 2 seconds.
  The pairing flow tests call it to wait for text such as
  `no device 9` in the output.
- `TestTheReconcilePassDoesNotWaitForAConnect` and
  `TestAFinishedConnectWakesTheLoop` in `connect_test.go` wait on
  `time.After(5 * time.Second)` and run no bubble.

## Evidence

The fakes under these tests need no real wait. `fakeKernel` in
`inputkernel_test.go` gives each node an `io.Pipe`, whose reads block
on channels, and the connector's fake radio blocks `Connect` on a
channel. So a goroutine that waits on them is durably blocked inside a
bubble, and `synctest.Wait` can replace the poll. The `testing` skill
allows a real timeout only as a bound on a test outside a bubble that
must be outside, such as one that runs a real process.

A slow CI runner can make a 2-second poll fail when the code is
correct, and a correct run still spends real time in each sleep.

## What would confirm it

Run each test in a `synctest.Test` bubble with `synctest.Wait` in
place of `waitFor` and `time.After`. A test that then hangs or fails
shows a goroutine that the bubble does not see as durably blocked,
which is the reason the helper exists.

## What a fix has to decide

Whether the relay's goroutines and the CLI's watch of the fake dynamic
client can run inside a bubble, or whether one of them reads something
that a bubble cannot see.
