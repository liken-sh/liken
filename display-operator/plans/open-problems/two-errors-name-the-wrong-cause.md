# Two errors name the wrong cause

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

The component's `AGENTS.md` asks that an error include its source's
own text, so a person reads the cause from the log or the response.
`dialFault` in `forward.go` turns the error of a request to a capture
sidecar into the caller's problem document, and two of its paths give
a cause other than the one that happened.

Every `net.OpError` whose `Op` is `dial` gets "refused the
connection", which includes a dial that timed out. Every error that
no case matches gets a 502 that says the sidecar "responded with
something that is not HTTP", which includes a connection reset and a
request that the caller cancelled. The log line beside each fault has
the real error, so only the caller's message is wrong.

## Evidence

- `forward.go`, `dialFault`: the `operation.Op == "dial"` case returns
  `refused`, and the fall-through builds the "not HTTP" fault.

## The reproduction

The test called `dialFault` with two errors and asserted words that
the detail must not hold: a `net.OpError` whose `Op` is `dial` and
whose cause is `os.ErrDeadlineExceeded`, which must not say "refused",
and a `net.OpError` whose `Op` is `read` and whose cause is
`ECONNRESET`, which must not say "not HTTP". It failed with:

```
the detail for dial tcp: i/o timeout is "the capture sidecar on node-1 refused the connection", want one that does not say "refused"
the detail for read tcp: read: connection reset by peer is "the capture sidecar on node-1 responded with something that is not HTTP", want one that does not say "not HTTP"
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When display-api cannot get a capture from a node, which causes can
the caller tell apart: a timeout, a reset, and a reply that is not
HTTP? Each needs its own detail, and its status follows from whether a
retry can succeed: a 503 when it can, and a 502 when it cannot.
