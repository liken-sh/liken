# Root on disk

Milestone 29 — Done

The first attempt to put liken on a real cloud machine found the limit
of a design choice that this repo had lived with comfortably: the
operating system ran entirely from RAM. The kernel unpacks a ~130 MB
initramfs into rootfs, init copies all of it into a tmpfs, and the
machine then spends the rest of its life running from memory it can
never return. On the lab's 4 GB guests, nobody noticed this cost. On a
1 GB Linode nanode, the boot died before liken ran a single
instruction: GRUB's relocator could not even stage the kernel and the
archive in the memory available.

That failure is a verdict on the design, not on the machine. liken
aims to be an ultra-light OS, and an ultra-light OS should boot a 1 GB
machine with memory to spare. The disk already holds every byte of the
system, because that is what a boot slot is. Holding those same bytes
in RAM again, forever, pays twice for the same data.

This milestone exists to meet one requirement: a liken machine boots
and runs from a read-only system image on its own disk. A 1 GB machine
is the lab's standing proof of this; the dev cluster's guests are now
sized to match it.

## The design

* **The system artifact becomes a mountable filesystem.** The release
  ships liken.sqfs, a zstd-compressed squashfs image of the same tree
  that liken.cpio used to archive, instead of a cpio archive that the
  kernel must unpack into RAM. squashfs was chosen over the
  alternatives because this kernel's support for it is built in
  (CONFIG_SQUASHFS=y, with the zstd decompressor and the loop device
  both included), so the boot needs no modules to reach its root. The
  running root is the digest-verified artifact itself, byte for byte,
  mounted read-only: this gives immutability by construction, rather
  than by convention.
* **A small boot archive carries init.** boot.cpio holds /liken and
  the handful of modules that the early boot needs (overlayfs, which
  Ubuntu builds as a module). It replaces the large archive as the
  initrd that the firmware or GRUB loads. The boot-time memory bill
  drops from about 150 MB staged in the loader to about 15 MB.
* **Root is an overlay: the image below, and a bounded tmpfs above.**
  The squashfs is the lower layer. A small tmpfs upper layer absorbs
  the runtime's writes (k3s's configuration drop-ins, resolv.conf, and
  the layer's seeds). This upper layer has a fixed size, and anything
  that grows with use already lives on a disk role instead
  (clusterState, machineEphemeral, the pod pools). So nothing about a
  busy day makes the root filesystem consume more RAM.
* **The deployment layer travels exactly as before.** deployment.cpio
  stays a second initrd, unpacked into rootfs before the switch. init
  carries its files (manifests, identity, module overrides) onto the
  overlay. The layer never travels over the network and never grows
  past its seed content, so RAM is the right place for it during this
  part of the boot.
* **The lab boots the same way with no disk.** A from-blank-disks boot
  (BOOT=kernel, the smoke drill) wraps liken.sqfs in a trivial cpio, so
  it lands in rootfs as a file, and init loop-mounts it from there
  instead of from a slot. The RAM cost returns in this case, but only
  for the lab convenience that accepts it, and the code path is the
  same loop-mount either way.
* **The lab guests shrink to match the claim.** dev-cluster's MEM
  default drops to 1024, so every drill, every smoke run, and every
  install now proves the 1 GB envelope.

Some things stay the same: the two-archive split and its reasons, the
release document and catalog shapes, the slot layout and its GPT
names, the installer's verify-copy-reverify discipline, and the
fetcher's carry of the layer. Artifact names change inside the release
document, and because this project is pre-release, existing machines
are reinstalled rather than migrated.

## What the drills showed

The lab proved the design at 1 GB in three ways. The from-blank smoke
boot reached the Ready state in about 16 seconds. An installed machine
booted from its slot and converged fully (coredns, metrics-server,
both operators, the log relays), with about a third of the machine's
memory still free. The same disk image also booted under SeaBIOS,
plain BIOS firmware, the path a Linode takes, and reached the same
Ready state. Then came the real test: the liken.sh nanode booted the
shipped image and reached the Ready state in 21 seconds, answering
kubectl on its public address.

Three small facts surfaced along the way. The kernel treats any boot
parameter with a dot in its name as a module parameter, and passes it
to init neither as an argument nor in the environment. This is why
liken.slot= can be read only from /proc/cmdline, so /proc now mounts
before anything else. The boot archive's modules also have to load on
install boots, because the installer mounts FAT slots and the encoding
table is a module. mke2fs also travels in the boot archive, because a
single-disk machine (the cloud case) claims and formats its data roles
during the install, at a point when the system image's own copy is not
yet mounted.

## Decisions on record

* **squashfs over erofs.** Both are capable filesystems, but this
  kernel builds squashfs in while it builds erofs only as a module.
  The boot path should need zero modules to find its root.
* **The RAM the OS still spends belongs to Kubernetes, not to liken.**
  A k3s server idles at several hundred MB, and that is the cost of
  running Kubernetes. This milestone does not claim otherwise. What it
  removes is liken's own doubled copy of the system.
