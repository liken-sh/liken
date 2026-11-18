# The observatory's phase and `StopSite` count different telescopes

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`observatoryStatus` in `statustree.go` sets an `Observatory` to
`Deactivating` while a reservation of one of its telescopes
deactivates and no telescope in it is `Ready`, `Activating`, or in
`Error`. Its comment says that the `StopSite` step then disconnects
the observatory's devices. `stopSite` in `deactivation.go` uses a
different test: it skips with "in use by Reservation ..." when any
other telescope of the same observatory has a holder in `claims`,
and a telescope that is `Deactivating` still has its holder.

So when two telescopes of one observatory deactivate at the same
time, the status says `Deactivating`, and each `StopSite` can find
the other telescope held and skip. `claims.release` runs only after
the last deactivation step (`finish.go`, `reservation.go`), and
`siteLock` runs the two `StopSite` steps one after the other, so
both steps can skip. Then no step disconnects the observatory's
devices in `siteOrder` or switches off their outputs. Plan 14 records
that a skipped `StopSite` leaves the observatory's server to the
sweep.

## Evidence

- `statustree.go`, `observatoryStatus`: only `PhaseReady`,
  `PhaseActivating`, and `PhaseError` set `keeping`.
- `deactivation.go`, `stopSite`: the loop over `r.o.claims.held()`
  skips for any other held telescope of the observatory, whatever its
  phase.
- `finish.go` line 64 and `reservation.go` line 78: the only calls to
  `claims.release`.

## What would confirm it

A test with one `Observatory`, two `Telescope`s, and a reservation
of each, both `Ready`. End both reservations at the same time. Check
the `StopSite` record of each reservation, and check whether the
observatory's devices were disconnected and its outputs switched off
before its server stopped.

## What a fix has to decide

- Whether a telescope whose reservation is deactivating still keeps
  the observatory, and so which of the two tests is correct.
- When two reservations deactivate together, which one runs
  `StopSite`, so that exactly one of them stops the observatory.
