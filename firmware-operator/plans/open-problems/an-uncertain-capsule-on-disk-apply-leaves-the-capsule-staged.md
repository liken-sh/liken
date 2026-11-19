# An `Uncertain` capsule-on-disk apply leaves the capsule staged

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

When a capsule apply ends `Uncertain`, its message says "the reboot
keeps liken's BootNext and the capsule does not run". That reason is
true in NVRAM mode, where fwupd's updater runs only from the
`BootNext` that fwupd sets. In capsule-on-disk mode, fwupd writes the
capsule to `\EFI\UpdateCapsule` on the ESP and sets the
file-capsule-delivery bit, `0x4`, in `OsIndications`. The firmware
then reads the file on the next boot, and `BootNext` does not take
part. No code removes the file or clears the bit after an `Uncertain`
result, so the reboot of the turn can still install the capsule. In
the cases where fwupd's writes differ from the rehearsal's, that is a
write that no rehearsal passed.

## Evidence

- `stageCapsule` in `scan/capsule.go` returns `resultUncertain` in
  six places after the install, four of them with the message above.
  It leaves the variable store and the ESP as fwupd wrote them.
- The `Written` result in the same function reads "the firmware reads
  it from \EFI\UpdateCapsule" when `BootNext` did not change, so the
  code already treats on-disk delivery as independent of `BootNext`.
- `judgeCapsule` in `scan/capsule.go` accepts an `OsIndications` write
  that adds only `fileCapsuleDelivery`.
- `keepsBootNext` in `apply.go` is false after `Uncertain`, which
  changes only `spec.keepBootNext` of the `Disruption`.
- `updateESPs` in `liken/init/efiactuator.go` calls
  `removeStagedCapsules` only for the slot that is not proven. The
  apply pod writes the ESP of the proven slot, which is the one the
  next boot reads.
- Plan 08 drilled capsule-on-disk on `node-5` of the `lab` fleet with
  a `Written` result. No drill of an `Uncertain` result in that mode
  is recorded.

## The reproduction

A test of the apply in `scan/` lets the fake `fwupdtool` stage a
capsule on disk: it writes `OsIndications` with bit `0x4` and
`EFI/UpdateCapsule/fwupd-9c13b7f1.cap` on the ESP, and then fails. It
wants no staged capsule on the ESP after an `Uncertain` result. It
failed with:

```
after the result Uncertain "fwupdtool install: fwupdtool: failed to write, after it wrote to the machine, so the reboot keeps liken's BootNext and the capsule does not run" the ESP still holds the staged capsule, want it removed, because the firmware installs it on the next boot
```

The message of that result gives the reason of NVRAM mode. The test is not in the tree, because a failing test cannot land on
`main`.

## What a lab run would add

The test shows that the file and the bit stay. Whether the firmware
then installs the capsule is a fact of the firmware. On the `lab` fleet, with the capsule-on-disk build of the lab
firmware: approve a capsule, make the apply end `Uncertain` after
fwupd staged the file, for example by a failure of `repairUpdater`,
and read the firmware's log and the next scan after the reboot. The
capsule runs if the scan reads the new version.

## What a fix has to decide

- After an `Uncertain` apply in capsule-on-disk mode, which program
  removes the staged capsule and clears the `OsIndications` request
  before the reboot: the apply pod, or `init` when
  `spec.keepBootNext` is false? A change in `init` is in the `liken`
  component.
- Whether that cleanup is part of plan 09, which removes fwupd's boot
  entry and updater after a capsule.
- What the `Uncertain` messages say in each mode.
