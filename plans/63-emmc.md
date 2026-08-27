# eMMC storage

A machine whose only disk is an eMMC module installs and boots the
same way a SATA or NVMe machine does.

## The problem

liken cannot see, install to, or boot from an eMMC disk. The gap has
three parts:

* **The report is blind to the controller.** `pciClassWord`
  (`hardware/names.go`) maps PCI base class `08` to the word
  `system`. An SD/eMMC host controller is class `0805`, inside base
  class `08`. `reportableClass` (`init/report.go`) accepts only
  `storage`, `network`, and `mass-storage`, and `claimableClass`
  drops `system` too. So an undriven eMMC controller gets no driver
  recommendation and no unclaimed listing. It is invisible in the
  proposal and in the warnings.
* **The boot archive has no eMMC driver.** `image/boot-modules.conf`
  carries `ahci` and `nvme` as the only real disk controllers, and
  boot-disk drivers cannot come from `spec.modules`, because the
  install claims disks long before declared modules load. The system
  image already ships the modules (`sdhci`, `sdhci-pci`,
  `sdhci-acpi`, `mmc_block`); the boot archive does not.
* **The by-id tree gives an mmc disk no identity name.**
  `init/diskids.go` builds `ata-` and `nvme-` names only, so a
  manifest could name an eMMC only by kernel path.

## The field case

`stick-1` at 44 Stony Point: an Apollo Lake HDMI stick PC, meant to
join a testbed cluster. Its hardware report proposed `storage: {}`
and said no disk can carry a role, while a 64GB eMMC sat in the
machine with a working Windows install on it.

The EFI shell on the install stick confirmed the hardware:

* Three class `0805` controllers, shown as "Base System Peripherals -
  SD Host controller": `8086:5ACA` (the SD card slot), `8086:5ACC`
  (the eMMC), and `8086:5AD0` (SDIO).
* `map -r` listed the eMMC's GPT through `8086:5ACC`: an EFI
  partition, a 16MB MSR, a ~61GB Windows partition, and a ~1GB
  recovery partition. The firmware reads the disk fine, so boot
  entries and slot mounts have nothing new to solve at that layer.
* The eMMC's two hardware boot areas appeared as their own block
  devices, which the kernel will also expose (`mmcblk0boot0`,
  `mmcblk0boot1`, and possibly `rpmb`).

## The design

Four changes, three of them additive. The one that is not additive
gets the tightest guard, because it runs for every device on every
machine.

### The class word

`pciClassWord` maps a PCI class code to the word the report and the
device listings use. Today it reads only the base class, so `0805`
reads as `system` and the report drops it. The fix adds one subclass
row before the base-class switch: exactly `0805` reads as `storage`.
Every other subclass of base class `08` still reads as `system`,
because those devices are the machine's own plumbing and the word is
correct for them.

The guard is a test that locks the word for every class code the
mapping answers today. A change to any existing word fails the test,
so the diff for this milestone shows one added row and nothing moved.

With the word corrected, the report's filters need no change of
their own: `reportableClass` already accepts `storage`, and
`claimableClass` already rejects it. On a liken image the boot
archive binds the controller before the report runs, so the disk
behind it joins the proposal directly, the same outcome a SATA
controller gets. The recommendation path matters for an `0805`
controller the boot archive does not cover: that one now earns a
warning naming the module an image rebuild must add, where before it
was invisible.

### The boot archive

`image/boot-modules.conf` gains the eMMC stack: `sdhci`, `sdhci-pci`
for controllers that enumerate over PCI, `sdhci-acpi` for boards
that enumerate them through ACPI, and `mmc_block`. Boot-disk drivers
cannot come from `spec.modules`, because the install claims disks
before declared modules load; that is the same reason `ahci` and
`nvme` are in this list. The system image already ships these
modules, so the cost is boot-archive bytes alone, and the slot
budget guard prices it.

### The identity name

`init/diskids.go` builds `mmc-<name>_<serial>` beside the `ata-` and
`nvme-` names, from the same sysfs attributes udev's rules read: the
card's `name` and `serial`, published on the mmc device under the
block directory. The CID a card answers with lives in the card's own
controller, not in its flash, so the name survives an image write
the way the `ata-` names do.

The by-path name is smaller than the rule elsewhere. udev's
`path_id` has no mmc handler at any version, and its PCI branch
alone does not qualify a block device for an `ID_PATH`, so udev
itself publishes no by-path link for a card on a PCI `sdhci`
controller. liken reproduces udev's names rather than inventing
ones no other tool agrees with, so a PCI-attached eMMC carries its
by-id name only, and milestone 44's two-names rule has this one
documented exception. A platform-enumerated host (what
`sdhci-acpi` binds) does get the `platform-...` name udev builds
for it.

### The disk walk

`discoverBlockDevices` keeps any block device with a `device`
symlink, and an eMMC module presents three that are not disks: the
two hardware boot areas (`mmcblk0boot0`, `mmcblk0boot1`) and, on
kernels that still show it as a block device, the RPMB area. No role
may land on them: the boot areas are where a board's firmware reads
its own early stages, and RPMB is an authenticated mailbox, not
storage. The marker is structural: the mmc block driver registers
each hardware area as a child of the data area's own block device,
so an area's `device` link leads into the block subsystem, where a
real disk's leads to the bus device that carries it. That one check
covers every kernel: from 3.8 to 4.14 RPMB's block device had the
same parent as the boot areas, and from 4.15 on RPMB is a character
device that never reaches the walk. Only the mmc driver builds the
shape, so every other machine's walk is byte-identical.

