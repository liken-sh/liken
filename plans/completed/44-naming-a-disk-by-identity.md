# Naming a disk by identity

Milestone 44 — Completed. It lets a Machine declare its storage by a
name that belongs to the disk, and it grows `/dev/disk` past the one
tree that iSCSI needed.

Built 2026-07-31 as twelve commits on `main` (2af4062..91d1a8b). The
design below held, with one deviation worth recording: the collision
report needed a first walk that a test can drive without a uevent
socket, so `init/disklinks.go` grew a `walkDiskLinks` seam for it.

A Machine declared each storage role's device as a kernel name, for
example `/dev/sda`. The kernel assigns that name in probe order, so it
says which disk answered first and nothing about which disk it is.
This milestone lets the same field hold a stable name, and it publishes
the link trees that make such a name resolvable.

## What the running fleet shows

The 44stonypoint fleet is the first place this matters. Three of its
five machines declare `/dev/sda`, and every one of those three also
holds iSCSI LUNs, which the kernel names `sdb`, `sdc`, and onward in
the same series:

```
nuc3  declares /dev/sda    sda 238Gi local SSD  + 5 iSCSI LUNs
nuc4  declares /dev/sda    sda 476Gi local SSD  + 11 iSCSI LUNs
nuc5  declares /dev/sda    sda 476Gi local SSD  + 1 iSCSI LUN
```

The other two declare `/dev/nvme0n1`, which no SCSI disk can take.

## What already holds, and what does not

An ordinary boot is safe, and it is worth stating why, because the
reason sets how small this milestone is.

Recognition never reads the device path. `matchRoles` finds each role
by its GPT partition name across every partition on the machine, and it
refuses to guess when two partitions carry one role's name. A disk that
changed letters, or moved to another controller, still carries its
roles. `awaitStorageDevices` says the same thing in its second door: a
spec is satisfiable once every role is recognized, whatever letter the
disk holds.

The letters cannot collide at that moment either. init writes only
`/etc/iscsi/initiatorname.iscsi`, and every iSCSI login happens in the
`liken-iscsid` DaemonSet, which needs k3s. So no network disk exists
while init settles storage.

The claim is the exposure. `planClaim` takes `role.Device` and
partitions what that path resolves to. An install or a reinstall
therefore writes whatever holds the letter that boot. A USB stick left
in a port after an install can take `sda` and move the system disk to
`sdb`, and the next install boot would claim the stick. The failure is
quiet, because both disks are real and neither is ambiguous.

## The link trees

`init/disklinks.go` already states the shape this needs. It owns
`/dev/disk/by-path`, it computes udev's `path_id` from sysfs without
running udev, and its header records that the tree is meant to grow:
a builder returns link names paired with the devices they point at, and
another builder's pairs merge into the same map or reconcile against a
second directory. Nothing in the file is specific to iSCSI except
`iscsiPaths`.

Three builders followed:

* **`by-id`**, which names the disk. It builds `wwn-` and `scsi-3`
  names from a naa wwid, `nvme-<model>_<serial>` and `nvme-eui.` names
  for NVMe, a `virtio-<serial>` name for a virtio disk, and a
  `usb-<manufacturer>_<product>_<serial>` name for USB. This is the
  name to declare for a machine, because it survives a move to another
  port or another bay. There is no `ata-<model>_<serial>` name: the
  INQUIRY model string that sysfs offers a SATA disk is truncated
  against the ATA IDENTIFY string udev reads, and the `wwn-` name
  already covers those disks.
* **`by-path`** for local disks (PCI, ATA, NVMe, USB), which names the
  port rather than the disk, built from the PCI chain. An NVMe path
  reads its namespace id from the sysfs attribute rather than parsing
  it out of the block device's name. It is the right name for a
  machine whose policy is "whatever is in bay 0", and it is what a
  person reads when they need to know where to put their hands.
* **`by-uuid`**, which names a file system rather than a disk, read
  from an ext4 superblock or a FAT32 volume id. It is not a candidate
  for a role's device, because a role claims a whole disk before any
  file system exists. It belongs here because CSI drivers and
  operators expect the directory, and because it costs one more
  builder.

The NUL trim in `sysfsString` is a prerequisite that is already built.
A `by-id` name is assembled out of the model and the serial, and a
target that pads its serial with NUL would have produced a link name
the kernel refuses.

