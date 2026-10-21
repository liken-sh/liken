# 08, Applying a capsule

Proposed on 2026-10-21. Not built. This plan applies an approved
board firmware update, a capsule, inside a reboot turn, and proves it
from the next scan. It follows plans
[06](06-applying-a-database-update.md), which applies a database
update in a turn, and [07](07-rehearsing-a-capsule.md), which
rehearses a capsule. It uses every capability that `liken`'s
[milestone 33](../../liken/plans/33-firmware-updates.md) offers: the
turn of [milestone 88](../../liken/plans/88-a-component-takes-a-reboot-turn.md),
the one-shot boot of the turn's reboot, a boot order that survives a
reset, and the ESP of the next boot.

## The problem

A capsule writes the variables that `init` keeps for release trials.
fwupd sets `BootNext` to its updater, and `init` pins `BootNext` at
the proven slot before every reboot, so today the capsule never
applies. The firmware then applies the capsule before any kernel
loads, and a failure there has no slot to fall back to.

## The steps

1. **Rehearse.** Plan 07.
2. **Ask for a turn.** As in plan 06.
3. **Write.** After the drain, the apply pod runs `fwupdtool install`
   against the real variable store and the ESP of the next boot. Unlike
   a database update, the writes are several, and fwupd makes them in
   its own order, so the pod runs fwupd instead of replaying the
   rehearsal's record. It then compares what fwupd wrote with the
   rehearsal's record. A difference fails the apply, and the operator
   releases the turn with no handover, so `init` pins `BootNext` at
   the proven slot and the capsule does not apply.
4. **Release the turn with the handover.** The operator tells the OS
   that it set `BootNext` for this reboot. The firmware boots the
   updater, applies the capsule, and resets. The next boot starts the
   proven slot, and `init` restores its own `BootNext`.
5. **Prove.** The next scan reads the version from the ESRT through
   fwupd. The capsule status variable records the firmware's own
   result, and the operator records it beside the version. fwupd can
   report a failure for an update that applied
   ([fwupd#11119](https://github.com/fwupd/fwupd/issues/11119)), so
   the version decides.
6. **Clean up.** A later turn, or the next one the machine takes,
   removes the "Linux Firmware Updater" boot entry and the files
   under `\EFI\fwupd` and `\EFI\UpdateCapsule` on the slot.

## Preconditions

A firmware update cannot be undone, so the operator checks more than
for a database update before it asks for a turn:

- The board's battery state, where fwupd reports one. fwupd refuses
  some capsules on battery.
- The ESP's free space against the rehearsal's files.
- No capsule pending on the machine from an earlier attempt.

`init` refuses the handover when the machine has no way back, under
milestone 33's rules, and the operator reads that refusal on the
turn.

## The risk the manual states

An update can turn Secure Boot on or reset its keys. `liken`'s
`vmlinuz` and the default-path loader are unsigned, so the machine
then boots neither, and no boot order repairs it. The guide that
approves a capsule states this before the approval, until the
hardening tier signs `liken`'s boot chain.

## The proof

Under OVMF built with `FmpDevicePkg` and `EsrtFmpDxe` first, with a
test capsule. Then one real board update on a testbed machine that a
catalog offers one, after plan 07's rehearsal of it.

## Open questions

- Capsule-on-disk against NVRAM mode. Capsule-on-disk writes only
  `OsIndications` and a file, so it needs no handover of `BootNext`,
  but fwupd calls that path uncommon.
- How long the firmware's update may take before the machine's
  `Ready` counts as late, and what the turn's deadline is for it.
- Whether the cleanup of step 6 needs its own turn, or rides the next
  turn the machine takes for any reason.
