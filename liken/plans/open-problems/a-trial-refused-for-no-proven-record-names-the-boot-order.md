# A trial refused for no proven record names the boot order

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When the machine has no proven release record, or the record does not
parse, the reboot path does not arm a staged trial, and the console
line says that the default boot order does not read back with the
proven slot first. The boot order was not checked, so the line names
the wrong cause.

## The evidence

`armProvingBoot` (`init/proving.go:132`) prints that line when
`fallbackInPlace` returns false (`init/proving.go:173`).
`fallbackInPlace` (`init/proving.go:216`) returns false at once when
`provenRecord` returns nil, before it calls `assertProven` or
`fallbackLeads`. `provenRecord` (`init/proving.go:347`) returns nil
with no line for a missing record, and with a line for a record that
cannot be read or parsed.

## What would confirm it

A unit test that calls `armProvingBoot` with a staged record on the
other slot and no proven record, with a fake actuator that accepts the
trial, and reads stderr.

## What a fix has to decide

Whether a machine with no proven record can arm a trial at all. If it
cannot, `armProvingBoot` checks for the record before
`fallbackInPlace` and prints that reason. If it can, `fallbackInPlace`
needs a slot to put first when no record names one.
