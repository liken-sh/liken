# Updating the machine's own firmware

Milestone 33. Proposed, except for the boot-path work below, which is
built. It would add fwupd as a feature slug, so a firmware update would
use the rolling-reboot orchestration that liken already has, and init
would stay the only writer of the boot chain.

The firmware that milestone 32 ships is payload that the kernel
consumes: modules, driver blobs, and microcode, all inert bytes. This
milestone covers the other direction, an update to the firmware that
the machine itself runs: UEFI, NIC NVRAM, and SSD and dock firmware.
fwupd and the Linux Vendor Firmware Service own this area. The work
waits for experience with bare metal from milestone 32.

fwupd is not inert payload. It is an agent with a daemon's memory cost
and a live trust relationship with LVFS. LVFS's vendor-signed downloads
are outside liken's own digest chain. Its job also writes to the
boot chain that liken controls most carefully. It writes BootNext, it
creates a boot entry of its own, it writes OsIndications, and it
stages an EFI capsule on an EFI system partition. Those are the writes
that init owns, for the reasons proving.go gives: the store on disk is
the authority, and the machine plane is what talks to the firmware.

The design below keeps that rule by removing fwupd's ability to break
it, and not by scheduling fwupd's writes into turns.

## The fallback that a firmware update can erase

This part is built. It is not really about fwupd, and it is worth doing
on its own, so it landed ahead of the rest of this milestone.

A UEFI machine's boot entry loads `\vmlinuz` from the slot through the
kernel's EFI stub, and the kernel command line is in the boot
option's optional data. Nothing else on a UEFI machine holds that
command line. Some firmware resets NVRAM to defaults as part of an
update, a dead NVRAM battery does the same, and so does the setup
menu's own "load defaults". Any of those erases every boot entry and
every command line at once, and leaves a complete installed disk that
no firmware can start.

This is the outcome `armProvingBoot` refuses to risk for a release. A
release trial's fallback is a boot entry in NVRAM, and a firmware
update can erase NVRAM, so no amount of BootNext discipline reaches
this case. Two changes close it.

First, every boot writes the slots' boot entries again
(init/bootentries.go). `assertProven` renders each entry from the GPT
facts on the disk and writes back only what drifted, so a machine that
boots at all repairs its own boot menu. Both slots lead BootOrder with
the proven slot first, so a firmware that cannot load one slot's kernel
starts the other half of the pair before it reaches its own setup menu.
The comparison runs before every write, because NVRAM accepts a limited
number of writes and this now runs on every boot and before every
reboot. This also finishes the dead-battery case that the UEFI dialect
could only half repair: it could fix a boot order and could not
recreate a boot entry. The UEFI dialect now has the same healing
duty the GRUB dialect has had since milestone 30.

Second, the proven slot holds a default-path loader
(init/slotloader.go): `\EFI\BOOT\BOOTX64.EFI`, a `loader.conf` that
never waits, and one loader entry holding the same command line the
firmware entry holds. A firmware at defaults searches each device for
that one path. That search is how an installation stick boots a machine
with no boot entry for the stick, so a firmware at defaults finds this
loader and boots it. That boot
then repairs NVRAM, and the boot after it is ordinary. The loader
program is a copy of the `systemd-bootx64.efi` that every slot already
holds as a release artifact, so this costs no new artifact and no
slot budget worth counting.

The loader is on the proven slot alone, and the other slot's copy is
removed only after the proven slot has taken one, so a machine is never
left with neither. Putting one on each slot was the first design, and
it is worse twice over. A firmware at defaults takes the first answer
it finds. That answer would be an older release or, at install time, an
empty slot whose loader would stop at a menu with no kernel to load.
One answer means such a firmware cannot boot the wrong half of the pair.

The command line is now in two places that must agree, and
init/grubcfg.go already has that burden for BIOS machines. One function
renders both, so they cannot disagree. The loader entry names only the
archives its own slot holds, because a proven slot can hold an older
release than the code writing the loader, and systemd-boot refuses an
entry whose initrd is missing.

One durability rule came out of the lab. A rename on FAT is the only
record of a file's name, size, and first cluster, and neither the
per-file fsync nor an fsync of the directory reaches that record. A FAT
directory's entries are in buffers attached to the block device rather
than in the directory's own pages. The first promotion drill left slot
B holding a loader entry with the right name, no size, and no data. The
firmware started that loader and stopped at its menu with nothing to
boot. `unix.Sync` after a changed write is what the installer already
does before it announces success, and the loader writer does it too.

## What the lab measured

Every drill ran on node-1 under OVMF, at the 1 GB disk-boot size.
`make -C dev-cluster reset-nvram` copies the vendor-default variable
store over a guest's, which is what an update that resets NVRAM does to
a real board.

* A machine proven on slot A, with vendor-default variables, reached
  Ready in 9 seconds. It booted through `\EFI\BOOT\BOOTX64.EFI`, and
  that boot wrote both entries and put slot A at the head of BootOrder.
  The next boot came through its own entry and printed nothing.
* An upgrade to 2026.07.26-901 moved the proven slot from A to B in 46
  seconds. At the promotion, BootOrder moved to slot B, slot B took the
  loader, and slot A's copy went, in that order.
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

## Requests, not writes

The feature would give the fwupd pod no access to
`/sys/firmware/efi/efivars`. A tmpfs in that place mimics efivarfs,
and `/sys/firmware/efi/esrt` binds read-only so device discovery still
works. init watches the tmpfs with the inotify machinery that the
facts tree already uses. A write from fwupd is then a request, and not
an action. init controls whether that request becomes a real write, and
when. After init writes the real variable it copies the value into the
tmpfs so later reads agree.

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

One measurement settles whether the shim works as written. fwupd
clears the per-file immutable flag before it writes a variable, the
same sequence writeEFIVar performs, and `FS_IOC_GETFLAGS` may return
ENOTTY on tmpfs. The shim depends on fwupd reading that as nothing to
clear rather than as an error.

## One reboot applies one change

The UEFI specification has the firmware read `\EFI\UpdateCapsule` only
from the EFI system partition on the device named in the active boot
option. That option is BootNext when it is set and BootOrder otherwise.
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
configuration then never changes, and each boot init binds that path to
the correct slot.

Where the firmware supports capsule-on-disk, prefer it, because fwupd
then writes OsIndications instead of BootNext and the seam is smaller.
Do not depend on it. fwupd itself calls that path uncommon.

## The conductor already fits

`wantsTurn` matches the AwaitingTurn reason on any condition
(cluster-operator/rollout.go). A FirmwareConverged condition that sets
that reason joins the rollout conductor with no change to the
conductor. Firmware updates then inherit the budget, the one-leader
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
boot-order or filesystem question rather than a hardware one. Two
drills need no fwupd, no capsule, and no ESRT:

* Write a capsule directory and a competing BootNext by hand. This
  drills the serialization rule, the false-rejection path through
  `settleSystemRelease`, and the sweep.
* Mount the tmpfs and run fwupd against it. This drills the shim,
  including the immutable-flag question.

One question needs more than stock OVMF. Whether the firmware consumes
a staged capsule needs an ESRT and an updatable firmware device. That
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
liken's arming. A deployment that runs fwupd now should do it with no
release staged.

An update that resets NVRAM no longer strands the machine, because the
proven slot holds a loader that a firmware at its defaults finds. An
update that turns Secure Boot on still strands it, because liken's
vmlinuz and that loader are both unsigned. The answer to this one is
the hardening tier's signed releases and UKIs.
