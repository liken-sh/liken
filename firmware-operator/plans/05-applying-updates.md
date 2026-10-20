# 05, Applying updates

Proposed on 2026-10-20, as a stub. Not built. This plan applies the
version that [plan 04](04-approval.md) approves, on one machine at a
time. The parts that write the boot chain stay in the OS, in `liken`'s
[milestone 33](../../liken/plans/33-firmware-updates.md), which also
lists what the OS must build first.

## The steps

1. **Stage.** When a machine's device runs another version than the
   approved one, the operator asks the OS for a rollout turn for that
   machine, through the interface that milestone 33 adds.
2. **Apply.** The rollout conductor grants the turn and drains the
   machine. The operator downloads the cabinet, checks its SHA-256,
   and runs fwupd in a pod that writes. A UEFI capsule and a `dbx`
   update go through `init`'s efivars shim, which stages the capsule in
   the right boot slot's ESP, and the reboot applies it. A device that
   fwupd flashes with no reboot, such as a dock, is flashed only when
   no claim holds the device.
3. **Prove.** The ESRT or the next scan of plan 03 shows the approved
   version, and the machine joins its cluster again. A failed flash
   has no earlier state to return to, so the operator records it in
   status with an `Event`, and nothing runs it again on its own.

## Questions for the full plan

- **The turn interface.** Milestone 33 has to give a component a way to
  request a rollout turn for a machine and to see it granted.
- **Which ESP.** A `liken` machine has two boot slots, each an ESP.
  The shim has to choose the slot the capsule goes into, and keep the
  release trials of `init` working.
- **The privilege of the pod that writes.** It runs only inside a
  turn, and it needs more than the scan pod.
