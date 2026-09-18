# The capture stream tests flake in CI

Open problem. Three capture tests fail now and then in CI with an
`unexpected EOF` while they read the tap's body, though they pass on a
workstation under stress. They are skipped with `t.Skip` in
`capture_test.go` until the race is found and fixed:

- `TestATapRunsPwRecordWithTheLineThePlanStates`
- `TestAFinishedSpanEndsCleanly`
- `TestAFinishedWAVSpanEndsCleanlyOverHTTP2`

## What the tests see

Each test starts a capture harness, which runs a fake `pw-record` and
an encoder and streams the result over HTTP. The client reads the body
with `io.ReadAll` and checks the byte count. The flake is an
`unexpected EOF` from that read: the HTTP framing ends before the whole
body arrives. `TestAFinishedWAVSpanEndsCleanlyOverHTTP2` reports the
same fault as an HTTP/2 `INTERNAL_ERROR` stream reset.

## What is known

The failures appear only in CI, which runs `go test -race`, and they
move between tests from one run to the next. Locally the same tests
pass many times over, with and without `-race`. So the cause is a race
in how the capture stream ends, which CI's timing exposes and a
workstation rarely does. The likely shape is the handler ending the
response, or the fake `pw-record` closing its pipe, before the last
bytes reach the client.

## What a fix needs

Find where the capture stream can end the response before it has
written every byte the reader expects, and order the flush, the
subprocess drain, and the handler return so the body always finishes.
Then remove the three `t.Skip` calls and confirm the tests hold under
`-race` and CI load.
