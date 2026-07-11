# Root on disk

Milestone 29 — Done

The first attempt to put liken on a real cloud machine found the
limit of a design choice this repo had lived with comfortably: the
operating system runs entirely from RAM. The kernel unpacks a ~130 MB
initramfs into rootfs, init copies it all into a tmpfs, and the
machine spends the rest of its life running from memory it can never
get back. On the lab's 4 GB guests nobody noticed. On a 1 GB Linode
nanode, the boot died before liken ran a single instruction: GRUB's
relocator could not even stage the kernel and the archive in the
memory available.

That failure is a verdict on the design, not on the machine. liken
means to be an ultra-light OS, and an ultra-light OS boots a 1 GB
machine with ample memory to spare. The disk already holds every
byte of the system (that is what a boot slot is); holding those bytes
in RAM again, forever, is paying twice for the same data.

The requirement this milestone exists to meet: a liken machine boots
and runs from a read-only system image on its own disk, and a 1 GB
machine is the lab's standing proof — the dev cluster's guests size
themselves to it.

## The design

* **The system artifact becomes a mountable filesystem.** The release
  ships liken.sqfs — a zstd-compressed squashfs image of the same
  tree liken.cpio used to archive — instead of a cpio the kernel must
  unpack into RAM. squashfs over the alternatives because this
  kernel's support is built in (CONFIG_SQUASHFS=y, zstd decompressor
  included, loop device built in), so the boot needs no modules to
  reach its root. The running root *is* the digest-verified artifact,
  byte for byte, mounted read-only: immutability by construction
  rather than by convention.
* **A small boot archive carries init.** boot.cpio holds /liken and
  the handful of modules the early boot needs (overlayfs, which
  Ubuntu builds as a module); it replaces the big archive as the
  initrd the firmware or GRUB loads. The boot-time memory bill drops
  from ~150 MB staged in the loader to ~15 MB.
* **Root is an overlay: the image below, a bounded tmpfs above.** The
  squashfs is the lower layer; a small tmpfs upper absorbs the
  runtime's writes (k3s's config drop-ins, resolv.conf, the layer's
  seeds). Bounded means bounded: the upper gets a fixed size, and
  everything that grows with use already lives on disk roles
  (clusterState, machineEphemeral, the pod pools) — nothing about a
  busy day makes the root eat RAM.
* **The deployment layer rides exactly as before.** deployment.cpio
  stays a second initrd, unpacked into rootfs before the switch; init
  carries its files (manifests, identity, module overrides) onto the
  overlay. The layer never travels over the network and never grows
  past seeds, so RAM is the right place for its moment in the boot.
* **The lab boots the same way with no disk.** A from-blank-disks
  boot (BOOT=kernel, the smoke drill) wraps liken.sqfs in a trivial
  cpio so it lands in rootfs as a file, and init loop-mounts it from
  there instead of from a slot: the RAM cost returns, but only for
  the lab convenience that accepts it, and the code path is the same
  loop-mount either way.
* **The lab guests shrink to the claim.** dev-cluster's MEM default
  drops to 1024: every drill, every smoke run, every install proves
  the 1 GB envelope from now on.

What does not change: the two-archive split and its reasons, the
release document and catalog shapes, the slot layout and its GPT
names, the installer's verify-copy-reverify discipline, the fetcher's
carry of the layer. Artifact names change inside the release
document, and this project is pre-release: machines are reinstalled,
not migrated.

## What the drills showed

The lab proved the design at 1 GB three ways: the from-blank smoke
boot reached Ready in ~16 seconds, an installed machine booted from
its slot and converged fully (coredns, metrics-server, both
operators, the log relays) with about a third of the machine free,
and the same disk image booted under SeaBIOS — plain BIOS firmware,
the path a Linode takes — to the same Ready node. Then the real
thing: the liken.sh nanode booted the shipped image and was Ready in
21 seconds, answering kubectl on its public address.

Three small truths surfaced on the way. The kernel treats any boot
parameter with a dot in its name as a module parameter and passes it
to init neither as an argument nor in the environment, which is why
liken.slot= can only be read from /proc/cmdline — so /proc now
mounts before anything else. The boot archive's modules have to load
on install boots too, because the installer mounts FAT slots and the
encoding table is a module. And mke2fs rides in the boot archive,
because a single-disk machine (the cloud case) claims and formats
its data roles during the install, when the system image's copy
isn't mounted yet.

## Decisions on record

* **squashfs over erofs.** Both are fine filesystems; this kernel
  builds squashfs in and erofs as a module. The boot path should
  need zero modules to find its root.
* **The RAM the OS still spends is Kubernetes' bill, not liken's.**
  A k3s server idles at several hundred MB; that is the cost of
  running Kubernetes and this milestone does not pretend otherwise.
  What it removes is liken's own doubled copy of the system.
