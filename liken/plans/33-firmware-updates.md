# Updating firmware that a machine and its devices keep

Milestone 33. Proposed, except for one part that is built and drilled:
the fallback that survives a firmware update. This milestone holds the
OS's part of updating firmware that a machine and its devices keep.
`firmware-operator`, an add-on that ships apart from the OS, declares
and runs the updates: its
[plan 03](../../firmware-operator/plans/completed/03-discovery.md) finds the
updates with fwupd and the LVFS catalog,
[plan 04](../../firmware-operator/plans/completed/04-approval.md) approves one,
[plan 05](../../firmware-operator/plans/completed/05-rehearsal.md)
rehearses it against a copy of the machine's variables, and plans
[06](../../firmware-operator/plans/06-applying-a-database-update.md)
to [08](../../firmware-operator/plans/08-applying-a-capsule.md) apply
it. The OS offers capabilities, and the add-on uses them: a reboot
turn ([milestone 88](88-disruptions.md)), the
one-shot boot of that turn's reboot, a boot order that survives a
reset, and the ESP of the next boot. The add-on makes every write to
the firmware itself. Root [plan 81](../../plans/81-firmware.md)
records the boundary.

## Three kinds of firmware

The word "firmware" names three different things in this project.

Milestone 32 ships firmware that the kernel loads into a device at
boot: wifi blobs, GPU blobs, CPU microcode. Those are files in the
image. The kernel reads them and the device runs them. Nothing on the
machine changes when the image changes.

`firmware-operator`'s plan 01 writes firmware into a device that holds
none of its own, such as a camera on a bare USB microcontroller, each
time the device appears. The bytes come from the vendor's container
image, and a power cycle removes them. The OS's only part is the USB
facts that [milestone 87](87-firmware-facts-a-machine-publishes.md)
publishes.

This milestone is about the third kind: firmware that a device keeps
across a power cycle, and that the machine
itself runs before any kernel loads. The UEFI firmware on the board, a
network card's NVRAM, an SSD's controller, a dock. Vendors ship
updates to these, and on Linux the program that detects and applies them
is fwupd, using the Linux Vendor Firmware Service (LVFS) as its
catalog. A vendor signs each update, and LVFS serves it.

## Why fwupd is not an ordinary workload

fwupd is a daemon with a memory cost, which matters on a 1 GB machine.
It has a live trust relationship with LVFS, and LVFS's vendor-signed
downloads are outside `liken`'s own digest chain. `firmware-operator`
answers both: it runs `fwupdtool` in short pods instead of the daemon,
and an approval pins each cabinet by its SHA-256.

The variables that fwupd writes are the problem. To apply a UEFI
update, fwupd
stages an update file, called a capsule, on an EFI system partition
(ESP), and then tells the firmware to apply it on the next boot. To do
that, it writes the firmware's variable store: it sets `BootNext`, the
one-shot choice of what to boot next; it creates a boot entry of its
own; and it writes `OsIndications`, the flag that tells the firmware a
capsule is waiting. Those are exactly the variables that `liken`'s
release trials depend on. `init/proving.go` explains the rule: the
store on disk is the authority, and init keeps the firmware's boot
preference a copy of it. A second writer to `BootNext` can overwrite a
staged release trial. In the other direction, init pins `BootNext` at
the proven slot on every boot and before every reboot
(`assertAndArmForReboot`), so today it overwrites fwupd's `BootNext`,
and fwupd's update is lost.

A Secure Boot database update writes none of those variables. It is
one authenticated append to `dbx`, `db`, or `KEK`, which `init` never
reads or writes. So a database update needs only the turn, and a
capsule needs every capability below.

## What is built: the fallback that a firmware update can erase

This part was built before the rest, because it protects machines that
never run fwupd. A dead NVRAM battery, a firmware update that resets
NVRAM to defaults, or the setup menu's own "load defaults" all do the
same thing to a UEFI machine.

On a UEFI machine, the boot entry loads `\vmlinuz` from the slot
through the kernel's EFI stub, and the kernel command line is stored
in that boot entry's optional data. Nothing else on the machine holds
that command line. When NVRAM is erased, every boot entry and every
command line are erased together. The installed disk is still
complete, but the firmware has no entry that can start it.

