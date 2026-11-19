# A solver test waits on the wall clock

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: `solver/engine_test.go:75` reads `time.Now()` and line 77
compares `time.Since(started)` with 5 seconds, outside a `synctest`
bubble. A unit test cannot show a failure that depends on the load of
the machine. A run of the package under load can.

`TestAnEngineThatHangsIsStoppedAtTheTimeout` in
`solver/engine_test.go` sets `Engine.Timeout` to 200 ms, runs the
fake engine, and fails when `Solve` takes more than 5 s of real time.
The fake engine is the test binary itself, `os.Args[0]`, started as a
child process by `fakeEngineAt` in `solver/fakeengine_test.go`, and
`Solve` stops it through `context.WithTimeout` in `solver/engine.go`.
The test runs outside a `synctest` bubble, so both the 200 ms timeout
and the 5 s bound are wall-clock time.

The section "Tests run on the Go toolchain alone" in the root
`AGENTS.md` says that a test waits on the fake clock of a `synctest`
bubble, never on real time. On a slow CI runner, the start of a
process and its stop can take longer than the bound, and the test
fails for a reason that is not in the code.

## Evidence

The test's body, lines 72 to 80 of `solver/engine_test.go`, uses
`time.Now` and `time.Since` and calls no `synctest` function. No
other test in `solver/`, `worker/`, or `promote/` compares wall-clock
times.

## What would confirm it

Run the test under heavy load, for example with `-count=100` and
`-cpu` set above the number of cores, and count the failures.

## What a fix has to decide

- Whether `Engine` takes a function that runs the program, so a test
  can give it a fake that blocks inside a `synctest` bubble with no
  child process.
- Or whether the timeout is checked by a test of the `context` that
  `Solve` passes to the program, and no test waits for a process to
  stop.
