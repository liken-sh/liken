# 07, Rehearsing a capsule

Proposed on 2026-10-21. Not built. This plan extends the rehearsal of
[plan 05](completed/05-rehearsal.md) from the Secure Boot databases to
capsules, so a person who approves a board firmware update reads what
the install would write before anything writes it. It writes nothing
real, and it needs nothing from the OS. [Plan
08](08-applying-a-capsule.md) applies a capsule that this rehearsal
passed.

## The problem

The rehearsal refuses a device whose fwupd plugin is not `uefi_dbx`,
`uefi_db`, or `uefi_kek`, without an install. A capsule writes more:

- In NVRAM mode, fwupd creates a boot entry named "Linux Firmware
  Updater" and sets `BootNext` to it. With the `modify-bootorder`
  quirk, which fwupd applies to Lenovo, Dell, ASUS, Framework, and HP
  machines, it also appends the entry to `BootOrder`.
- In capsule-on-disk mode, it writes `OsIndications` and a file under
  `\EFI\UpdateCapsule`.
- Either way, it writes its updater, `fwupdx64.efi`, and the capsule
  under `\EFI\fwupd` on the ESP.

These facts come from the source of fwupd 2.1.1, the version in the
scan image. No capsule was offered to liken-1 or stick-1 in the plan
03 drills, so none is measured.

## The design

The rehearsal copies the ESP of the next boot beside the variables,
and points `FWUPD_UEFI_ESP_PATH` at the copy. The ESP of the next boot
is the proven slot's, which `liken`'s
[milestone 33](../../liken/plans/33-firmware-updates.md) names with
the link `/run/liken/proven-esp`. The check then allows exactly the capsule's writes and refuses
everything else:

- the creation of one boot entry, whose path names the updater on the
  ESP;
- `BootNext`, naming that entry;
- one `fwupd-*` variable under fwupd's vendor GUID
  `0abba7dc-e516-4167-bbf5-4d9d1c739416`, which records the capsule's
  path and status for the updater;
- `BootOrder`, only when its old entries keep their order at its head;
- `OsIndications`, only with the capsule-on-disk bit;
- files under `\EFI\<ID>`, where `<ID>` is the `ID` of the scan
  image's `os-release`, and under `\EFI\UpdateCapsule` on the ESP,
  and nowhere else.

Under OVMF, fwupd 2.1.1 wrote exactly these in NVRAM mode, and did not
write `BootOrder`. In capsule-on-disk mode it wrote only the capsule
under `\EFI\UpdateCapsule` and `OsIndications`
([milestone 33, the firmware under QEMU](../../liken/plans/33-firmware-updates.md#the-firmware-under-qemu)).

The record names each variable and file written, with its size and
SHA-256, and the ESP's free space against the files' size.

A rehearsal of a capsule cannot show whether the firmware accepts the
capsule. The firmware checks the capsule's signature and applies it
before any kernel loads, and a copy has no firmware. The rehearsal
proves only what the operating system side writes.

## The proof

A capsule offered to a testbed machine, rehearsed, and refused for
each write the check does not allow. A machine with a capsule on LVFS
is the first need. A read-only scan of the lab fleet and of the
testbed tells which machines have one. Under OVMF, a capsule needs an
ESRT and an updatable firmware device, which means OVMF built with
edk2's `FmpDevicePkg` and `EsrtFmpDxe`.

## Open questions

- Which machine has a capsule on LVFS. The lab's proof does not wait
  on it: the OVMF build with a test device offers one, from a catalog
  that the lab serves.
- Whether to rehearse each capsule against the running slot's ESP
  when the proven slot is not the running one. A capsule's reboot
  arms no release trial (milestone 33), so the two are the same slot
  in a turn, but a rehearsal runs outside a turn.
- Device updates through fwupd's other plugins, such as SSDs and
  docks. They write the device itself, and no copy can rehearse them.
  They need a plan of their own.
