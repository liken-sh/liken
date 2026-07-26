# Updating the machine's own firmware

Milestone 33 — Proposed. It would add fwupd as a feature slug, so a
firmware update would use the rolling-reboot orchestration that liken
already has, and init would stay the only writer of the boot chain.

The firmware that milestone 32 ships is payload that the kernel
consumes: modules, driver blobs, and microcode, all inert bytes. This
milestone covers the other direction, an update to the firmware that
the machine itself runs: UEFI, NIC NVRAM, and SSD and dock firmware.
fwupd and the Linux Vendor Firmware Service own this area. The work
waits for experience with bare metal from milestone 32.

fwupd is not inert payload. It is an agent with a daemon's memory cost
and a live trust relationship with LVFS, whose vendor-signed downloads
sit outside liken's own digest chain. Its job also reaches into the
boot chain that liken guards most carefully. It writes BootNext, it
creates a boot entry of its own, it writes OsIndications, and it
stages an EFI capsule on an EFI system partition. Those are the writes
that init owns, for the reasons proving.go gives: the store on disk is
the authority, and the machine plane is what talks to the firmware.

The design below keeps that rule by removing fwupd's ability to break
it, rather than by asking fwupd to take turns.

## The fallback that a firmware update can erase

This is the prerequisite, and it is not really about fwupd.

A UEFI machine's boot entry loads `\vmlinuz` from the slot through the
kernel's EFI stub, and the kernel command line lives in the boot
option's optional data (init/install.go). Nothing else on a UEFI
machine holds that command line. No installed slot carries
`\EFI\BOOT\BOOTX64.EFI`, because systemd-boot ships for the install
stick alone. `installBootEntries` runs only from the installer, so no
ordinary boot rewrites an entry. `assertProven` repairs BootOrder, but
it gives up when no entry answers to the slot (init/efiactuator.go).

Some firmware resets NVRAM to defaults as part of an update. On a
liken machine that erases every boot entry and every command line at
once. The firmware then has nothing to boot, and recovery needs an
install stick and a person.

This is the outcome `armProvingBoot` refuses to risk for a release. A
release trial's fallback is a boot entry in NVRAM, and a firmware
update can erase NVRAM, so liken's existing guarantee does not reach
this case. No amount of BootNext discipline fixes it. Two changes
close it, and both stand on their own merit.

First, re-register the slots' boot entries on every boot. A boot that
finds its entries missing rewrites them from the storage reconcile's
partition facts. This also finishes the dead-NVRAM-battery case that
init/efiactuator.go already names, where today liken can repair a boot
order but cannot recreate a boot entry.

Second, write a default-path loader on each slot:
`\EFI\BOOT\BOOTX64.EFI` and the loader entries that carry the command
line. A firmware at defaults then finds something to boot. liken
already builds systemd-bootx64.efi and already writes loader entries
for the install stick, so this is reuse. The firmware picks whichever
slot it enumerates first, so a machine proven on slot B costs one boot
of the older release before the re-registration repairs NVRAM and the
next boot lands correctly. `provingWatch` already accepts that same
trade. The real cost is duplication: the command line would live in
two places that must agree, which is the burden init/grubcfg.go
already carries for BIOS machines.

## Requests, not writes

The feature would give the fwupd pod no access to
`/sys/firmware/efi/efivars`. A tmpfs in that place mimics efivarfs,
and `/sys/firmware/efi/esrt` binds read-only so device discovery still
works. init watches the tmpfs with the inotify machinery that the
facts tree already uses. A write from fwupd is then a request, not an
action. init decides whether and when to honor it, and after init
writes the real variable it copies the value into the tmpfs so later
reads agree.

The alternative is a scheduled window: give fwupd the real variable
store, and let it apply only while liken holds a conductor grant. The
shim is better because it changes the kind of guarantee. A window
holds by timing, it leaves NVRAM writable to a privileged pod at all
times, and it needs an answer for a crash mid-window that leaves a
foreign BootNext behind. The shim makes init the only writer by
construction.

The plugin's own options do not offer a third way. It reads
EspLocation, DisableCapsuleUpdateOnDisk, and RequireESPFreeSpace, and
none of them stops the BootNext write.

One measurement decides whether the shim works as written. fwupd
clears the per-file immutable flag before it writes a variable, the
same dance writeEFIVar performs, and `FS_IOC_GETFLAGS` may return
ENOTTY on tmpfs. The shim depends on fwupd reading that as nothing to
clear rather than as an error.