`armProvingBoot` refuses to risk this outcome for a release trial. But
a trial's fallback is a boot entry in NVRAM, and a firmware update can
erase NVRAM, so no rule about `BootNext` writes protects against this
case. Two changes fix it.

**Every boot writes the slots' boot entries again.** `healBootEntries`
in `init/bootentries.go`, run from `efiActuator.assertProven`, renders
each slot's entry from the GPT facts on the disk and writes back only
what drifted. So any boot of the machine repairs its boot menu. Both
slots are at the head of `BootOrder`, with the proven slot first, so a
firmware that cannot load one slot's kernel starts the other before it
reaches its own setup menu. The comparison runs before every write,
because NVRAM accepts a limited number of writes and this code runs on
every boot and before every reboot. This also completes the repair
after a dead battery. Before this change, the UEFI dialect could fix a
boot order but could not recreate an entry. The UEFI dialect now
repairs its boot entries on every boot, as the GRUB dialect has done
since milestone 30.

**The proven slot holds a loader at the default path.**
`init/slotloader.go` writes `\EFI\BOOT\BOOTX64.EFI`, a `loader.conf`
that never waits, and one loader entry holding the same command line
the firmware entry holds. A firmware at its defaults searches each
device for that one path. That search is how an installation stick
boots a machine with no entry for the stick, so a firmware at defaults
finds this loader and boots it. That boot repairs NVRAM, and the boot
after it is ordinary. The loader program is a copy of the
`systemd-bootx64.efi` that every slot already holds as a release
artifact. So the loader adds no new artifact, and it uses very little
of the slot size budget.

The loader is only on the proven slot. init removes the other slot's
copy only after the proven slot has its own, so a machine always has
a loader on at least one slot. The first design put a loader on each slot,
and that design fails in two ways. A firmware at its defaults boots
the first loader it finds. That loader would be on a slot with an
older release. At install time, it would be on an empty slot, where
the loader stops at a menu with no kernel to load. With one loader,
such a firmware cannot boot the wrong slot of the pair.

The command line is now in two places that must agree.
`init/grubcfg.go` already has the same requirement for BIOS machines. One
function, `slotArgs`, renders both, so they cannot disagree. The
loader entry names only the archives its own slot holds, because a
proven slot can hold an older release than the code writing the
loader, and systemd-boot refuses an entry whose initrd is missing.

A lab drill found one durability rule. On FAT, the directory entry
that a rename writes is the only record of a file's name, size, and
first cluster. Neither the per-file fsync nor an fsync of the
directory flushes that record. A FAT directory's entries are in
buffers attached to the block device, not in the directory's own
pages. The first promotion drill left slot B holding a loader entry
with the right name, no size, and no data. The firmware started that
loader and stopped at its menu with nothing to boot. The installer
already calls `unix.Sync` before it reports success. The loader writer
also calls `unix.Sync` after a changed write.

### What the lab measured

Every drill ran on node-1 under OVMF, at the 1 GB disk-boot size.
`make -C dev-cluster reset-nvram` copies the vendor-default variable
store over a guest's, which is what an update that resets NVRAM does to
a real board.

* A machine proven on slot A, with vendor-default variables, reached
  Ready in 9 seconds. It booted through `\EFI\BOOT\BOOTX64.EFI`, and
  that boot wrote both entries and put slot A at the head of
  `BootOrder`. The next boot came through its own entry and printed
  nothing.
* An upgrade to 2026.07.26-901 moved the proven slot from A to B in 46
  seconds. At the promotion, `BootOrder` moved to slot B, slot B took
  the loader, and init removed slot A's copy, in that order.
* That machine, proven on slot B, with vendor-default variables,
  reached Ready in 10 seconds on release 2026.07.26-901 from slot B. It
  did not boot the older release on slot A. This drill failed before
  the flush rule above existed, and that failure is the reason for the
  rule.
