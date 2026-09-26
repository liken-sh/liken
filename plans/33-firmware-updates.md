# Updating the machine's own firmware

Milestone 33. Proposed, except for one part that is built and drilled:
the fallback that survives a firmware update. This milestone would let
a `liken` machine update its own firmware. fwupd would do the
vendor-specific work, and `init` would stay the only process that
writes the boot chain.

## Two kinds of firmware

The word "firmware" names two different things in this project.

Milestone 32 ships firmware that the kernel loads into a device at
boot: wifi blobs, GPU blobs, CPU microcode. Those are files in the
image. The kernel reads them and the device runs them. Nothing on the
machine changes when the image changes.

This milestone is about the other kind: the firmware that the machine
itself runs before any kernel loads. The UEFI firmware on the board, a
network card's NVRAM, an SSD's controller, a dock. Vendors ship
updates to these, and on Linux the program that detects and applies them
is fwupd, using the Linux Vendor Firmware Service (LVFS) as its
catalog. A vendor signs each update, and LVFS serves it.

## Why fwupd is not an ordinary workload

fwupd is a daemon with a memory cost, which matters on a 1 GB machine.
It has a live trust relationship with LVFS, and LVFS's vendor-signed
downloads are outside `liken`'s own digest chain. A deployment can
accept these costs.

The variables that fwupd writes are the problem. To apply a UEFI
update, fwupd
stages an update file, called a capsule, on an EFI system partition
(ESP), and then tells the firmware to apply it on the next boot. To do
that, it writes the firmware's variable store: it sets `BootNext`, the
one-shot choice of what to boot next; it creates a boot entry of its
own; and it writes `OsIndications`, the flag that tells the firmware a
capsule is waiting. Those are exactly the variables that `liken`'s
release trials depend on. `init/proving.go` explains the rule: the
store on disk is the authority, and init is the one process that talks
to the firmware. A second writer to `BootNext` can overwrite a staged
release trial. In the other direction, when init arms a release trial
(sets the one-shot `BootNext` for the trial boot), it can overwrite
fwupd's `BootNext`, and fwupd's update is lost.

The design below keeps init as the only writer: it removes fwupd's
access to the variable store. It does not schedule fwupd's writes into
rollout turns.

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

## Prerequisites for fwupd

Four pieces of work are missing, and the rest of this plan assumes
them. None of them involves fwupd. Each is a separate change, and each
is useful without the others.

**The machine must report its firmware version.** `FirmwareStatus` in
`machine/status.go` carries the boot mode, `BootCurrent`, `BootNext`,
and `BootOrder`, and no version. `firmwareFacts` in `init/efi.go` reads
those from the variable store. The kernel exposes the board's firmware
version and date under `/sys/class/dmi/id` as `bios_version` and
`bios_date`, on BIOS and UEFI machines alike. Milestone 65 waits for
one new field on `FirmwareStatus`, read from there and printed at boot
by the console parity rule. That plan's table and
`machine-operator/metrics.go` name the metrics `liken_firmware_info`
and `liken_firmware_update_pending` as waiting on this field. This is
the first part of this milestone, and it needs no fwupd, no capsule,
and no new trust relationship.

**A release must be able to carry an artifact for one machine.**
`fetchRelease` in `machine-operator/fetch.go` writes every artifact in
the release document to every slot, and `releases/bundle.go` sums all
artifacts against one fleet-wide slot size. fwupd, its plugins, and
its LVFS metadata cannot ship in every image; they are the kind of
payload that one machine needs and the fleet does not. Milestone 34's
GPU add-on has the same need, and neither plan names it today. The
proposed design is an artifact that the release document marks as
optional, that a Machine selects by name, and that the fetcher writes
only to the machine that selected it.

**A feature must be able to run one pod per machine.** Every feature
of kind `FeatureWorkload` in `cluster/features.go` is a cluster
singleton. `flux` is the only one, and the cluster operator seeds it
once. fwupd runs on each machine that updates, with that machine's
devices. The feature kinds need a second workload kind: one
DaemonSet-like pod on each machine that opts in. A `fwupd` feature
slug cannot work until that kind exists. The open problem
[firmware-bootorder-persistence](open-problems/firmware-bootorder-persistence.md)
is a fourth prerequisite of a different kind. Any firmware trial
assumes that `BootOrder` survives a reset, and the safeguard that
document proposes does not exist yet. The pin of `BootNext` at the
proven slot is built in `efiActuator.assertProven`. The
record-and-compare check is not built.

## The fwupd design

Everything below is proposed. It depends on the four prerequisites
above.

### How init makes fwupd's variable writes

The feature gives the fwupd pod no access to
`/sys/firmware/efi/efivars`, the filesystem through which Linux
exposes the variable store. In its place, the pod sees a tmpfs that
looks like efivarfs. This plan calls that tmpfs the shim. The ESRT,
the table under `/sys/firmware/efi/esrt` where the firmware lists each
updatable component and its version, is bind-mounted read-only, so
fwupd's device discovery still works.

init watches the shim with the inotify machinery that the facts tree
already uses. A write from fwupd changes only the shim, and init reads
it as a request. init decides whether that request becomes a real
variable write, and when. After init writes the real variable, it
copies the value into the shim so later reads agree.

The alternative is a scheduled window: give fwupd the real variable
store, and let it apply updates only while `liken` holds a conductor
grant. The shim is better. A window protects the boot chain only
through timing. It leaves NVRAM writable to a privileged pod at all
times. It also needs a way to handle a crash in the middle of the
window that leaves a foreign `BootNext` behind. With the shim, fwupd
has no path to the real variable store, so init is always the only
writer.

