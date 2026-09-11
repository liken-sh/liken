# Updating the machine's own firmware

Milestone 33. Proposed, except for one part that is built and drilled:
the fallback that survives a firmware update. This milestone would let
a `liken` machine update its own firmware, with fwupd doing the vendor
work and `init` keeping its place as the only writer of the boot chain.

## Two kinds of firmware

The word "firmware" names two different things in this project.

Milestone 32 ships firmware that the kernel loads into a device at
boot: wifi blobs, GPU blobs, CPU microcode. Those are files in the
image. The kernel reads them and the device runs them. Nothing on the
machine changes when the image changes.

This milestone is about the other kind: the firmware that the machine
itself runs before any kernel loads. The UEFI firmware on the board, a
network card's NVRAM, an SSD's controller, a dock. Vendors ship
updates to these, and on Linux the program that finds and applies them
is fwupd, using the Linux Vendor Firmware Service (LVFS) as its
catalog. A vendor signs each update. LVFS serves it.

## Why this is not an ordinary workload

fwupd is a daemon with a memory cost, which matters on a 1 GB machine.
It has a live trust relationship with LVFS, and LVFS's vendor-signed
downloads are outside `liken`'s own digest chain. Those are costs, and a
deployment can accept them.

The problem is what fwupd writes. To apply a UEFI update, fwupd
stages an update file, called a capsule, on an EFI system partition
(ESP), and then tells the firmware to apply it on the next boot. To do
that, it writes the firmware's variable store: it sets `BootNext`, the
one-shot choice of what to boot next; it creates a boot entry of its
own; and it writes `OsIndications`, the flag that tells the firmware a
capsule is waiting. Those are exactly the variables that `liken`'s
release trials depend on. `init/proving.go` explains the rule: the
store on disk is the authority, and init is the one process that talks
to the firmware. A second writer to `BootNext` can overwrite a staged
release trial, or lose its own update to `liken`'s arming.

The design below keeps init as the only writer by taking that ability
away from fwupd, and not by scheduling fwupd's writes into turns.

## What is built: the fallback that a firmware update can erase

This part landed ahead of the rest, because it protects machines that
never run fwupd. A dead NVRAM battery, a firmware update that resets
NVRAM to defaults, or the setup menu's own "load defaults" all do the
same thing to a UEFI machine.

On a UEFI machine, the boot entry loads `\vmlinuz` from the slot
through the kernel's EFI stub, and the kernel command line is stored
in that boot entry's optional data. Nothing else on the machine holds
that command line. When NVRAM is erased, every boot entry and every
command line goes at once, and a complete installed disk is left that
no firmware can start.

This is the outcome that `armProvingBoot` refuses to risk for a
release trial. A trial's fallback is a boot entry in NVRAM, and a
firmware update can erase NVRAM, so no `BootNext` discipline reaches
this case. Two changes close it.

**Every boot writes the slots' boot entries again.** `healBootEntries`
in `init/bootentries.go`, run from `efiActuator.assertProven`, renders
each slot's entry from the GPT facts on the disk and writes back only
what drifted. A machine that boots at all repairs its own boot menu.
Both slots lead `BootOrder`, with the proven slot first, so a firmware
that cannot load one slot's kernel starts the other before it reaches
its own setup menu. The comparison runs before every write, because
NVRAM accepts a limited number of writes and this runs on every boot
and before every reboot. This also finishes the dead-battery case that
the UEFI dialect could only half repair: it could fix a boot order and
could not recreate an entry. The UEFI dialect now has the same healing
duty the GRUB dialect has had since milestone 30.

**The proven slot holds a loader at the default path.**
`init/slotloader.go` writes `\EFI\BOOT\BOOTX64.EFI`, a `loader.conf`
that never waits, and one loader entry holding the same command line
the firmware entry holds. A firmware at its defaults searches each
device for that one path. That search is how an installation stick
boots a machine with no entry for the stick, so a firmware at defaults
finds this loader and boots it. That boot repairs NVRAM, and the boot
after it is ordinary. The loader program is a copy of the
`systemd-bootx64.efi` that every slot already holds as a release
artifact, so this costs no new artifact and no slot budget worth
counting.

The loader is on the proven slot alone. The other slot's copy is
removed only after the proven slot has taken one, so a machine is never
left with neither. A loader on each slot was the first design, and it
is worse twice over. A firmware at defaults takes the first answer it
finds. That answer would be an older release or, at install time, an
empty slot whose loader stops at a menu with no kernel to load. One
answer means such a firmware cannot boot the wrong half of the pair.

The command line is now in two places that must agree, and
`init/grubcfg.go` already has that burden for BIOS machines. One
function, `slotArgs`, renders both, so they cannot disagree. The
loader entry names only the archives its own slot holds, because a
proven slot can hold an older release than the code writing the
loader, and systemd-boot refuses an entry whose initrd is missing.