* Reading both slots offline confirmed the placement: slot B holds
  `EFI/BOOT/BOOTX64.EFI` at 135168 bytes with a 253-byte `loader.conf`
  and a 191-byte entry, and slot A keeps its own entry with no loader
  program beside it.
* `make smoke-uefi` and `make smoke-bios` both stayed green. Together
  they prove that a BIOS machine's boot chain still reads the same
  command line.


## What the OS offers

The OS keeps as little of firmware as it can. It knows no vendor, no
cabinet, and no `Firmware` object. It offers four capabilities, each
useful without fwupd, and `firmware-operator` writes the variable
store and the ESP itself, from its own pods, inside a turn.

### A reboot turn

[Milestone 88](88-disruptions.md) lets a
component ask for a machine's turn with a `Disruption`, hold the turn
after the drain while it works, and then release the machine to
reboot. The turn gives a firmware write the machine's
`MachineDisruptionBudget`s, one leader at a time, the drain, and a
reboot that only the conductor orders. A capsule's `Disruption` is
exclusive: under the turn's one cordon, it takes a reboot of its own,
and the requests that share a reboot, such as a release, take the last
one. A failed boot after a firmware write then has one cause.

A Secure Boot database update needs only this capability.

### The one-shot boot of a turn's reboot

When a component releases its turn, it can say that it set
`BootNext` for this reboot. machine-operator carries that in the
`RebootIntent` it writes for `init` (`machine/reboot.go`). On that
reboot, `assertAndArmForReboot` leaves `BootNext` as it finds it, and
arms no release trial: a release that is staged stays staged, and
`armProvingBoot` arms it on the turn's last reboot. It still
asserts `BootOrder` and heals the slots' entries, which writes nothing
when nothing drifted.

The firmware then boots fwupd's updater once, applies the capsule, and
resets. The next boot finds no one-shot, so it starts the head of
`BootOrder`, which is the proven slot. That boot runs
`assertProvenSlot` as every boot does, which pins `BootNext` at the
proven slot again. Nothing else in `init` changes. fwupd's
"Linux Firmware Updater" entry stays in `BootOrder` behind the slots,
where `bootOrderWith` keeps every entry it did not write, and
`firmware-operator` removes it in the machine's next turn.

The handover is for one reboot. A machine that does not reboot within
the turn, or an intent that carries no handover, gets the ordinary
pin. `init` also refuses the handover, and pins as usual, when the
reboot could leave the machine with no way back: the proven slot has
no default-path loader, or `chooseBootActuator` returned `noActuator`.
A firmware update cannot be undone, so these checks are stricter than
a release's, in the pattern of `canArmTrial`. `init` prints the
refusal, and machine-operator reports it on the `Disruption`, so the
component reads that its update did not run.

### A boot order that survives a reset

The open problem
[firmware-bootorder-persistence](open-problems/firmware-bootorder-persistence.md)
records a firmware that reads a `BootOrder` write back correctly and
loses it at the reset. On such a firmware, the one-shot is the only
preference that survives, and a capsule's reboot consumes it. The boot
after the capsule then follows the old `BootOrder`. The proven slot's
entries and the default-path loader mean the machine still boots
`liken`, but it may boot the other slot. A capsule turn needs the
record-and-compare check that the open problem proposes, and the
check is worth building for release trials alone.

### The ESP of the next boot

fwupd writes its updater and the capsule to the ESP that the firmware
reads at the next boot. On a `liken` machine, that is the proven
slot, because a handover reboot arms no release trial. The OS has to let the component's pod mount that one
partition. One shape is a fixed path on the host that `init` binds to
the proven slot at each boot, so fwupd's `EspLocation` never changes.
The shape is not decided.

Read in the code, not measured: `verifySlotContents` hashes only the
artifacts that a slot's release document lists, so a file that fwupd
adds does not fail a slot's verification. The file does use the
slot's free space, and a later release written to that slot must
still fit. `firmware-operator` removes the files after it proves the
update.

`init` also deletes `\EFI\UpdateCapsule` from any slot that is not the
proven slot. A capsule on such a slot applies at a later boot from
it, which gives the machine a firmware change that nobody requested.
Milestone 37 applies the same rule to partitions, and milestone 42
applies it to the work of a retracted feature.

