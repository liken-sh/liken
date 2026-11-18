# The index watch discards a failed read

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`follow` in `solver/indexwatch.go` returns nil when a read of the
inotify file fails, for any reason. `run` then closes the watch,
calls `open`, which opens a new watch and reads the whole index
directory again, and calls `follow` again. The error is not logged,
and the loop has no pause. If the read fails every time, for example
because of a fault of the kernel or of the file system, the solver
opens a watch and reads every index file's header in a loop, and
nothing in the log says why.

The comment on `follow` states that a failed read reopens the watch on
purpose, because `run` ends a waiting read by closing the file when
`ctx` ends. `run` already checks `ctx.Err()` after `follow` returns,
so a read that fails while `ctx` is live is a different case.

## Evidence

- `solver/indexwatch.go`, `follow`: `if err != nil { return nil }`
  after `watch.read()`.
- `solver/indexwatch.go`, `run`: no wait between one `open` and the
  next.

## What would confirm it

A test that gives `follow` an `inotify` whose file is closed before
the read, with a live `ctx`, and counts the calls to `open` within a
fixed time of a `synctest` bubble.

## What a fix has to decide

- Whether `follow` returns the error of a read that fails while `ctx`
  is live, so the solver stops and the pod restarts, or logs it and
  reopens after a backoff.
