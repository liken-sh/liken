# 09, Cleaning up after a capsule

Proposed on 2026-10-23. This plan finishes the last two steps of
[plan 08](completed/08-applying-a-capsule.md), which were not built: the
operator records the firmware's own result for a capsule beside the
version, and it removes what fwupd left on the machine after the
update.

## The problem

After a capsule in NVRAM mode, fwupd's next run deletes the capsule
and its `fwupd-*` variable. fwupd's "Linux Firmware Updater" boot
entry and its updater, `fwupdx64.efi`, stay on the slot's ESP under
`\EFI\<ID>`, where `<ID>` is the `ID` of the scan image's
`os-release`. A later capsule uses them again: on the `lab` fleet on
2026-10-23, a rehearsal on fwupd 2.1.6 staged 2 variables and 1 file
because the entry and the updater from the earlier drill were still
there. When the scan image moved from Ubuntu to Debian on the same
day, fwupd wrote a second entry and a second updater under
`\EFI\debian`, beside the ones under `\EFI\ubuntu`. So each change of
the scan image's `ID` adds one more entry and one more updater, and
nothing removes them.

The `fwupd-*` variable holds the firmware's own result for the
update. The operator does not read it, so a person sees only whether
the version changed. The version still decides whether an update
applied, because fwupd can report a failure for an update that
applied ([fwupd#11119](https://github.com/fwupd/fwupd/issues/11119)).
The firmware's result tells a person why a version did not change.

## The design

1. **Record the result.** The first scan after a capsule's reboot
   reads the firmware's result for the device, and the operator writes
   it into that machine's entry in the `Firmware` object's status,
   beside the version. The scan must read the result before any run of
   fwupd deletes the variable.
2. **Remove the entry and the updater.** A `Disruption` that waits for
   the machine's next turn removes the "Linux Firmware Updater" boot
   entry and the files that fwupd wrote under `\EFI\<ID>`, as plan 08's
   step 6 proposed. The next capsule then writes its entry and its
   updater again, and its rehearsal shows those writes.

## Open questions

- **Which reading holds the result.** The `fwupd-*` variable, the
  ESRT's `last_attempt_status`, or both. The lab firmware's log read
  `LastAttemptStatus: 0` for each update, so the ESRT is a candidate
  that no run of fwupd deletes.
- **Whether the scan's run of fwupd deletes the variable** before the
  operator reads it.
- **Whether the removal needs a turn.** The removal reboots nothing,
  but it writes boot variables and the ESP while `init` asserts
  `BootOrder` and heals its own boot entries.
- **Which slots hold the files.** fwupd writes to the ESP of the proven
  slot. After a release promotes the other slot, the files are on the
  slot that is not proven. `init` removes `\EFI\UpdateCapsule` from
  that slot, and does not remove `\EFI\<ID>`.
