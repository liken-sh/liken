# Two tests of init wait on the real clock

Open problem, recorded on 2026-11-18. It is not measured.

`init/proving_test.go` and `init/supplicant_test.go` wait on real time
and not on the fake clock of a `synctest` bubble. The root `AGENTS.md`
says that a test never waits on the wall clock, because such a test
makes every push slower and can fail on a loaded runner.

## What would confirm it

Read each wait in the two files and time the tests with
`go test -run <name> -count 20 ./init`.

## What a fix has to decide

Whether each test moves into a `synctest` bubble, or the code under
test takes a clock that the test controls.
