# Two tests prove less than their names say

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TestACaptureWaitsForEachReplyBeforeTheNextRequest` in
`captureclient_test.go` should fail when the capture client sends a
second capture request before the compositor replies to the first,
which weston treats as a protocol error. The fake compositor checks
the `awaiting` field in `reply`. But the fake reads and handles one
request at a time on one goroutine, and `reply` sets `awaiting` to
true and back to false before it returns. So `awaiting` is always
false when the next request arrives, and the check cannot fail. A
client that sent two requests at once would pass the test.

`TestCrtcModeOmitsAZeroRefresh` in `currentmode_test.go` has the same
input and the same expected output as
`TestCrtcModeReadsTheKernelsOwnName`: `crtcWithMode("1280x720", 1)`
and `"1280x720"`. `crtcWithMode` leaves `Vrefresh` at 0, so both tests
check the same case.

## Evidence

- `captureclient_test.go`, `reply`: `s.awaiting = true` and
  `defer func() { s.awaiting = false }()` around a body that only
  sends events.
- `currentmode_test.go`, lines 35 to 53.

## What would confirm it

Change the capture client to send its next request before it reads
the reply, and run the first test. It should fail and does not.

## What a fix has to decide

The fake can set `awaiting` when it gets a request and clear it only
when the client's next request follows a reply that the client has
read, or it can queue its replies and send them after it reads the
next message. The fix also decides whether one of the two
`crtcMode` tests is removed or given an input of its own.