One durability rule came out of the lab. A rename on FAT is the only
record of a file's name, size, and first cluster, and neither the
per-file fsync nor an fsync of the directory reaches that record. A
FAT directory's entries are in buffers attached to the block device,
not in the directory's own pages. The first promotion drill left slot
B holding a loader entry with the right name, no size, and no data.
The firmware started that loader and stopped at its menu with nothing
to boot. `unix.Sync` after a changed write is what the installer
already does before it announces success, and the loader writer does
it too.

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
  the loader, and slot A's copy went, in that order.
* That machine, proven on slot B, with vendor-default variables,
  reached Ready in 10 seconds on release 2026.07.26-901 from slot B. It
  did not boot the older release on slot A. This is the drill that
  failed before the flush rule above, and it is why the rule exists.
* Reading both slots offline confirmed the placement: slot B holds
  `EFI/BOOT/BOOTX64.EFI` at 135168 bytes with a 253-byte `loader.conf`
  and a 191-byte entry, and slot A keeps its own entry with no loader
  program beside it.
* `make smoke-uefi` and `make smoke-bios` both stayed green, which is
  what proves a BIOS machine's boot chain still reads the same command
  line.

## What must exist before fwupd can

Four pieces of work are missing, and the rest of this plan assumes
them. None is about fwupd. Each is its own change, and each is useful
on its own.

**The machine must report its firmware version.** `FirmwareStatus` in
`machine/status.go` carries the boot mode, `BootCurrent`, `BootNext`,
and `BootOrder`, and no version. `firmwareFacts` in `init/efi.go` reads
those from the variable store. The kernel exposes the board's firmware
version and date under `/sys/class/dmi/id` as `bios_version` and
`bios_date`, on BIOS and UEFI machines alike. One field on
`FirmwareStatus`, read there and printed at boot under the console
parity rule, is what milestone 65 waits for: the metrics
`liken_firmware_info` and `liken_firmware_update_pending` are named in
that plan's table and in `machine-operator/metrics.go` as waiting on
this one. This is the first slice of this milestone, and it needs no
fwupd, no capsule, and no new trust relationship.

**A release must be able to carry an artifact for one machine.**
`fetchRelease` in `machine-operator/fetch.go` lands every artifact in
the release document on every slot, and `releases/bundle.go` sums all
artifacts against one fleet-wide slot size. fwupd, its plugins, and
its LVFS metadata cannot ship in every image; they are the kind of
payload that one machine needs and the fleet does not. Milestone 34's
GPU add-on has the same need, and neither plan names it today. The
shape is an artifact that the release document marks as optional, that
a Machine selects by name, and that the fetcher lands only on the
machine that selected it.

**A feature must be able to run one pod per machine.** Every feature
of kind `FeatureWorkload` in `cluster/features.go` is a cluster
singleton. `flux` is the only one, and the cluster operator seeds it
once. fwupd runs on each machine that updates, with that machine's
devices. The feature vocabulary needs a second workload shape, one
DaemonSet-like pod per machine that opts in, before a `fwupd` slug can
mean anything. The open problem
[firmware-bootorder-persistence](open-problems/firmware-bootorder-persistence.md)
is a fourth prerequisite of a different kind: any firmware trial
trusts that `BootOrder` survives a reset, and the safeguard that
document proposes does not exist yet. The pin of `BootNext` at the
proven slot landed in `efiActuator.assertProven`; the
record-and-compare check did not.

## The fwupd design

Everything below is proposed. It stands on the four prerequisites
above.

### fwupd asks; init writes

The feature gives the fwupd pod no access to
`/sys/firmware/efi/efivars`, the filesystem through which Linux
exposes the variable store. In its place, the pod sees a tmpfs that
looks like efivarfs. The ESRT, the table under
`/sys/firmware/efi/esrt` where the firmware lists each updatable
component and its version, binds read-only, so fwupd's device
discovery still works.

init watches that tmpfs with the inotify machinery that the facts tree
already uses. A write from fwupd is then a request, not an action. init
decides whether that request becomes a real variable write, and when.
After init writes the real variable, it copies the value into the
tmpfs so later reads agree.

The alternative is a scheduled window: give fwupd the real variable
store, and let it apply only while `liken` holds a conductor grant. The
shim is better because it changes the kind of guarantee. A window
holds by timing. It leaves NVRAM writable to a privileged pod at all
times. It needs an answer for a crash in the middle of the window that
leaves a foreign `BootNext` behind. The shim makes init the only writer
by construction.

fwupd's own options do not offer a third way. Its UEFI plugin reads
`EspLocation`, `DisableCapsuleUpdateOnDisk`, and `RequireESPFreeSpace`,
and none of them stops the `BootNext` write.

One measurement settles whether the shim works as written. fwupd
clears the per-file immutable flag before it writes a variable, the
same sequence `writeEFIVar` performs, and `FS_IOC_GETFLAGS` may return
`ENOTTY` on tmpfs. `writeEFIVar` already treats an error from that call
as nothing to clear. The shim depends on fwupd doing the same.

