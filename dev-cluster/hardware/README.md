# The hardware-parity guest

Every other guest in the lab boots on paravirtual hardware: its disks
are virtio-blk and its network cards are virtio-net. The vendored
kernel builds both drivers in (`CONFIG_VIRTIO_BLK=y`,
`CONFIG_VIRTIO_NET=y`), so a virtio guest never loads a storage or a
network module. A real machine is not like this. Its disks hang off a
SATA or an NVMe controller, and its network card is a real card, and
the kernel drives all of these through modules. liken's first boot on
real hardware failed on exactly this seam: the modules that a real
controller needs were never loaded, and no lab guest had ever exercised
that path.

This deployment closes the gap. It boots the same `node-1` the dev
cluster boots, on hardware with the shape a real machine has: the disks
sit on an AHCI SATA controller (`CONFIG_SATA_AHCI=m`), and the network
cards are e1000 (`CONFIG_E1000=m`). Both are classes the kernel ships
as modules, so this guest walks the module path a virtio guest skips.
The guest proves three things at once: the boot archive loads the
storage module before it looks for the boot disk, the storage wait
outlasts the controller's own settle time, and the declared-module
path (`spec.modules`) loads the NIC driver and brings a real card up.

## What is here

`cluster.yaml` is a symlink to the dev cluster's own cluster document.
The parity guest is the same cluster's founding leader, so it comes up
Ready on its own, exactly as the dev cluster's `node-1` does under the
ordinary smoke. Nothing about the cluster changes; only the hardware
does.

`machines/node-1.yaml` is the one file that differs from the dev
cluster's `node-1`. It moves the storage roles from the virtio disks
(`/dev/vd*`) to the SATA disks (`/dev/sd*`), and it declares `e1000` in
`spec.modules`. The file's comments explain why the device names change
and why the interface names hold.

## How it runs

`make smoke-hardware` from the repo root builds this deployment's own
install image, then runs the parity drill: it installs `node-1` from
blank disks on the metal-shaped hardware, boots the installed disk, and
waits for the node to report Ready over the cluster's API. This is the
same verdict the `smoke-uefi` and `smoke-bios` drills use. The lab's
Makefile selects the hardware shape with `HARDWARE=metal`; the default,
`HARDWARE=virtio`, is the shape every other target uses.