## Resolving a declared name

The claim runs long before any of these trees exist. `watchDiskLinks`
is a machine-plane component that starts after init has settled
storage, so a claim that read `/dev/disk/by-id/...` from the file
system would find nothing on the boot that matters most.

So the resolution does not depend on the tree. init computes the same
identity from sysfs that the builder computes, and matches the declared
name against it, grouped by the disk that name resolves to. The tree is
for CSI drivers and for people. The claim uses the computation, not its
output. This keeps one source of truth and removes an ordering
constraint that would otherwise be easy to violate and hard to see.

`diskByPath` is the seam. It used to compare a declared path against
`/dev/<kernel name>` only. It now resolves a declared name through the
same builders first, and returns the block device that name identifies.

## The refusals

A declared name that matches two disks is an error, and the boot fails
rather than guesses. This is the rule `matchRoles` already applies to
an ambiguous partition, and the reason is the same: a wrong guess about
which disk holds the cluster destroys data. The storage wait ends at
once on any such refusal, rather than waiting out its normal timeout.

A declared name that matches no disk is the ordinary missing-disk case,
and `awaitStorageDevices` already reports it.

## What an operator reads

`status.hardware.blockDevices` reports each disk's name, model, serial,
and size. It also reports the stable names that disk answers to, so the
operator who must write `spec.storage` can read the value instead of
deriving it.

The hardware report (milestone 36) writes a proposed manifest to the
stick. It proposes the stable name, so a machine installed from its own
report never carries a letter in its spec.

A kernel name stays legal in the spec. A fleet that declares
`/dev/nvme0n1` has nothing to gain from a change, and breaking those
documents would buy nothing.

## Verification

The drills that matter are the ones a unit test cannot reach. Two of
the four ran on the dev cluster; the other two need real ports and real
transport, so they wait for metal.

* Install a machine with a spare disk left in a port, and confirm the
  claim lands on the declared disk rather than on the letter. Ran in
  the lab as a stray-disk race: see below.
* Move a system disk to another SATA port and confirm the machine
  boots, both under a declared `by-id` name and under a declared
  letter, since recognition already covers the second case. Waits for
  metal.
* Attach an iSCSI LUN whose serial is NUL-padded and confirm its
  `by-id` name appears. Waits for metal. No transport in the dev
  cluster offers a serial to pad, since SCSI has no serial-based `by-id`
  name in the first place; the lab drilled the `wwn-` name and the
  status serial trim instead, which is what a NUL-padded serial would
  actually exercise.
* Confirm every published name resolves to the same device that udev
  would name, by comparing against a distribution that runs udev on
  the same hardware. Waits for metal; this is also the drill that would
  test the USB `by-path` name's assumption that the storage interface
  is always `:1.0`.

## What the lab measured

Every drill below ran on the dev cluster on 2026-07-31 and passed.

`smoke-uefi` and `smoke-bios` still pass on letter specs, so the
compatibility path this milestone had to hold stays green under both
firmwares. An unattended install under a declared `by-id` spec reached
`Ready` in 21 seconds under UEFI and 26 seconds under BIOS, matching
the timing a letter spec already gets.

A stray-disk race stood in for the USB-stick case: two blank disks were
attached so they took `vda` and `vdb` ahead of the declared disks, and
the claims still landed on the right disk by identity. Both strays were
confirmed at zero written bytes afterward.

A duplicate-identity spec, naming a value that matched two disks,
refused rather than guessed:

```
matches 2 disks (vda, vdb); refusing to guess
```

The storage wait failed at 0.97 seconds, and neither disk was written.

An upgrade from the public release 2026.07.30-001 to a lab build
tagged `-901` ran under both firmwares, each about 40 seconds. Status
gained `stableNames` for every disk during the run, and the letter spec
already on the Machine stayed untouched.

Two findings came out of these drills and are already fixed. FAT
volume ids were derived from a whole-second timestamp, so a claim that
formatted three volumes inside one second gave them the same id; the
fix draws the id from `crypto/rand` instead. And the collision report
printed its line on every walk of the tree rather than once, so a
disk's still-shared id would repeat the line on every reconcile; the
fix prints it once per boot, and a machine installed by the older
release now shows exactly one line for an id it genuinely still
shares.
