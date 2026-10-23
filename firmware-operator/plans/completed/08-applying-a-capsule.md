# 08, Applying a capsule

Proposed on 2026-10-21. Built on 2026-10-22, and drilled on three
machines of the `lab` fleet on OVMF with a test device
([As built](#as-built), [The lab drill](#the-lab-drill)). No real
board has taken a capsule through the operator. This plan applies an approved
board firmware update, a capsule, inside a reboot turn, and proves it
from the next scan. It follows plans
[06](06-applying-a-database-update.md), which applies a database
update in a turn, and [07](07-rehearsing-a-capsule.md), which
rehearses a capsule. It uses every capability that `liken`'s
[milestone 33](../../../liken/plans/completed/33-firmware-updates.md) offers: the
turn of [milestone 88](../../../liken/plans/completed/88-disruptions.md),
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
2. **Ask for a turn.** As in plan 06, with `spec.hold`. The reboot that
   hands `BootNext` over arms no release trial, so a release that
   shares the turn takes a later reboot, and the capsule's reboot
   carries no other release
   ([milestone 33, as built](../../../liken/plans/completed/33-firmware-updates.md#as-built)).
3. **Write.** After the drain, the apply pod runs `fwupdtool install`
   against the real variable store and the ESP of the next boot, which
   the pod mounts through `/run/liken/proven-esp`. Unlike
   a database update, the writes are several, and fwupd makes them in
   its own order, so the pod runs fwupd instead of replaying the
   rehearsal's record. It then compares what fwupd wrote with the
   rehearsal's record. A difference fails the apply, and the operator
   clears the hold with no handover, so `init` pins `BootNext` at the
   proven slot and the capsule does not apply. A failed step holds
   the rest of the turn until a person clears it.
4. **Release the hold with the handover.** The operator removes `hold`
   and sets `spec.keepBootNext` in one patch. The firmware boots the
   updater, applies the capsule, and resets. The next boot starts the
   proven slot, and `init` restores its own `BootNext`.
5. **Prove.** The next scan reads the version from the ESRT through
   fwupd. The capsule status variable records the firmware's own
   result, and the operator records it beside the version. fwupd can
   report a failure for an update that applied
   ([fwupd#11119](https://github.com/fwupd/fwupd/issues/11119)), so
   the version decides.
6. **Clean up.** A `Disruption` that waits for the machine's next
   turn removes the "Linux Firmware Updater" boot entry and the files
   that fwupd wrote on the slot. fwupd writes its updater and the
   capsule under `\EFI\<ID>`, where `<ID>` is the `ID` of the scan
   image's `os-release`, and a capsule-on-disk under
   `\EFI\UpdateCapsule`. After an update in NVRAM mode, fwupd's next
   run deletes the capsule and its `fwupd-*` variable, and the entry
   and the updater stay.

## Preconditions

A firmware update cannot be undone, so the operator checks more than
for a database update before it asks for a turn:

- The board's battery state, where fwupd reports one. fwupd refuses
  some capsules on battery.
- The ESP's free space against the rehearsal's files.
- No capsule pending on the machine from an earlier attempt.
- The machine's `status.firmware.bootOrderKept` is `true`, and the
  machine has a proven slot, so `/run/liken/proven-esp` exists.

`init` refuses the handover when the machine has no way back, under
milestone 33's rules, and prints why. The refusal is not reported on
the `Disruption`, so the operator reads it from the next scan, which
still offers the approved version.

## The risk the manual states

An update can turn Secure Boot on or reset its keys. `liken`'s
`vmlinuz` and the default-path loader are unsigned, so the machine
then boots neither, and no boot order repairs it. The guide that
approves a capsule states this before the approval, until the
hardening tier signs `liken`'s boot chain.

## The proof

Under OVMF built with `FmpDevicePkg` and `EsrtFmpDxe` first, with a
test capsule. That firmware took a capsule through fwupd in a
throwaway guest
([milestone 33, the firmware under QEMU](../../../liken/plans/completed/33-firmware-updates.md#the-firmware-under-qemu)),
and the lab fleet needs it as one more `FIRMWARE` choice. Then one real board update on a testbed machine that a
catalog offers one, after plan 07's rehearsal of it.

## Open questions

- Capsule-on-disk against NVRAM mode. Capsule-on-disk writes only
  `OsIndications` and a file, so it needs no handover of `BootNext`,
  but fwupd calls that path uncommon.
- How long the firmware's update may take before the machine's
  `Ready` counts as late, and what the turn's deadline is for it.

## As built

- **The precondition.** The operator starts a capsule's apply only on
  a machine whose `status.firmware.bootOrderKept` is `true`, and
  records `Refused` on any other. The battery, the ESP's free space
  beyond the rehearsal's check, and an earlier pending capsule are not
  checked.
- **The write.** The apply pod mounts the ESP of the next boot
  read-write and runs fwupd on the machine's own variable store, with
  no device ID, because the device's ID depends on the path of its
  firmware tables. It repairs the updater's partition as the rehearsal
  does (plan 07), and judges what fwupd wrote with the rehearsal's
  rules. Writes whose digest is the rehearsal's read `Written`. Other
  writes, and a failure after a write, read `Uncertain`. Before the
  install it clears the immutable flag of fwupd's status variable from
  an earlier update, which fwupd cannot open otherwise.
- **The handover.** On `Written`, the operator removes `spec.hold` and
  sets `spec.keepBootNext` in one patch. On `Uncertain`, it removes
  `spec.hold` alone, so `init` aims `BootNext` at the proven slot, and
  the capsule does not run.
- **Not built.** The cleanup of step 6: fwupd's entry and its updater
  stay, and a later capsule uses them again. The record of the
  capsule's own status variable beside the version.
  [Plan 09](../09-cleaning-up-after-a-capsule.md) holds both.

## The lab drill

On 2026-10-22, node-3, node-4, and node-5 of the `lab` fleet ran
`liken` `2026.10.22-912` on OVMF built with an FMP test device
(`dev-cluster/firmware/capsule/`), and a `FirmwareCatalog` with
`spec.trust` offered versions 2 and 3 from the lab's server. The
`default` budget allows one machine out at a time.

The drill found six faults before the first capsule applied, each on a
build that reached the lab: the scan's read-only `efivarfs`, the
device's ID that changed with the copy's path, the missing files under
`efi/`, the missing updater, the zeroed partition in the updater's
entry, and the zeroed partition in its status variable. The first of
the partition faults let the firmware boot fwupd's updater, which
could not find the capsule, took a CPU exception, and stopped node-4
until it was reset by hand.

On build `cap-15`, version 2 rolled to the three machines in turn,
and version 3 followed:

| Release | node-5 | node-3 | node-4 |
|---|---|---|---|
| 2 | 22:08:44Z | 22:09:18Z | 22:09:51Z |
| 3 | 22:11:20Z | 22:11:54Z | 22:10:48Z |

Each time is the scan of the new boot that read the version. The
firmware's log of each update boot reads `ValidateFmpCapsule - Success`
and `SetTheImage() LastAttemptStatus: 0`. Each machine came back on its
proven slot, and the approval read `Installed`.

## The drill on fwupd 2.1.6

On 2026-10-23 the scan image moved from Ubuntu 26.04's fwupd 2.1.1 to
Ubuntu 26.10's fwupd 2.1.6, because 2.1.1 never offered stick-1 the
KEK release for its platform key. The lab catalog gained version 4,
and the same three machines ran version 3.

The first build failed every rehearsal with `Device ... does not
currently allow updates`. The rehearsal named the device's ID, so
fwupdtool planned the release twice for the device. The first install
staged the capsule and hid the device until the next boot, and fwupd
2.1.6 refuses an install on a hidden device (fwupd commit
`d311af1d5`). With the cabinet alone, one install ran and passed.

On the fixed build, each rehearsal staged 2 variables and 1 file:
fwupd reused the updater's boot entry and `fwupdx64.efi` from the
earlier drill. Version 4 rolled to the three machines in turn:

| Release | node-3 | node-4 | node-5 |
|---|---|---|---|
| 4 | 13:24:46Z | 13:25:15Z | 13:25:45Z |

Each time is the scan of the new boot that read the version. Each
machine came back on its proven slot, and the approval read
`Installed`.

## The drill on Debian's fwupd and on disk

On 2026-10-23 the scan image moved to Debian 13 with fwupd 2.1.7 from
its backports, and the operator began to scan each machine again when
it runs a new scan image. The lab's three capsule machines ran
version 4.

Within 20 seconds of the new operator's start, it scanned all five
machines against both catalogs, with no reboot, and every record
named the new image.

fwupd on Debian writes its updater and capsule under `\EFI\debian`.
The first rehearsal staged 3 variables and 2 files, a new boot entry
and updater beside the ones under `\EFI\ubuntu`. Version 5 rolled to
the three machines in NVRAM mode, and version 6 followed:

| Release | node-3 | node-4 | node-5 |
|---|---|---|---|
| 5 | 14:17:27Z | 14:16:45Z | 14:16:51Z |
| 6 | 14:19:25Z | 14:19:52Z | 14:20:19Z |

The lab firmware's patch had lost the capsule-on-disk part of the
first spike, so a build with `CAPSULE_ON_DISK_ENABLE` made the same
firmware as one without it. With the part restored, node-5 booted the
on-disk build and took version 7 on disk: its rehearsal staged
`\EFI\UpdateCapsule\fwupd-<guid>.cap` and `OsIndications`, the apply
wrote the same, and the firmware's log read
`capsule on disk: Success` and `LastAttemptStatus: 0`. node-3 and
node-4 took version 7 in NVRAM mode in the same rollout.

| Release | node-3 | node-4 | node-5 |
|---|---|---|---|
| 7 | 14:31:35Z | 14:32:02Z | 14:30:54Z |

Each time is the scan of the new boot that read the version. Each
machine came back on its proven slot with `bootOrderKept: true`, and
the approval read `Installed`.