### One reboot applies one change

The UEFI specification has the firmware read `\EFI\UpdateCapsule` only
from the ESP on the device named in the active boot option. That
option is `BootNext` when it is set and `BootOrder` otherwise. `liken`
controls that entry completely: `registerSlotEntries` puts both slots
at the head of `BootOrder`, and `efiActuator.assertProven` keeps the
proven slot first.

So the rule is that a capsule and a release trial are never armed for
the same reboot. This keeps the boot device known, and it keeps the
proving verdict attributable. A reboot that changes two things gives a
failure no owner.

`armProvingBoot` already supports the deferral. It has five early
returns that leave the staged record untouched, and the settle path
reads that state as awaiting its proving reboot. A staged capsule is
one more reason to take a path that already exists. The deferral costs
one reboot.

A capsule left on a slot that stops being proven is inert until some
later boot from that slot applies it, as a firmware change nobody
asked for. init sweeps `\EFI\UpdateCapsule` from any slot that is not
the proven slot. Milestone 37 applies that rule to partitions and
milestone 42 applies it to a retracted feature's work.

### The ESP that the firmware reads

fwupd finds an ESP on its own only when one is mounted at `/boot/efi`,
`/boot`, or `/efi`, or when UDisks reports one. `liken` has none of those
mounts and no UDisks, so detection fails. That is the safe failure.

The serialization rule settles which ESP is correct: with no slot
switch pending, it is always the running slot's. Slots already mount
at fixed role paths (`init/grubactuator.go`), so init binds the running
slot at one constant path and sets `EspLocation` to it. fwupd's
configuration then never changes, and each boot binds that path to the
correct slot.

Where the firmware supports capsule-on-disk, prefer it. fwupd then
writes `OsIndications` instead of `BootNext`, and the seam is smaller.
Do not depend on it. fwupd itself calls that path uncommon.

### The conductor already fits

`wantsTurn` in `cluster-operator/rollout.go` matches the `AwaitingTurn`
reason on any condition. A `FirmwareConverged` condition that sets that
reason joins the rollout conductor with no change to the conductor.
Firmware updates then inherit the budget, the one-leader floor, the
drain, and stall detection. fwupd never reboots a machine itself. The
grant owns the reboot, the same as every other staged change.

The plan does not yet say which process computes `FirmwareConverged`.
The machine operator sets `AwaitingTurn` generically in `converge.go`,
so it is the natural owner, and it would read the ESRT through the
facts tree.

### Proving a change that the firmware applied

Read the evidence from the machine, not from fwupd. After the boot,
the ESRT reports each component's version, and the firmware writes
its own result into a capsule status variable. Proven means the ESRT
reports the declared target and the machine joined its cluster. Both
facts belong in the facts tree and in Machine status, by the console
parity rule.

A firmware update has no second slot and cannot be undone, so
`RejectedLastBoot` has nothing to offer. The preconditions must be
stricter than a release's, in the `canArmTrial` pattern: refuse when
the default-path loader is absent, when `chooseBootActuator` returned
`noActuator`, when a release is staged for the other slot, and when
the ESP lacks free space.

One risk has no precondition that can catch it in advance. An update
that turns Secure Boot on, or resets its keys, stops `liken`'s unsigned
`vmlinuz` from loading, and the default-path loader is unsigned too.
No boot order repairs that. The answer is the hardening tier's signed
releases and UKIs, so until those exist the feature must state the
risk plainly.

## What the lab can measure

Almost all of this runs under QEMU, because every decision above is a
boot-order or filesystem question, not a hardware one. Two drills need
no fwupd, no capsule, and no ESRT:

* Write a capsule directory and a competing `BootNext` by hand. This
  drills the serialization rule, the false-rejection path through
  `settleSystemRelease`, and the sweep.
* Mount the tmpfs and run fwupd against it. This drills the shim,
  including the immutable-flag question.

One question needs more than stock OVMF. Whether the firmware consumes
a staged capsule needs an ESRT and an updatable firmware device. That
means OVMF built with edk2's `FmpDevicePkg` and `EsrtFmpDxe`, or real
metal. That question is independent of every decision above, so it
does not gate the design.

Real vendor targets stay on metal. NIC NVRAM, SSD firmware, and docks
need their own buses, and a guest cannot update the firmware image
that its host owns.

## Until this milestone exists

A deployment can run fwupd as a privileged workload today, with a cost
to `liken`. Such a pod writes `BootNext`, so it can overwrite a staged
release trial, and it can lose its own update to `liken`'s arming. A
deployment that runs fwupd now should do it with no release staged.

An update that resets NVRAM no longer strands the machine, because the
proven slot holds a loader that a firmware at its defaults finds. An
update that turns Secure Boot on still strands it, because `liken`'s
`vmlinuz` and that loader are both unsigned. The answer to this one is
the hardening tier's signed releases and UKIs.
