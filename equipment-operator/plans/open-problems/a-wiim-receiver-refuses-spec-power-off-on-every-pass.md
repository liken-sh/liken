# A WiiM receiver refuses spec.power Off on every pass

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test starts a fake WiiM, calls `setPower` with `Standby` twice, as
two passes do, and counts the log lines that hold the refusal. It
wants one. It failed with two identical lines:

```
after two passes the log has 2 refusals, want 1: ["Receiver studio: generation 0 requests power Standby; sent power Standby; the command was not applied: a WiiM has no standby command, so it cannot power off" ...]
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

A WiiM has no standby, so a `Receiver` with a `wiim` block and
`spec.power` set to `Standby` or `Off` logs the same refusal on every
pass, at least every 30 seconds.

- Does the API server refuse `Standby` and `Off` for a WiiM when you
  apply the `Receiver`, with a CEL rule?
- Or does the operator accept the value, send nothing, and report the
  refusal once, for example with a send budget as the settings have
  and a condition?