fwupd's configuration offers no third option. Its UEFI plugin reads
`EspLocation`, `DisableCapsuleUpdateOnDisk`, and `RequireESPFreeSpace`,
and none of them stops the `BootNext` write.

One measurement is still needed to show whether the shim works as
written. fwupd
clears the per-file immutable flag before it writes a variable, the
same sequence `writeEFIVar` performs, and `FS_IOC_GETFLAGS` may return
`ENOTTY` on tmpfs. `writeEFIVar` already treats an error from that call
as nothing to clear. The shim works only if fwupd does the same.

### One reboot applies one change

The UEFI specification has the firmware read `\EFI\UpdateCapsule` only
from the ESP on the device named in the active boot option. That
option is `BootNext` when it is set and `BootOrder` otherwise. `liken`
controls that entry completely: `registerSlotEntries` puts both slots
at the head of `BootOrder`, and `efiActuator.assertProven` keeps the
proven slot first.

So the rule is that init never arms a capsule and a release trial for
the same reboot. This rule keeps the boot device known. It also means
that when a proving boot fails, the failure has one cause: the capsule
or the release, never both.

`armProvingBoot` already supports this deferral. It has five early
returns that leave the staged record untouched, and
`settleSystemRelease` reads that state as a release that still waits
for its proving boot. A staged capsule adds one more condition for an
early return that already exists. The deferral costs one reboot.

A capsule on a slot that is no longer the proven slot does nothing
until a later boot from that slot. That boot then applies it, and the
machine gets a firmware change that nobody requested. So init deletes
`\EFI\UpdateCapsule` from any slot that is not the proven slot.
Milestone 37 applies the same rule to partitions, and milestone 42
applies it to the work of a retracted feature.

### The ESP that the firmware reads

fwupd finds an ESP on its own only when one is mounted at `/boot/efi`,
`/boot`, or `/efi`, or when UDisks reports one. `liken` has none of those
mounts and no UDisks, so detection fails. This failure is safe.

The rule of one change per reboot decides which ESP is correct. With
no slot switch pending, it is always the running slot's ESP. Slots already mount
at fixed role paths (`init/grubactuator.go`), so init binds the running
slot at one constant path and sets `EspLocation` to it. fwupd's
configuration then never changes, and each boot binds that path to the
correct slot.

Where the firmware supports capsule-on-disk, prefer it. fwupd then
writes `OsIndications` instead of `BootNext`, so the shim has fewer
writes to handle. The design must not depend on capsule-on-disk,
because fwupd itself calls that path uncommon.

### Firmware updates in the rollout conductor

`wantsTurn` in `cluster-operator/rollout.go` matches the `AwaitingTurn`
reason on any condition. So the rollout conductor gives a turn to a
machine whose `FirmwareConverged` condition sets that reason, with no
change to the conductor. Firmware updates then get the disruption
budget, the limit of one leader at a time, the drain, and stall
detection. fwupd never reboots
a machine itself. The machine reboots only when the conductor grants
it a turn, the same as for every other staged change.

The plan does not yet say which process computes `FirmwareConverged`.
The machine operator sets `AwaitingTurn` generically in `converge.go`,
so it is the likely process, and it would read the ESRT through the
facts tree.

### Proving a change that the firmware applied

Read the evidence from the machine, not from fwupd. After the boot,
the ESRT reports each component's version, and the firmware writes
its own result into a capsule status variable. Proven means the ESRT
reports the declared target and the machine joined its cluster. Both
facts belong in the facts tree and in Machine status, by the console
parity rule.

A firmware update has no second slot and cannot be undone, so
`RejectedLastBoot` does not apply: there is no earlier state to return
to. The preconditions must be
stricter than a release's, in the `canArmTrial` pattern: refuse when
the default-path loader is absent, when `chooseBootActuator` returned
`noActuator`, when a release is staged for the other slot, and when
the ESP lacks free space.

One risk has no precondition that can catch it in advance. An update
that turns Secure Boot on, or resets its keys, stops `liken`'s unsigned
`vmlinuz` from loading, and the default-path loader is unsigned too.
No boot order repairs that. The fix is the signed releases and UKIs
of the hardening tier. Until those exist, the feature must state this
risk.

## What the lab can measure

Almost all of this runs under QEMU, because every decision above
depends on boot order or filesystem behavior, and none depends on the
hardware. Two drills need
no fwupd, no capsule, and no ESRT:

* Write a capsule directory and a competing `BootNext` by hand. This
  drills the one-change-per-reboot rule, the false-rejection path through
  `settleSystemRelease`, and the sweep.
* Mount the tmpfs and run fwupd against it. This drills the shim,
  including the immutable-flag question.

One question needs more than stock OVMF. Whether the firmware consumes
a staged capsule needs an ESRT and an updatable firmware device. That
means OVMF built with edk2's `FmpDevicePkg` and `EsrtFmpDxe`, or real
metal. No decision above depends on the answer, so the design does not
wait for it.

Real vendor targets stay on metal. NIC NVRAM, SSD firmware, and docks
need their own buses, and a guest cannot update the firmware image
that its host owns.

## Running fwupd before this milestone

A deployment can run fwupd as a privileged workload today, but that
has a cost for `liken`. Such a pod writes `BootNext`, so it can
overwrite a staged release trial, and init's arming of a trial can
overwrite fwupd's update. A deployment that runs fwupd now should do
it with no release staged.

An update that resets NVRAM no longer strands the machine, because the
proven slot holds a loader that a firmware at its defaults finds. An
update that turns Secure Boot on still strands it, because `liken`'s
`vmlinuz` and that loader are both unsigned. That risk stays until the
hardening tier's signed releases and UKIs exist.
