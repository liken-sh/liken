# An empty capture logs "Wait was already called"

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When ffmpeg exits with status 0 and writes no bytes, `stream` in
`capturestream.go` ends the response with `encoder.failure()`, so the
log gets ffmpeg's last stderr line. But `stream` already called
`encoder.wait()` a few lines before, and `failure` calls `wait` again.
`exec.Cmd.Wait` returns the error "exec: Wait was already called" on a
second call. So the log line gives that error and not ffmpeg's stderr,
and the component's rule that an error includes its source's text is
broken for this case.

## Evidence

- `capturestream.go`, `stream`: `if err := encoder.wait(); ...` runs
  first, then `if written == 0 ... encoder.failure()`.
- `encoders.go`, `failure`: `if err := e.wait(); err != nil { return
  err }`, and only then the "exited with no error and wrote no bytes"
  error.
- `encoders.go`, `wait`: calls `e.cmd.Wait()` each time.

## What would confirm it

A test in `capturestream_test.go` with a fake ffmpeg that exits 0 with
no output and one line on stderr, which checks the logged error for
that line.

## What a fix has to decide

`encoder` can keep the result of its first `Wait` and return it on
each later call, or `stream` can pass the result it already has to
`failure`. The first also covers `end`, which calls `cmd.Wait` too.