Partition device names need no new code but do need the audit: mmc
partitions are `mmcblk0p1`, the `p` form NVMe already uses, so every
path that builds a partition name from a disk name must already
handle the pattern, and the drill proves it.

### The lab shape

`HARDWARE` already selects the guest's controller classes per drill:
`virtio` is the fast paravirtual shape, `metal` is AHCI and e1000.
`emmc` becomes the third shape: two `sdhci-pci` controllers, one
fronting one `emmc` card and one with an empty slot (QEMU emulates
eMMC natively from version 9.1, boot partitions and RPMB included),
with every storage role on the one disk, because that is how an
eMMC machine is actually built: a soldered module beside a card
slot nobody filled. The empty controller drills the bind every
fielded machine hits on every boot.
The shape gets its own machine manifest the way the metal shape has
one, and a `smoke-emmc` drill beside `smoke-uefi` and `smoke-bios`
keeps the path proven.

One risk sits outside liken: whether OVMF's firmware ships an SD/MMC
host driver for QEMU's controller. Real firmware reads its own eMMC
(the field case proved it on `stick-1`), but if OVMF cannot, the
from-disk boot leg of the drill proves on metal instead, and the lab
still drills the report, the install, and the walk.

`smoke-emmc` is a local drill, like `smoke-hardware`, not a CI step.
CI's runner is Ubuntu 24.04 with QEMU 8.2, and the `emmc` device
arrived in QEMU 9.1. When the runner's QEMU reaches that, the drill
joins the build workflow beside the other two smokes.

## Decided against

* The SD card slot and the SDIO controller propose nothing. A card
  in a slot leaves with the person, like the install stick, and SDIO
  is a radio bus, not storage. This is enforced, not assumed: the
  boot archive's `sdhci-pci` binds card slots too, a card would sort
  first in the candidate list (`mmcblk0` before `nvme0n1`), and
  without the exclusion a forgotten 32GB card would be proposed as
  the system disk ahead of the machine's real disk. The card's own
  `type` attribute (`SD` against an eMMC's `MMC`) is the marker, the
  report says what it saw and why it proposed nothing, and a spec
  that names an SD card explicitly still installs.
* No generic subclass table. Exactly one subclass row exists until a
  second device earns one, because every row in that table is a
  behavior change for every machine.

## What the lab measured

The whole milestone drilled in QEMU on 2026-08-26, on the `emmc`
hardware shape: two `sdhci-pci` controllers, one 6G `emmc` card with
4 MiB boot areas and a 128 KiB RPMB area behind the first, nothing
behind the second.

* The guest presents all four device kinds: `mmcblk0`,
  `mmcblk0boot0`, `mmcblk0boot1`, and `mmcblk0rpmb` (a character
  device on this kernel).
* The install claimed the card and formatted all nine roles onto
  `mmcblk0p1..p9` in about 200 seconds of guest time; QEMU's emmc
  emulation writes at about 32 MiB per second, which sets the
  drill's 420-second bounds.
* The disk walk lists `/dev/mmcblk0` and nothing else, and the
  by-id tree carries `mmc-QEMU___0xdeadbeef` for it.
* OVMF reads the card both ways: it loads the installed slot entry
  from the card's GPT, and with a vendor-default varstore it
  enumerates the card on its own and takes the fallback path. The
  from-disk leg therefore drills in the lab.
* The first from-disk boot exposed a race this plan did not
  predict: mmc card detection runs on a workqueue, so the card
  attached about 50 ms after the early slot search gave up, and the
  boot continued on rootfs. `findSystemImage` now waits on the slot
  path, bounded at 10 seconds, polling at 50 ms; only a boot whose
  `liken.slot=` parameter names a slot ever waits, and a first
  search that hits costs nothing.
* The card's kernel name is not stable. The same card came up as
  `mmcblk0` on one boot and `mmcblk1` on the next, and the machine
  converged anyway, because roles resolve by GPT partition name
  after the claim. The drill's manifest names the card by its by-id
  identity for exactly this reason, which also drills the
  `mmc-` name end to end.
* `smoke-emmc` passes end to end: install, from-disk boot under
  OVMF, node-1 Ready in about 90 seconds, with the walk's skip and
  the nine on-card roles asserted from the machine's own status.

## Noted for the fleet rollout

Every live machine carries a class `0805` controller with no card
behind it, and this milestone makes all of them bind `sdhci-pci` at
every boot where before the device sat unclaimed. The drill's guest
carries a second, empty `sdhci-pci` controller so the no-card bind
is drilled beside the card-present one, and the testbed roll proves
it on Intel metal. One caution stands for the NUC rotation: two
NUCs carry BayHub `8086:9DF5` controllers that fall to `sdhci-pci`'s
generic class match rather than a vendor quirk path, and a generic
SDHCI reset on a quirky controller is a known source of boot-time
timeouts. Watch the first NUC's boot log at that fleet's bump.
