# A composition with a late begin is cancelled as idle

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

A test in a `synctest` bubble that composes with `t=60` against
siblings that send their first byte at 61 seconds. The composition is
cancelled at about 60 seconds.

## What a fix has to decide

Where the idle count starts: at the begin time plus `composeLeadIn`, at
the first body byte, or at the later of the two, which is what the
`upstreamIdleTimeout` comment states. The `upstreamIdleTimeout` and
`watchIdle` comments change with the rule.