### What firmware-operator owns

Everything else. It rehearses each install, asks for the turn, writes
the variables and the ESP with `fwupdtool` in a pod inside the turn,
proves the update from its next scan, which reads the ESRT through
fwupd, and cleans up the updater's entry and files. Its plans 05 to 08
hold the design. Its manual states the risk that a firmware update can
turn Secure Boot on or reset its keys. That stops `liken`'s unsigned
`vmlinuz` from loading, and the default-path loader is unsigned too,
so no boot order repairs it. The fix is the signed releases and UKIs
of the hardening tier.

## The firmware version in Machine status

[Milestone 65](../../plans/completed/65-prometheus-metrics.md) names
the metrics `liken_firmware_info` and `liken_firmware_update_pending`
as waiting on a firmware version in `Machine` status. `FirmwareStatus`
in `machine/status.go` carries the boot mode, `BootCurrent`,
`BootNext`, and `BootOrder`, and no version. The kernel exposes the
board's firmware version and date under `/sys/class/dmi/id` as
`bios_version` and `bios_date`, on BIOS and UEFI machines alike.
One new field, read from there and printed at boot by the console
parity rule, closes that gap. `firmware-operator` does not need it,
because its scan reads the version from fwupd.

## What the lab can measure

Everything the OS offers runs under QEMU, because each capability
depends on boot order or filesystem behavior, and none depends on the
hardware:

* Write a competing `BootNext` by hand and release a hold with the
  handover. The one-shot survives the reboot, and the boot after it
  starts the proven slot.
* Stage a release and an exclusive `Disruption` with the handover on
  one machine. The first reboot arms no trial, and the release's
  trial arms on the turn's last reboot.
* Write a file into `\EFI\UpdateCapsule` on the slot that is not the
  proven slot. The next boot deletes it.

Whether the firmware consumes a staged capsule needs an ESRT and an
updatable firmware device. That means OVMF built with edk2's
`FmpDevicePkg` and `EsrtFmpDxe`, or real metal. `firmware-operator`'s
plan 08 needs the answer, and the OS does not.

## Running fwupd before this milestone

A deployment can run fwupd as a privileged workload today, but that
has a cost for `liken`. Such a pod writes `BootNext`, so it can
overwrite a staged release trial, and init's pin of the proven slot
overwrites fwupd's update. A deployment that runs fwupd now should do
it with no release staged, and expect a capsule not to apply. A
Secure Boot database update writes no boot variable, and stick-1 took
one this way
([plan 05](../../firmware-operator/plans/completed/05-rehearsal.md#the-real-write-on-stick-1)).

An update that resets NVRAM no longer strands the machine, because the
proven slot holds a loader that a firmware at its defaults finds. An
update that turns Secure Boot on still strands it, because `liken`'s
`vmlinuz` and that loader are both unsigned. That risk stays until the
hardening tier's signed releases and UKIs exist.

## Considered and set aside

- **A shim that makes `init` the only writer.** An earlier design of
  this milestone gave fwupd's pod a tmpfs in place of `efivarfs`.
  `init` watched it, read each write as a request, and wrote the real
  variable when it allowed it. It kept one writer of the variable
  store, but it put fwupd's write semantics into `init`: the
  immutable flag, the append, the boot entry, and `OsIndications`.
  `liken` keeps the OS to capabilities that any add-on can use, so the
  add-on writes inside a turn instead. The shim's case against a
  window was that a window leaves the variable store writable to a
  privileged pod at all times. Inside a turn, only `firmware-operator`'s
  pod writes, and only while the conductor's grant holds.
- **A release artifact for one machine.** `fetchRelease` in
  `machine-operator/fetch.go` writes every artifact to every slot. An
  optional artifact that one machine selects would carry fwupd on a
  slot. `firmware-operator` runs fwupd as a container image, so
  firmware does not need it. Milestone 34 still does.
- **`init` proves a capsule from the ESRT.** The proof reads the next
  scan of `firmware-operator`, which reads the ESRT through fwupd. A
  second reader in `init` adds a fact that nothing in the OS acts on.
