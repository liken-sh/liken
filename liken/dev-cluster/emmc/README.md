# The eMMC guest

Every other guest in the lab has more than one disk, and each of
those disks is a plain block device with nothing beside it. A machine
whose only disk is an eMMC module is not like that. Its one card
carries every storage role, boot slots and pod storage alike, and the
kernel exposes three block devices beside the card's data area that
are not storage at all: the two hardware boot areas (`mmcblk0boot0`,
`mmcblk0boot1`), which are block devices, and the authenticated RPMB
area, which this kernel presents as a character device. The field
case that
produced this deployment is `stick-1`, an Apollo Lake HDMI stick PC
whose 64GB eMMC `liken` could not see: the controller's PCI class
read as `system`, no driver recommendation appeared, and the report
proposed an empty storage block while a working disk sat in the
machine. `plans/completed/63-emmc.md` gives the whole design.

This deployment closes the gap. It boots the same `node-1` the dev
cluster boots, on the hardware shape a stick PC has: two `sdhci-pci`
host controllers, one with an `emmc` card behind it and one with an
empty slot, which the kernel drives through `sdhci`, `sdhci-pci`,
and `mmc_block`, the modules the boot archive carries. The guest
proves four things at once: the boot archive's eMMC modules load
before the machine looks for its disk, the disk walk keeps the
card's data area and skips the boot areas, a one-disk machine claims
and formats every storage role on the card it booted from, and a
controller with no card binds quietly, which is the case every liken
machine in the field hits on every boot. The skip is asserted, not
assumed: `smoke.sh` reads `status.hardware.blockDevices` back from
the cluster and requires it to name `mmcblk0` alone.

## What is here

`cluster.yaml` is a symlink to the dev cluster's own cluster
document, for the same reason the parity guest's is: this guest is
the same cluster's founding leader, so it comes up Ready on its own,
exactly as the dev cluster's `node-1` does under the ordinary smoke.
Nothing about the cluster changes; only the hardware does.

`machines/node-1.yaml` is the one file that differs. It puts every
storage role on `/dev/mmcblk0`, the name the mmc block driver gives
the card, and it declares no `spec.modules`, because the eMMC stack
travels in the boot archive and the cards are virtio. The file's own
comments explain both.

## How it runs

`make smoke-emmc` from the repo root builds this deployment's install
image and runs the drill: it installs `node-1` onto a blank card,
then boots the installed card under OVMF and waits for the node to
report Ready over the cluster's API. Ready is the same verdict the
`smoke-uefi`, `smoke-bios`, and `smoke-hardware` drills use. The
lab's Makefile selects the shape with `HARDWARE=emmc`: one
`sdhci-pci` controller fronting one `emmc` card, with 4 MiB hardware
boot areas and a 128 KiB RPMB area, so the guest presents the same
devices a real card does, plus a second, empty controller
(`id=mmc-empty`) for the no-card bind. The empty controller's
`-device` line must come after the card's, because QEMU names both
child buses `sd-bus` and the card attaches to the first one that
exists.

The drill runs no hardware report. It passes no `REPORT_STICK`,
because the report step's assertions in `smoke.sh` read the metal
shape's proposal by name, and the report boot proves nothing new here
until those assertions grow a shape of their own.

OVMF reads the card. The lab measured it both ways: with the varstore
the installer wrote, the firmware loads the slot entry from the
card's GPT (`BdsDxe: loading Boot0002 "liken slot A"`), and with a
vendor-default varstore it enumerates the card on its own and takes
the fallback path. So the from-disk leg of this drill runs entirely
in the lab. The field case already proved the metal half: `stick-1`'s
own firmware lists the eMMC's partitions in its boot menu.

## When to run it

Run this drill by hand: before a release, and whenever a change
touches the disk walk, the by-id names, the boot archive's module
list, or the installer's disk claiming.

It is not in CI. The CI runner is Ubuntu 24.04 with QEMU 8.2, and the
`emmc` device arrived in QEMU 9.1, so the runner cannot present the
card. When the runner's QEMU reaches that version, the drill joins
the build workflow beside the other smokes. Until then the cost is
the same one the parity guest carries: a fault on the eMMC path can
reach `main` and wait here until someone runs this drill. That is the
reason for the list above.
