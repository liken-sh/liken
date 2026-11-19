# The hardware-parity guest

An ordinary lab guest has virtio-blk disks and virtio-net network
cards. The vendored kernel builds both drivers in
(`CONFIG_VIRTIO_BLK=y`, `CONFIG_VIRTIO_NET=y`), so such a guest never
loads a storage or a network module. A real machine has its disks on
a SATA or an NVMe controller and a real network card, and the kernel
drives them through modules. A real machine fails to boot when the
module its controller needs is not loaded, and the ordinary guests
never test that.

This deployment boots the dev cluster's `node-1` on the hardware a
real machine has: the disks are on an AHCI SATA controller
(`CONFIG_SATA_AHCI=m`), and the network cards are e1000
(`CONFIG_E1000=m`). The kernel builds both drivers as modules, so the
guest proves that:

* the boot archive loads the storage module before it looks for the
  boot disk,
* the boot waits long enough for the controller to find its disks,
  and
* `spec.modules` loads the network driver and brings a real card up.

## What is here

`cluster.yaml` is a symlink to the dev cluster's `cluster.yaml`. The
guest is that cluster's founding leader, so it becomes Ready on its
own, as the dev cluster's `node-1` does in the ordinary drills. Only
the hardware differs.

`machines/node-1.yaml` differs from the dev cluster's `node-1`. It
moves the storage roles from the virtio disks (`/dev/vd*`) to the SATA
disks (`/dev/sd*`), and it declares `e1000` in `spec.modules`. Its
comments explain why the disk names change and the interface names do
not.

## How it runs

`make smoke-hardware` in `liken/` builds this deployment's install
image and stick, then runs the drill. The drill follows the steps a
person takes with a new machine. It boots the hardware report and
checks the manifest that the report writes to the stick. It installs
`node-1` onto blank disks. Then it boots the installed disk and waits
until the node is Ready in the cluster's API, the same check that the
`smoke-uefi` and `smoke-bios` drills use. The lab's Makefile selects
this hardware with `HARDWARE=metal`. The default, `HARDWARE=virtio`,
is the hardware of the other targets except `smoke-emmc`.

## When to run it

CI runs `smoke-uefi` and `smoke-bios` on every push, but not this
drill, because it adds too much time to every build. It is the only
drill that loads a network driver from `spec.modules` and checks the
hardware report, so a fault there can reach `main` and stay until
someone runs it. Run it by hand before a release, and after every
change to the boot archive's module list, the boot's wait for
storage, the hardware report, or the installer.
