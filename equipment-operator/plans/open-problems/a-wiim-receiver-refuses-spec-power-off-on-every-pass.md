# A WiiM receiver refuses spec.power Off on every pass

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When a `Receiver` with a `wiim` block has `spec.power` `Standby` or
`Off`, `receiverUnit.setPower` in `reconcile.go` calls
`wiim.Client.SetPower(MainZone, false)`. That call always returns the
error "a WiiM has no standby command, so it cannot power off"
(`wiim/client.go`). `setPower` logs the refusal and returns before
`applyPower`, so the unit never records the value as settled. A WiiM
always reports power `On` (`wiim/state.go`, `Status.zone`), so the
next pass finds the same difference and logs the same refusal again.
`controller.run` runs a pass on each wake and at least every
`backstopInterval`, 30 seconds. The `Receiver` CRD accepts `Standby`
and `Off` for a WiiM, because no CEL rule ties `spec.power` to the
protocol block.

## Evidence

- `TestARefusedSpecPowerLogsTheDriversError` in
  `receiver_lines_test.go` shows one call of `setPower(PowerStandby)`
  on a WiiM writing the refusal line. No test runs a second pass.
- `setPower` has no send budget, unlike the settings, which stop after
  `sendLimit` sends (`send_budget.go`).

## What would confirm it

A test that sets `spec.power` `Off` on a WiiM `Receiver`, runs two
passes, and counts the refusal lines in the receiver log.

## What a fix has to decide

- Whether the CRD refuses `Standby` and `Off` for a `wiim` block, or
  the operator accepts them and records them as settled with nothing
  sent.
- Whether a refused power command gets a send budget, as a setting
  does, and a condition that reports the refusal.
