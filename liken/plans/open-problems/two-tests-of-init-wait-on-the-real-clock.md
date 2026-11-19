# Two tests of init wait on the real clock

Open problem, recorded on 2026-11-18. It is not measured.

`init/proving_test.go` and `init/supplicant_test.go` wait on real time
and not on the fake clock of a `synctest` bubble. The root `AGENTS.md`
says that a test never waits on the wall clock, because such a test
makes every push slower and can fail on a loaded runner.

## The evidence

On 2026-11-19 the waits were checked against the code with
`grep -n "time.Sleep\|time.After(" init/proving_test.go init/supplicant_test.go`.
No unit test can show a test that waits on the wall clock. The code
shows it:

- `TestAssertProvenSlotUnderLockWaitsForTheFirmwareLock` waits 50
  milliseconds of real time to show that a call blocks on the lock
  (`init/proving_test.go:218`).
- `TestStoppingDuringARetryEndsTheLoopWithNothingToSignal` polls with
  `time.Sleep` until the loop has tried two starts
  (`init/supplicant_test.go:218`).
- `TestTheEventStreamComesBackAfterARestart` sleeps 20 milliseconds
  before it serves the socket, and then polls with `time.Sleep` until
  the loop attaches (`init/supplicant_test.go:270` to `284`).
- The other tests of `init/supplicant_test.go` that run the loop with
  `supervised` wait out its real restart delays of a few
  milliseconds, outside a bubble.

The `time.After` timeouts at `init/proving_test.go:227` and
`init/supplicant_test.go` lines 83, 136, 196, 241, and 405 bound a
wait and do not make one. `TestASupplicantThatNeverAttachesIsStoppedAndAwaited`
already runs in a bubble.

## What would confirm it

Read each wait in the two files and time the tests with
`go test -run <name> -count 20 ./init`.

## What a fix has to decide

Whether each test moves into a `synctest` bubble, or the code under
test takes a clock that the test controls.
