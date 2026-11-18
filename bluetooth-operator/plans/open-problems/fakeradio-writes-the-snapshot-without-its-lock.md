# `fakeRadio` writes the snapshot without its lock

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The comment on `fakeRadio.mu` in `radio_test.go` says the mutex guards
every field of the fake, "because Connect runs on its own goroutine
while the pass reads the snapshot". Five methods do not take it:

- `SetAdapterAlias` and `SetAdapterConnectable` write
  `r.snapshot.Adapter`.
- `OpenWindow` reads `r.windowErr` and writes three fields of
  `r.snapshot.Adapter`.
- `CloseWindow` writes three fields of `r.snapshot.Adapter`.
- `Pair` reads `r.pairErr`.

`Snapshot`, `update`, `Connect`, `Disconnect`, `SetAdapterPowered`,
and `Remove` take the lock.

## Evidence

`make test-go` runs the tests with `-race`, and it passed on
2026-11-18. The connector's goroutine that calls `Connect` writes only
`r.snapshot.Devices`, through `update` and under the lock, and the
five methods run on the test's own goroutine. So no test runs two
of them at once on the same field today. A test that calls
`Snapshot` from a `Connect` goroutine while a pass opens a pairing
window would race on `r.snapshot.Adapter`.

## What would confirm it

A test under `-race` that blocks a `Connect` through `connectBlocks`,
runs a pass that opens or closes a pairing window, and reads
`Snapshot` from another goroutine.

## What a fix has to decide

Whether every method of the fake takes `mu`, as the comment says, or
whether the comment states which fields the lock guards.
