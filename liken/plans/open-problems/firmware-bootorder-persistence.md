# Firmware that loses boot order across reset

Open problem. Some UEFI firmware does not keep `BootOrder` across a
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
consumes that one-shot setting, each later reset depends on `BootOrder`
to select the proven slot.

Before arming a trial, `armProvingBoot` calls `fallbackInPlace` to set
the proven preference and read it back. This check cannot detect a value
that changes only across reset. If the restored order selects the trial
slot, a kernel panic followed by the `panic=10` reset can repeat the
same failed boot indefinitely.

The proving watchdog runs inside `init`. It cannot interrupt a loop in
which the kernel fails before starting `init`.

## Detection, as built

Since 2026-10-22, `init` reads at power-on, before it writes, whether
the proven slot leads `BootOrder`, and reports it as
`status.firmware.bootOrderKept`
([milestone 33, as built](../33-firmware-updates.md#as-built)). Every
shutdown asserts that order, so `false` is the mismatch this problem
describes, with no record kept. Only the handover of `BootNext` to a
`Disruption`'s requester reads it. Release trials still arm as before,
so the policy decisions below remain open.

## The first readings on liken-1

On 2026-10-23 liken-1, the testbed's mini PC, read
`bootOrderKept: false` after a release trial and `true` after a plain
reboot. Before the trial's reset, `BootOrder` led with `Boot0003`, the
proven slot B, and `BootNext` named `Boot0001`, the trial slot A. At
power-on, slot B no longer led. Before the plain reboot, `BootNext`
named the proven slot's entry, which already led `BootOrder`, and the
order was kept. stick-1 read `true` after a trial of its own.

The two readings fit a firmware that moves the entry it boots from
`BootNext` to the head of `BootOrder`. They also fit a firmware that
loses the order at a reset that follows a one-shot to another entry.
`init` does not record the order it read at power-on, so the readings
cannot tell the two apart.

A firmware that moves the `BootNext` entry to the head matters for a
capsule. fwupd's updater entry would lead `BootOrder` after the
updater runs, so the boot after the update would start the updater
again, not the proven slot. The handover reads `bootOrderKept` from
the boot before it, which was a plain boot, so it reads `true` and
does not refuse. Recording the power-on order, as the safeguard below
proposes, would tell the two causes apart.

## Proposed safeguard

Record the last asserted `BootOrder` on `machineState`. On the next boot,
compare the actual order with that record before repairing it. An
unexpected difference would block automatic trial arming. It would also
report that nobody knows whether this firmware keeps the fallback order
across a reset.

A mismatch shows that the recorded order did not survive that reset. It
does not show the cause: the firmware and a person editing the boot menu
are both possible. A successful comparison also cannot guarantee that
future resets will keep the order.

The original proposal would clear the refusal after a later matching
boot. That remains an option. One successful reset is not enough
evidence to make that recovery automatic without discussion.

## Remedy scope

The fix is a focused detection safeguard, plus a decision about recovery
policy. Recording and comparing boot preferences is internal reliability
work. Refusing a trial after a known mismatch keeps the existing
fallback to the proven slot working, and the release format does not
change.

The design decisions are how to treat machines with no observation yet,
when to clear a refusal, and whether an operator may override it. Those
choices affect unattended upgrades. They also decide what recovery a
user gets from firmware that does not keep the fallback preference.

## Verification needed

- Simulate firmware that accepts writes but restores an older order on
  reset. Detect the mismatch before repairing the order and refuse a trial.
- Exercise missing or damaged observation records and deliberate boot-menu
  changes under the chosen policy.
- Verify recovery after a firmware update, including when a refusal clears.
- Test on affected hardware. A correct readback in the same boot does not
  show that the value survives a reset.
