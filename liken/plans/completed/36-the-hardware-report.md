# The hardware report

Milestone 36. Completed. The installer stick gets a report boot that
proposes a machine manifest, and every attended boot ends at a held
console.

Milestone 35 put liken on real hardware for the first time. That
machine showed three faults:

* The vendored kernel builds the virtio drivers in, so a lab guest
  never loads a storage module or a network module. The lab does not
  test the module path that a real controller needs.
* A person stands at an install, but the installer's terminal states
  were made for unattended lab guests: a message for one second, then
  power off.
* A new machine gives no answer to the questions of which modules,
  which interface names, and which disk paths it needs. To find the
  answers took three install cycles.

This milestone adds a report boot and makes the installer print what
the operator must know. It has five parts.

## The three-entry menu

The installer stick's menu grows from one entry for each machine to
two, plus one entry for the stick itself:

    install as liken-1
    wipe and reinstall as liken-1
    liken hardware report

The first entry is `liken.install`, unchanged. The second is
`liken.reinstall`, which already exists in init but was reachable only
with systemd-boot's "e" key. The third is new: `liken.report`, a boot
that changes nothing on the machine.

The menu runs with `timeout menu-force`, so it waits until a person
makes a pick. That is also the consent for the reinstall entry. A
person who picks "wipe and reinstall as liken-1" at the keyboard gives
the same instruction that the hand-edited `liken.reinstall` word gave.

The report entry has no `liken.machine=` identity, because the
report describes the hardware and not a machine in the deployment. The
two entries for each machine sort together, and the report entry sorts
last.

## The hardware report

The report boot gives the operator the values the manifest needs. It
writes one file, `hardware-report.yaml`, on the root of the
installation stick. The file is a proposed machine manifest with full
comments. It is a valid `Machine` document. Its `spec.modules` names
the drivers this hardware needs. Its storage section lists every disk.
Its comments give the evidence: the PCI device each module claims, each
disk's size, model, and device path, and each network interface's name,
MAC, and link state.

The report does not guess the interface names. It loads the drivers it
recommends and reads what appears. A name is real only after the driver
binds: `eth0` does not exist until `r8169` loads. The modules come from
the payload the stick already holds. The install boot mounts
`liken.sqfs` for its module tree, and the report boot does the same. A
module load changes only RAM, so the report changes nothing on the
machine.

The recommendation follows soft dependencies. The testbed's NIC needed
`realtek` loaded before `r8169`, a `softdep` relation that
`modules.dep` does not record. The report reads each module's `softdep`
information and recommends the full ordered list, so the proposal says
`[realtek, r8169]` and not `r8169` alone.

The last step prints the full proposal to the console with a note that
says where the file was written, then holds. When the operator presses
Enter, the machine reboots. A new machine needs three boots: the
report, then an edit of `machine.yaml` at a desk, then the install.

## Attended boots end at a held console

The rule is that a menu pick makes a boot attended, and that every
terminal state of an attended boot ends at a held console. The person
showed they were present when they picked the entry.

Before this milestone, a failed install held the console, but a
successful install printed its message and powered off in the same
second. On real hardware, a dark screen and a dead machine can mean
"done" or "never started".

Every terminal state of the three menu entries now holds:

* Install and reinstall, success: "installed to slot A; remove the
  stick, then press Enter to power off; the next power-on boots from
  the disk." The instruction to remove the stick is necessary, because
  the stick is first in the boot order and a power-on with the stick
  still in it goes back to the menu.
* Install and reinstall, failure: already holds; unchanged.
* Report: "this report was written to the stick as
  hardware-report.yaml; press Enter to reboot."

Unattended boots keep their abrupt behavior: boots from disk, boots of
an upgrade slot, and every install that a script or a Makefile started.
Nobody watches these boots, and `panic=10` with the fall-back slot is
their recovery.

The menu is what separates the two kinds, so the menu states it. Each
entry includes `liken.attended`, and only that word makes init hold.
The boot words cannot state the meaning, because anything can write
them.
The liken.sh image build boots `liken.install` in QEMU with its serial
port pointed at a file, and a PXE server boots it with nobody in the
room. Both power off as before.

A headless server also powers off. A test of the console device cannot
give this result, because `/dev/console` opens on a machine with no
keyboard and only the read blocks.

## Softdeps in the unclaimed-hardware report

The running node's unclaimed-hardware report found the testbed's NIC
driver by modalias and said "declare r8169 in spec.modules". That
advice was correct and incomplete. Without `realtek` declared first,
the NIC binds to the generic PHY. The report now reads the same softdep
information the hardware report uses, and its advice names the full
ordered list.

The declared-module loader stays explicit. It loads what a manifest
declares, in the declared order, and nothing else. liken has no udev
for the same reason: the manifest is the whole truth about what a
machine runs. The softdep information changes the advice, not the
loader.

## The lab boots the same hardware classes

All three hardware faults have one shape. The vendored kernel builds
virtio in (`CONFIG_VIRTIO_BLK=y`), so the lab never loads a storage
module or a network module, and a missing driver is not visible until a
real machine boots. QEMU can present the same hardware classes that a
real machine has: an AHCI SATA controller (`CONFIG_SATA_AHCI=m`) and an
e1000 NIC (`CONFIG_E1000=m`).

A new smoke guest boots with its disks on AHCI and its uplink on e1000,
and its manifest declares `e1000` in `spec.modules`. That is the flow a
real operator uses. The guest shows that the boot-path storage modules
load, that the storage wait outlasts link training, and that the
declared-module path brings a real NIC up. The smoke also boots the
report entry and checks that the proposal file on the stick names
`e1000` and the AHCI disk.

## The manual

The install guide becomes the procedure for a first machine: boot the
report, read `hardware-report.yaml`, write the manifest, boot the
install. Each terminal state's held message appears in the guide, so
the page shows exactly what the operator sees. The reinstall entry gets
its place in the guide, for the second install of the same machine.

## Slices

1. Attended terminal states: the hold moves from the failure paths to
   every terminal state of an attended boot, with the messages above.
2. Softdep reading in init, and the unclaimed-hardware advice names the
   full ordered list.
3. The report boot: `liken.report` in init, the proposal file, the
   console print, and the hold.
4. The menu: two entries for each machine plus the report entry, in
   `image/stick.go`.
5. The parity smoke guest in the dev-cluster lab.
6. The manual: the install guide rewritten around the three-boot flow.
