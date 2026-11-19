# The eMMC guest

This guest boots `node-1` on the hardware of a stick PC, whose only
disk is an eMMC module. The lab's other guests have several disks, and
each one is a plain block device. An eMMC card is different. One card
holds every storage role, the boot slots and the pod storage. The
kernel also shows the card's two hardware boot areas (`mmcblk0boot0`,
`mmcblk0boot1`) as block devices, and its RPMB area as a character
device, and no role may use any of them.

The guest exists because of `stick-1`, an Apollo Lake HDMI stick PC
with a 64GB eMMC. `liken` could not see its eMMC. The PCI class of the
controller read as `system`, the hardware report recommended no
driver, and it proposed no storage while a working disk was in the
machine. `plans/completed/63-emmc.md` has the design.

The guest has the same hardware as a stick PC: two `sdhci-pci` host
controllers, one with an `emmc` card and one with an empty slot. The
kernel drives them with `sdhci`, `sdhci-pci`, and `mmc_block`, which
are in the boot archive. The drill checks that:

* The boot archive loads the eMMC modules before the machine looks
  for its disk.
* The disk walk keeps the card's data area and skips the boot areas.
  `smoke.sh` reads `status.hardware.blockDevices` and fails when it
  lists a hardware area, lists no mmc disk, or lists two.
* A machine with one disk claims and formats every storage role on
  the card it booted from. `smoke.sh` checks that each of the nine
  roles in `status.storage` is a partition of the card.
* A controller with no card binds without error. Every `liken`
  machine in the field meets this case on every boot.

## What is here

`cluster.yaml` is a symlink to the dev cluster's own cluster document,
as in the hardware-parity guest in `../hardware/`. This guest is the founding leader of that
cluster, so it becomes Ready on its own, like the dev cluster's
`node-1` in the ordinary smoke. Only the hardware is different.

`machines/node-1.yaml` is the only file that differs from the dev
cluster. It puts every storage role on the card by its by-id name,
`/dev/disk/by-id/mmc-QEMU___0xdeadbeef`, because the kernel name of
the card can change between boots. It sets no `spec.modules`, because
the boot archive has the eMMC modules and the network cards are
virtio. The comments in the file explain both.

## How it runs

`make smoke-emmc` in `liken/` builds this guest's install image and
runs the drill. It installs `node-1` onto a blank card, boots the
installed card under OVMF, and waits until the node reports Ready
through the cluster's API. The `smoke-uefi`, `smoke-bios`, and
`smoke-hardware` drills use the same Ready check.

The lab's Makefile selects this hardware with `HARDWARE=emmc`. It
adds one `sdhci-pci` controller with one `emmc` card, which has 4 MiB
hardware boot areas and a 128 KiB RPMB area, so the guest shows the
same devices as a real card. It adds a second, empty controller
(`id=mmc-empty`) for the case of no card. The `-device` line of the
empty controller must come after the line of the card, because QEMU
names the bus of each controller `sd-bus`, and the card attaches to
the first one.

The drill does not run the hardware report and sets no `REPORT_STICK`.
The report step in `smoke.sh` checks the proposal for the metal
hardware by name, so it cannot check this guest until it supports a
second hardware shape.

OVMF reads the card. With the variable store that the installer
wrote, the firmware loads the slot entry from the card's GPT
(`BdsDxe: loading Boot0002 "liken slot A"`). With a default variable
store, it finds the card on its own and boots the fallback path. So
the lab tests the whole boot from disk. The firmware of `stick-1` also
lists the partitions of its eMMC in its boot menu, which shows the
same step on real hardware.

## When to run it

Run this drill by hand before a release, and after a change to the
disk walk, the by-id names, the boot archive's module list, or the
installer's disk claiming.

CI does not run this drill, so a fault on the eMMC path can reach
`main` and stay there until someone runs it. The parity guest has the
same risk.
