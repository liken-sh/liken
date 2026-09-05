# Firmware that loses boot order across reset

Open problem. Some UEFI firmware does not preserve `BootOrder` across a
reset, even when a write succeeds and reads back correctly. On these
machines, a failed release trial can boot repeatedly instead of returning
to the proven slot.

## Evidence

The recorded test on 2026-08-26 showed this behavior. Before reboot,
`init` wrote slot B first in `BootOrder` and confirmed the value. The
machine nevertheless booted slot A. On the next boot, `init` printed
`BootOrder now leads with Boot0003`, which it prints only when changing
the order. The previous write had not persisted. `BootNext` survived
every reset tested on that machine that day.

The current shutdown path also sets `BootNext` to the proven slot.
This protects ordinary reboots on that firmware. The relevant code is
`assertAndArmForReboot` in [proving.go](../../init/proving.go) and
`healBootEntries` in [bootentries.go](../../init/bootentries.go).

## Why release trials remain unsafe

A trial needs `BootNext` to select the new slot once. After the firmware
consumes that one-shot setting, subsequent resets depend on `BootOrder`
to select the proven slot.

Before arming a trial, `armProvingBoot` calls `fallbackInPlace` to assert
and read back the proven preference. This check cannot detect a value
that changes only across reset. If the restored order selects the trial
slot, a kernel panic followed by the `panic=10` reset can repeat the
same failed boot indefinitely.

The proving watchdog runs inside `init`. It cannot interrupt a loop in
which the kernel fails before starting `init`.

## Proposed safeguard

Record the last asserted `BootOrder` on `machineState`. On the next
boot, compare the actual order with that record before repairing it.
An unexpected difference would block automatic trial arming and report
that fallback persistence has not been established.

A mismatch establishes that the recorded order did not survive that
reset. It does not identify the cause by itself: firmware behavior and a
person editing the boot menu both need consideration. A successful
comparison also cannot guarantee that future resets will preserve the
order.

The original proposal would clear the refusal after a later matching
boot. That remains an option, but one successful reset is not enough
evidence to make that recovery policy automatic without discussion.

## Remedy scope

**A focused detection safeguard, with a recovery-policy decision.**
Recording and comparing boot preferences is internal reliability work.
Refusing a trial after a known mismatch protects the existing fallback
promise without changing the release format.

The design decisions are how to treat machines with no observation yet,
when to clear a refusal, and whether an operator may override it. Those
choices affect unattended upgrades and the recovery a user can expect
from firmware that does not preserve the fallback preference.

## Verification needed

- Simulate firmware that accepts writes but restores an older order on
  reset. Detect the mismatch before repairing the order and refuse a trial.
- Exercise missing or damaged observation records and deliberate boot-menu
  changes under the chosen policy.
- Verify recovery after a firmware update, including when a refusal clears.
- Test on affected hardware. A correct same-boot readback cannot prove
  persistence across reset.
