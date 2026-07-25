# A reinstall formats every partition

Milestone 37 — Completed. A reinstall erases every role it claims, and
the proposed disk layout scales with the size of the disk.

Milestone 36 put liken on real hardware. That machine showed two faults
that the lab did not show. A `wipe and reinstall` left the previous
install's cluster state in place. The layout the report proposed was
too small for a real disk. In both cases the install states what the
machine will be, and the machine becomes something else.

This milestone has three parts and a pass over the manual.

## A partition this boot created gets a new file system

`liken.reinstall` blanks each declared disk's partition table: the
first mebibyte and the last, which hold the GPT and its mirror.
Partitions start one mebibyte in, so the wipe stops one byte before the
first partition. Nothing in a partition is touched.

That was not visible while the claim wrote the same layout again. A
claim writes each role's name into the partition entry, then leaves the
file system alone if it finds one. The old ext4 signature is at the
same offset, so the mount succeeds, and the machine comes back with the
previous install's etcd database, its proven manifest, its node
password, and its rejection records. The testbed's `creationTimestamp`
showed this: hours older than the reinstall that was to replace it.

The fix does not belong in the reinstall path, because the reinstall
path did not decide this. It belongs in the claim path, as one rule: a
partition this boot created is always formatted. A partition liken
created seconds ago holds nothing to keep, whatever the bytes in it
are.

The rule keeps the property that the old check gave. A claim writes the
role's name before a file system exists, so a boot that stops between
the partition step and mkfs leaves a partition that the next boot
recognizes and completes. That next boot does not create the partition,
so it still reads the signature, still finds none, and still formats. A
boot that stopped after mkfs still keeps its file system.

The rule also closes a second fault. The tail wipe lands inside the
last partition and zeroes about a mebibyte of its data, but leaves its
superblock. Before this rule, a reinstall could mount that file system
with no check. Now the partition is always made again.

A reinstall now erases every role it claims, on every disk the manifest
declares. Nothing from the previous install stays. That is what the
menu entry says, and now it is what the boot does.

## The proposed layout scales with the disk

The report proposes a fixed 6Gi for `clusterState`, the size that
liken's own public node runs. On the testbed's 477Gi SSD that gives 6Gi
of image store, 4Gi of pod volumes, and 466Gi of scratch. A person had
to correct it by hand, and chose 64Gi.

This size is more important than the others because it is permanent.
`clusterState` cannot grow after the install. `podStorage` is directly
behind it in the canonical order, and a partition only grows into free
space that follows it. A number chosen at install time is a number the
machine keeps.

The layout gains a step above the conventional one, for a disk with
free space. `clusterState` takes an eighth of the disk, with the
conventional 6Gi as its floor and 64Gi as its ceiling. `podEphemeral`
takes a bounded share, and `podStorage` takes the remainder. The steps
for a small disk do not change: `podStorage` gives up space first, and
`clusterState` gives up space last and never below its floor.

The note beside the numbers states what a person cannot see: this size
is permanent, and the images this node runs decide it.

## The Machine document after a reinstall

A reinstall with a new layout leaves the cluster with the old layout.
The operator then refuses to stage the Machine's spec, because a
declared size smaller than the partition on the disk is a shrink, and
storage roles are grow-only. The machine stays in `StagingRejected`
until a person patches the Machine by hand.

The Machine document is authoritative, and this milestone does not
change that. It changes what the refusal says. The message names the
role, the declared size, the size the disk carries, and the remedy, so
the person who reads it has no diagnosis left to do. The install guide
gives the order the edit needs: the machine publishes its new layout in
status first, and the spec is edited to match after that.

## The lab drill

A file system carries a UUID that `mke2fs` writes once, so the UUID is
the evidence. The drill installed node-1, read every partition's UUID
from the guest's disk images, booted `liken.reinstall`, and read the
UUIDs again. Every one changed: `machineState`, `clusterState`,
`machineEphemeral`, `podStorage`, and `podEphemeral`. The console
showed the three claims and the eight new file systems. The node then
booted and founded a cluster in half a minute, with no data from the
install before it.

## The manual

Milestone 36 documented the menu, the report boot, and the reinstall
entry. This pass corrects what changed and adds what was assumed: that
these boots need a person at a keyboard and a screen, or a serial
console built into the stick; that the report loads storage drivers and
network drivers only, and why; that the report can run again as many
times as necessary; and what a reinstall now erases.
