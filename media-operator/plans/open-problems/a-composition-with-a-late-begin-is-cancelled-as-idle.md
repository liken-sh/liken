# A composition with a late begin is cancelled as idle

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`mux` in `apicompose.go` creates one pipe for each upstream body and
sets the body's idle mark to the time of the pipe's creation.
`watchIdle` does not check the marks before the begin time of the
`t=` span, and then cancels the composition when any mark is older
than `upstreamIdleTimeout`, 30 seconds. Each upstream gets the span
with `composeLeadIn`, 1 second, added, so its first body byte arrives
about 1 second after the begin time.

So for a request whose begin is more than 30 seconds ahead, the first
tick of `watchIdle` after the begin finds marks that are as old as the
begin offset and cancels the composition before any upstream sends a
byte.

The comment on `upstreamIdleTimeout` in `apiupstream.go` says the
count starts "from the first body byte or from begin, whichever is
later". The code does neither. The comment on `watchIdle` describes
what the code does.

## Evidence

- `apicompose.go`, `mux`: `mark.Store(s.clock().UnixNano())` at the
  creation of each pipe. `copyToPipe` updates the mark only when a read
  returns bytes.
- `apicompose.go`, `watchIdle`: `floor` is the clock plus `query.Begin`,
  and each tick at or after `floor` compares every mark with
  `upstreamIdleTimeout`.
- `apicompose.go`: `displayTarget` and `audioTarget` take
  `composeLeadIn`.

## The reproduction

A test in a `synctest` bubble sets one mark at the bubble's start, as
`mux` does when it creates a pipe, and calls `watchIdle` with `t=60`.
It lets the clock run to 61 seconds, before a sibling would send its
first byte, and asserts that the composition's context is not
cancelled. It failed with:

```
the composition with t=60 was cancelled as idle at 61 seconds, want it running until a sibling has had upstreamIdleTimeout to send its first byte
```

The test is not in the tree, because a failing test cannot land on
`main`. It calls `watchIdle` directly and does not run `mux`, because
`mux` runs ffmpeg and reads its output from an OS pipe, which stops
the bubble's clock.

## What a fix has to decide

A recording or a stream that you ask to start more than 30 seconds
ahead is cut off at its start time.

Where the idle count starts: at the begin time plus `composeLeadIn`, at
the first body byte, or at the later of the two, which is what the
`upstreamIdleTimeout` comment states. The `upstreamIdleTimeout` and
`watchIdle` comments change with the rule.
