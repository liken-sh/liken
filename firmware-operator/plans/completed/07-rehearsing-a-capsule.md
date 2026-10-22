# 07, Rehearsing a capsule

Proposed on 2026-10-21. Built on 2026-10-22, and drilled with plan
08 on the `lab` fleet ([As built](#as-built)). This plan extends the rehearsal of
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

## As built

Commit `97f0709c` built the rehearsal, and the drill of plan 08 on the
same day changed four parts of it.

- **The copy of the ESP.** The rehearsal copies only the `\EFI`
  directory of the ESP of the next boot, which the pod mounts
  read-only from `/run/liken/proven-esp`. A slot's ESP also holds a
  whole release, and fwupd writes only under `\EFI`. The pod mounts
  the ESP only on a machine that reports
  `status.firmware.bootOrderKept`, because only that machine has a
  proven slot.
- **The copy of the variable store.** fwupd 2.1.1 disables its
  `uefi_capsule` plugin unless `efi/efivars` is a mount point that it
  could write, or the firmware supports capsule-on-disk. The scan had
  mounted `efivarfs` read-only, so on the lab's firmware, which has no
  capsule-on-disk, no scan saw a capsule device. Every pod now copies
  `/sys/firmware` to `/run/fw` and mounts a directory in memory at
  `/run/fw/efi/efivars`. The scan no longer mounts `efivarfs`. fwupd
  names a capsule device by the path of its ESRT entry, so every pod
  copies to the same path, and the device keeps the ID that the scan
  recorded. The copy also holds the files directly under `efi/`, such
  as `fw_platform_size`, which the install reads.
- **The updater.** The scan image holds fwupd-efi's updater from
  Ubuntu's `fwupd-signed`, 1.55+1.7-1, which a capsule's install
  copies to the ESP.
- **The updater's partition.** fwupd reads the ESP's partition only
  from udisks over D-Bus, so in a pod it writes a hard drive node of
  zeros into the updater's boot entry and into its status variable. The
  rehearsal and the apply copy the node of `liken`'s boot entry for the
  ESP's partition into both (`scan/updaterentry.go`).

The judge allows an updater entry from an earlier update that fwupd
writes again, because fwupd keeps its entry after an update.
