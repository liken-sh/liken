# Root on disk

Milestone 29. Completed. A machine boots and runs from a read-only
system image on its own disk, instead of from a copy of the system in
RAM.

Before this milestone the operating system ran entirely from RAM. The
kernel unpacked a ~130 MB initramfs into rootfs, init copied all of it
into a tmpfs, and the machine then ran for the rest of its uptime from
memory that it could never return. The lab's 4 GB guests absorbed that
cost. A 1 GB Linode nanode did not: the boot failed before liken ran one
instruction, because GRUB's relocator could not stage the kernel and the
archive in the available memory.

That failure is a result of the design, not of the machine. liken aims
to be an ultra-light OS, and an ultra-light OS boots a 1 GB machine with
memory to spare. The disk already holds every byte of the system,
because that is what a boot slot is. A second copy in RAM, held forever,
pays twice for the same data.

This milestone meets one requirement: a liken machine boots and runs
from a read-only system image on its own disk. A 1 GB machine is the
lab's standing proof, and the dev cluster's guests are now that size.

## The design

* **The system artifact becomes a mountable filesystem.** The release
  ships liken.sqfs, a zstd-compressed squashfs image of the same tree
  that liken.cpio archived, instead of a cpio archive that the kernel
  must unpack into RAM. squashfs was chosen over the alternatives
  because this kernel builds its support in (CONFIG_SQUASHFS=y, with the
  zstd decompressor and the loop device included), so the boot needs no
  module to reach its root. The running root is the digest-verified
  artifact itself, byte for byte, mounted read-only. The system is
  therefore immutable by construction, not by convention.
* **A small boot archive holds init.** boot.cpio holds /liken and the
  few modules that the early boot needs, such as overlayfs, which Ubuntu
  builds as a module. It replaces the large archive as the initrd that
  the firmware or GRUB loads. The boot-time memory cost drops from about
  150 MB staged in the loader to about 15 MB.
* **Root is an overlay, with the image below and a bounded tmpfs
  above.** The squashfs is the lower layer. A small tmpfs upper layer
  takes the runtime's writes: k3s's configuration drop-ins, resolv.conf,
  and the layer's seeds. This upper layer has a fixed size, and
  everything that grows with use is on a disk role instead
  (clusterState, machineEphemeral, and the pod pools). A busy day
  therefore does not make the root filesystem use more RAM.
* **The deployment layer travels exactly as before.** deployment.cpio
  stays a second initrd, and it unpacks into rootfs before the switch.
  init copies its files (manifests, identity, and module overrides)
  onto the overlay. The layer never travels over the network and never
  grows past its seed content, so RAM is the correct place for it during
  this part of the boot.
* **The lab boots the same way with no disk.** A from-blank-disks boot
  (BOOT=kernel, the smoke drill) wraps liken.sqfs in a small cpio, so it
  lands in rootfs as a file, and init loop-mounts it from there instead
  of from a slot. The RAM cost returns in this case, but only for the
  lab convenience that accepts it, and the code path is the same
  loop-mount in both cases.
* **The lab guests become 1 GB.** dev-cluster's MEM default drops to
  1024, so every drill, every smoke run, and every install now proves
  the 1 GB envelope.

Some things do not change: the two-archive split and its reasons, the
release document and catalog shapes, the slot layout and its GPT names,
the installer's verify-copy-reverify discipline, and the way the
fetcher carries the layer. Artifact names change inside the release
document. This
project is pre-release, so existing machines are reinstalled and not
migrated.

## The drills

The lab proved the design at 1 GB in three ways. The from-blank smoke
boot reached the Ready state in about 16 seconds. An installed machine
booted from its slot and converged fully, with coredns, metrics-server,
both operators, and the log relays, and about a third of the machine's
memory still free. The same disk image also booted under SeaBIOS, which
is plain BIOS firmware and the path that a Linode takes, and it reached
the same Ready state. The liken.sh nanode then booted the shipped image,
reached the Ready state in 21 seconds, and answered kubectl on its
public address.

Three small facts came out of this work. The kernel treats a boot
parameter with a dot in its name as a module parameter, and passes it to
init neither as an argument nor in the environment. liken.slot= can
therefore be read only from /proc/cmdline, so /proc now mounts before
anything else. The boot archive's modules must also load on an install
boot, because the installer mounts FAT slots and the encoding table is a
module. mke2fs also travels in the boot archive, because a single-disk
machine, which is the cloud case, claims and formats its data roles
during the install, at a point where the system image's own copy is not
yet mounted.

## Decisions on record

* **squashfs over erofs.** Both are capable filesystems, but this kernel
  builds squashfs in and builds erofs only as a module. The boot path
  needs no module to find its root.
* **The RAM that the OS still uses belongs to Kubernetes, not to
  liken.** A k3s server idles at several hundred MB, and that is the
  cost of running Kubernetes. This milestone does not claim otherwise.
  What it removes is liken's own second copy of the system.
