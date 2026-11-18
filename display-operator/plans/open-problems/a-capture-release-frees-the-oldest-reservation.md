# A capture's release frees the oldest reservation

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The capture sidecar limits each connector to `CAPTURE_PER_OUTPUT`
captures at a time. `captureServer.take` in `capture.go` appends a
`runningCapture` with the capture's end time to `s.running[connector]`,
and the function that it returns calls `release(connector)`. `release`
always removes `held[0]`, the reservation that was taken first, and not
the reservation of the capture that ended. The count stays correct, but
the list keeps the end time of a capture that already finished.

## Evidence

- `take` writes the busy 503 from `held[0].ends`: "%s is being
  captured until <time>".
- `release` takes only the connector and runs
  `s.running[connector] = held[1:]`.

So with `CAPTURE_PER_OUTPUT` of 2, a stream with no end is taken
first and a clip that ends at 10:05 second. When the clip ends,
`release` removes the stream's entry and keeps the clip's. When a new
capture fills the free slot, the next request gets a 503 that says
"until 10:05", a time that is already past, while the stream that
holds the slot has no end.

## What would confirm it

A test in `capture_test.go` with `perOutput` of 2: take a capture with
an end time, take one with none, release the second, fill the slot
again, and read the 503 detail.

## What a fix has to decide

`take` can return a release that removes its own entry, by an id or by
the entry's index at the time of the take. The detail says "the end
of the oldest"; the fix also decides whether the 503 should give the
earliest end of the running captures, which is when a slot frees.
