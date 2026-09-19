# 10, A capture body ends cleanly

Plan 10. Built on 2026-09-19.

A tap decides its body is complete from the body itself, so stopping
the recorder can never mark a delivered body as cut. This closes the
open problem "The capture stream tests flake in CI", which plan 09
left behind.

## The problem

A tap streams `pw-record`'s samples into an HTTP response. It ends in
one of two ways. The span it asked for runs out, or the client stops
reading. Anything else is the pipeline dying under the tap, and the
client has to be told: the handler ends the body without its
terminating chunk, which an HTTP/1.1 client reads as `unexpected EOF`
and an HTTP/2 client reads as an `INTERNAL_ERROR` reset. The handler
calls this end "cut".

Three tests were skipped on 2026-09-17 because CI saw that `unexpected
EOF`. The release run was retried three times (`gh run 35294266829` on
`de93ba67`), and each attempt failed a different test:
`TestAFinishedWAVSpanEndsCleanlyOverHTTP2` on the first,
`TestAFinishedSpanEndsCleanly` and
`TestATapRunsPwRecordWithTheLineThePlanStates` on the second, and
`TestAClientThatHungUpEndsCleanly` on the third. The last of those was
never skipped and stayed flaky.

There were two races behind the three symptoms.

**The recorder's death decided the body.** At the end of a tap the
handler closed the sample pipe and only then signalled `pw-record`.
Closing the pipe first sends SIGPIPE to a recorder blocked on a write.
The fake `pw-record` runs under `set -e`, so the child's SIGPIPE ended
the shell with an exit status of 141, and the handler counted any
status above zero as a capture failure. It marked the body cut after
the client had every byte: the failing log lines read
`bytes=96044 ... ended=pw-record:141 truncated`, where 96044 is the
whole body. A real `pw-record` dies of the signal and reports -1,
which the code already read as clean, so on a node the race was a
teardown smell; in CI it was a failure.

**The read error beat the write error.** An open span has no bound, so
`delivered()` is always false and the body is complete only when the
client is known to have left. The handler recognized that from the
write error alone. A client hangup also cancels the request context,
which kills `pw-record`, and the pump's read error can reach the copy
before the failed write does. When it did,
`TestAClientThatHungUpEndsCleanly` read the hangup as a truncation.

## The design

`tap.go` gains two small functions and one method, and the end of a
tap reads from three inputs.

```go
func endedByClient(copyErr error, requestDone bool) bool {
	return requestDone || errors.Is(copyErr, errClientGone)
}

func bodyCut(delivered, left, encoderFailed bool) bool {
	return (!delivered && !left) || encoderFailed
}

func (e tapExit) encoderFailed() bool { return e.Encoder > 0 }
```

`endedByClient` names the client's departure from either signal: the
write that ended in `errClientGone`, or the request's own context being
done when the read side failed first. `bodyCut` says a body is
complete when the span ran out or the client left. The recorder's exit
status is deliberately not consulted. A recorder that died before the
end already shows as a span that was not delivered, and one that died
after the end cannot take back bytes the client holds. The encoder is
the one process whose status still cuts a delivered body, because an
encoder can consume every sample and fail to write its last frame.

The handler also stops the processes before it closes their pipes, so
the container's own teardown does not arrive as the process's failure.
The order is the one a Unix program keeps: signal the child, then
close the channel.

The fake `pw-record` no longer turns a child's SIGPIPE into an exit
status of its own. Its loop keeps running when `cat` dies, so the
stand-in dies the way a real recorder does and lets the container
signal it.

`stream` ends with the decision in one line:

```go
cut := bodyCut(tap.delivered(),
	endedByClient(copyErr, r.Context().Err() != nil),
	exit.encoderFailed())
```

## What was considered and set aside

- **Keeping the recorder's status as a second failure signal, and
  fixing only the close-before-kill order.** The order still races the
  signal against the pipe close, and the exit status says nothing
  about a body the client already holds. The status is kept for the
  `audio_capture_failures_total{reason="encoder"}` counter, where a
  process that died on its own is still a failure worth counting.
- **The encoder's status too, so `cut` depends on the body alone.** An
  encoder that consumes a whole span and fails to write its last frame
  leaves a clean EOF and a delivered flag set, so its failure would go
  unreported.
- **Treating exit status 141 as a clean end.** The number is a shell's,
  not Go's: a process killed by a signal reports -1. It would hide a
  real recorder crash that also reports 141.

## How the work is proved

- Two table tests cover the decision without timing: every combination
  of a delivered or open span, a client that left or a pipeline that
  died, and an encoder that failed or did not. One test covers the two
  ways a departure is seen.
- The three `t.Skip` calls are gone, and the fourth test that CI failed
  is covered by the same change.
- On this workstation, with forty busy loops saturating 22 cores,
  `go test -race -count=400` of the four tests ran clean. Before the
  change the same recipe failed the hangup test on most iterations and
  produced `ended=pw-record:141` on six of eight hundred finished-span
  taps, one per failure.
- `go test -race ./...` passes.
