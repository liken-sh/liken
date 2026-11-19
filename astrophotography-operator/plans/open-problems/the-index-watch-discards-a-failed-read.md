# The index watch discards a failed read

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test opens a real watch on an index directory in `t.TempDir()`,
closes the watch's file, and calls `follow` on it, as a read that
fails while `ctx` is live. It wants the read's error, either returned
or in the log. It failed with:

```
follow returned no error and logged nothing for a read of a closed watch, want the read's error in one of them
```

The test runs outside a `synctest` bubble, because a goroutine that
waits on a real inotify file is not durably blocked. It is not in the
tree, because a failing test cannot land on `main`.

## What a fix has to decide

When the solver cannot read its watch of the index directory, it
opens the watch again and reads every index file, with no pause and
no log line.

- Does the solver stop, so that Kubernetes restarts its pod?
- Or does it log the error and open the watch again after a pause?
