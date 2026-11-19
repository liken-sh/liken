# The busy detail names the oldest capture and not the next free slot

Open problem, recorded on 2026-11-19. It comes from a reading of the
code, and no test reproduces it.

The capture sidecar runs at most `CAPTURE_PER_OUTPUT` captures of one
connector at a time. When the connector is full, `take` in
`capture.go` refuses the next request with a 503 of the kind
`capture-busy`, and its detail says how long the caller waits:
"HDMI-A-1 is being captured until <time>", or "until its client
closes" for a stream with no end. The detail gives the end of the
oldest running capture, `held[0]` at `capture.go:316`.

With `CAPTURE_PER_OUTPUT` of 2 or more, the oldest capture is not
always the one that ends first. When a stream with no end runs first
and a clip that ends at 10:05 runs second, the detail says "until its
client closes", and a slot frees at 10:05. A caller that reads the
detail waits longer than it has to, or gives up.

## What a fix has to decide

Should the busy 503 give the time when the next slot frees, which is
the earliest end among the running captures, or keep the end of the
oldest capture? The earliest end is the time a retry can succeed. A
stream with no end has no time to compare, so the detail says "until
its client closes" only when no running capture has an end. The
change alters the detail that a caller reads, not its status or its
problem kind.
