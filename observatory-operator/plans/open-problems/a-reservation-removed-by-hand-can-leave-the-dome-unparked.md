# A reservation removed by hand can leave the dome unparked

Open design question, recorded on 2026-11-19. A review of the
`StopSite` rule in `sitestop.go` found it by reading the code. A unit
test of one remedy showed why that remedy does not work.

When a person removes the finalizer of a `Reservation` in the middle
of its deactivation, the `Reservation` leaves the store while its
runner still holds its `Telescope`. The runner releases the telescope
and runs no more steps. If the `Deactivation` step had not reached
the dome's park procedure, the dome is not parked. The reservation of
another telescope of the same `Observatory` then reaches `StopSite`,
finds no other telescope in use, disconnects the observatory's
devices, and switches off their outputs. A dome that loses its power
while it is open cannot be closed until a person restores the power.

The operator does this only after a person removes a finalizer by
hand. A `Reservation` that a person deletes keeps its finalizer until
its last step ends, so a delete alone does not cause it.

## The evidence

- `siteStopFor` (`sitestop.go`) ignores a held telescope that reads
  `Idle`, and a telescope reads `Idle` when its reservation is gone
  from the store.
- `claims.forgetGone` runs in `supervise` on the same change of the
  store, and it releases the telescope of a reservation that is gone.
  After it runs, the telescope is not held, and `siteStopFor` does
  not read it.
- The comment on `stopSite` (`deactivation.go`) says that the step
  does not park the dome, because the `Deactivation` step already ran
  the dome's park procedure. That is not true for a reservation that
  left the store before that step ended.

The `StopSite` step behaved the same way before the rule of
`sitestop.go` was built: after `forgetGone` released the telescope,
the other reservation found no telescope held and stopped the
observatory.

## A remedy that does not work

On 2026-11-19, `siteStopFor` was changed to skip the step when a held
telescope of the observatory read `Idle`. A unit test in a `synctest`
bubble removed the first reservation from the store while the second
waited, and asserted the skip. It failed in 23 of 30 runs: in those
runs `forgetGone` released the telescope before the waiting runner
read the tree again, so the runner found no held telescope and
stopped the observatory. The change was taken out.

## The design question

This needs the maintainer's decision before a fix is built.

**How the operator learns that a dome may not be parked.** The fact
has to outlast the reservation and the claim, and the operator keeps
no state in memory that the API does not hold.

- The `StopSite` step can read the dome: when the dome's device
  reports that it is not parked, the step skips and says so, and the
  outputs stay on. This reads the hardware and needs no record, but
  it needs the dome connected at that moment.
- `forgetGone` can record on the `Observatory` that a reservation
  left in the middle of its deactivation, and `StopSite` can skip
  while the record is there. A person clears the record after they
  check the dome.
- The step can run the dome's park procedure itself before it
  switches off the outputs, when no reservation ran it.

A second question follows from the first: when the step skips for
this reason, the observatory's server and outputs stay on until a
later reservation ends. Whether the operator reports that on the
`Observatory`, as a condition, is part of the same decision.