## One reboot carries one change

The UEFI specification has the firmware read `\EFI\UpdateCapsule` only
from the EFI system partition on the device named in the active boot
option, which is BootNext when it is set and BootOrder otherwise.
liken controls that entry completely: `installBootEntries` puts both
slots at the head of BootOrder, and `assertProven` keeps the proven
slot first.

So the rule is that a capsule and a release trial are never armed for
the same reboot. This keeps the boot device known, and it keeps the
proving verdict attributable, because a reboot that changes two things
gives a failure no owner.

`armProvingBoot` already supports the deferral. It has four early
returns that leave the staged record untouched, and the settle path
reads that state as awaiting its proving reboot. A staged capsule is
one more reason to take a path that already exists, and the deferral
costs one reboot.

A capsule left on a slot that stops being proven is inert, and it
applies on some later boot from that slot as a firmware change nobody
asked for. init would sweep `\EFI\UpdateCapsule` from any slot that is
not the proven slot. Milestone 37 applies that rule to partitions and
milestone 42 applies it to a retracted feature's work.

## The ESP that the firmware reads

fwupd autodetects an ESP mounted at `/boot/efi`, `/boot`, or `/efi`
when UDisks is available. liken has none of those mounts and no
UDisks, so autodetection fails, which is the safe failure rather than
the dangerous one.

Serialization settles which ESP is correct: with no slot switch
pending, it is always the running slot's. init would bind-mount that
slot at a stable path and set EspLocation to the constant. fwupd's
configuration then never changes, and init decides each boot what the
path means.

Where the firmware supports capsule-on-disk, prefer it, because fwupd
then writes OsIndications instead of BootNext and the seam is smaller.
Do not depend on it. fwupd itself calls that path uncommon.

## The conductor already fits

`wantsTurn` matches the AwaitingTurn reason on any condition
(cluster-operator/rollout.go). A FirmwareConverged condition that sets
that reason joins the rollout conductor with no change to the
conductor, and firmware updates inherit the budget, the one-leader
floor, the drain, and stall detection. fwupd would never reboot a
machine itself. The grant owns the reboot, the same as every other
staged change.

## Proving a change that the firmware applies

Read the evidence from the machine, not from fwupd. After the boot,
`/sys/firmware/efi/esrt` reports each component's version, and the
firmware writes its own result into a capsule status variable. Proven
means the ESRT reports the declared target and the machine joined its
cluster. Both facts belong in the facts tree and in Machine status, by
the console parity rule.

A firmware update has no second slot and cannot be undone, so
RejectedLastBoot has nothing to offer. The preconditions must be
stricter than a release's, in the `canArmTrial` pattern: refuse when
the default-path loader is absent, when `chooseBootActuator` returned
noActuator, when a release is staged for the other slot, and when the
ESP lacks free space.

One risk has no precondition that can catch it in advance. An update
that enables Secure Boot, or resets its keys, stops liken's unsigned
vmlinuz from loading, and the default-path loader is unsigned too. No
boot order repairs that. The answer is the hardening tier's signed
releases and UKIs, so until those exist the feature must state the
risk plainly.

## What the lab can measure

Almost all of this runs under QEMU, because every decision above is a
boot-order or filesystem question rather than a hardware one. Three
drills need no fwupd, no capsule, and no ESRT:

* Delete the OVMF variable store and boot. This drills NVRAM loss and
  both repairs.
* Write a capsule directory and a competing BootNext by hand. This
  drills the serialization rule, the false-rejection path through
  `settleSystemRelease`, and the sweep.
* Mount the tmpfs and run fwupd against it. This drills the shim,
  including the immutable-flag question.

One question needs more than stock OVMF. Whether the firmware consumes
a staged capsule needs an ESRT and an updatable firmware device, which
means OVMF built with edk2's FmpDevicePkg and EsrtFmpDxe, or real
metal. That question is independent of every decision above, so it
does not gate the design.

Real vendor targets stay on metal. NIC NVRAM, SSD firmware, and docks
need their own buses, and a guest cannot update the firmware image
that its host owns.

## Until this milestone exists

A deployment can run fwupd as a privileged workload today, but not
without consequence for liken. Such a pod writes BootNext, so it can
overwrite a staged release trial, and it can lose its own update to
liken's arming. It can also arm a firmware update on a machine that
has no fallback loader. A deployment that runs fwupd now should do it
with no release staged, and should expect to recover a machine with an
install stick.
