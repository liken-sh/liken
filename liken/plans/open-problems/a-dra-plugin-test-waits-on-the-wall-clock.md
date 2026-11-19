# A DRA plugin test waits on the wall clock

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TestServeRespondsOnBothSockets` in
`machine-operator/draplugin_test.go` waits for a socket file with a
loop of `time.Sleep` on the wall clock, outside a `synctest` bubble.
The root `AGENTS.md` and the `testing` skill say that a test waits on
the fake clock of a bubble, and never sleeps or polls on the wall
clock.

## The evidence

`waitForSocket` in `machine-operator/draplugin_test.go` checks for the
file with `os.Stat` every 10 milliseconds until a deadline of 5
seconds from `time.Now()`. The test starts `serveDRAPlugin` in a
goroutine and connects to it through real Unix sockets with gRPC. The
`testing` skill says that a goroutine that reads a real socket is not
durably blocked, so this test cannot move into a bubble as it is.

## What would confirm it

A read of the test is enough to see the wall-clock wait. On a loaded
CI runner, the wait can reach its deadline and fail the test.

## What a fix has to decide

Whether `serveDRAPlugin` reports when its sockets are ready, so the
test waits on that signal and not on the file. Or whether the test
reaches the plugin through in-memory connections, as
`kubernetes/apiservertest` does for the API server, so it can run in a
bubble.
