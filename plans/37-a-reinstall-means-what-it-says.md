# A reinstall means what it says

Milestone 37 — Done

Milestone 36 put liken on real hardware and gave the installer a voice.
The machine that ran it then reported what the lab could not: a
`wipe and reinstall` left the previous install's cluster state in place,
and the layout the report proposed was too small for a real disk. Both
findings are about the same thing. An install states what the machine
will be, and both of these let the machine be something else.

This milestone has three parts, and a pass over the manual.

## A partition this boot created gets a new file system

`liken.reinstall` blanks each declared disk's partition table: the first
mebibyte and the last, which hold the GPT and its mirror. Partitions
start one mebibyte in, so the wipe stops one byte short of the first
one. Nothing in a partition is touched.

That was invisible while the claim rewrote the same layout, because a
claim writes each role's name into the partition entry and then leaves
the file system alone if it finds one. The old ext4 signature is still
at the same offset, so the mount succeeds, and the machine comes back
with the previous install's etcd database, its proven manifest, its node
password, and its rejection records. The testbed's `creationTimestamp`
told the story: hours older than the reinstall that was meant to replace
it.

The fix does not belong in the reinstall path, because the reinstall
path is not what decided this. It belongs in the claim path, as one
rule: **a partition this boot created is always formatted.** A partition
liken created seconds ago holds nothing worth keeping, whatever the
bytes under it say.

The rule keeps the property the old check existed for. Claiming writes
the role's name before any file system exists, so a boot that dies
between partitioning and mkfs leaves a partition the next boot
recognizes and finishes. That boot does not create the partition, so it
still asks the signature, still finds nothing, and still formats. A boot
that died after mkfs still keeps its file system.

It also closes a quieter fault. The tail wipe lands inside the last
partition, and zeroes about a mebibyte of its data while leaving its
superblock alone. Before this rule, a reinstall could mount that file
system without a check. Now the partition is always made again.

So a reinstall erases every role it claims, on every disk the manifest
declares. Nothing of the previous install survives. That is what the
menu entry says, and now it is what the boot does.

## The proposed layout scales with the disk

The report proposes a fixed 6Gi for `clusterState`, the size liken's own
public node runs. On the testbed's 477Gi SSD that reads as 6Gi of image
store, 4Gi of pod volumes, and 466Gi of scratch. A person had to correct
it by hand, and they chose 64Gi.

The size matters more than the others because it is permanent.
`clusterState` cannot grow after the install: `podStorage` sits directly
behind it in the canonical order, and a partition only grows into free
space that follows it. A number chosen at install time is a number the
machine keeps.

So the layout gains a rung above the conventional one, for a disk with
room to spare. `clusterState` takes an eighth of the disk, with the
conventional 6Gi as its floor and 64Gi as its ceiling. `podEphemeral`
takes a bounded share, and `podStorage` takes the rest. The ladder for a
small disk does not change: `podStorage` still gives up space first, and
`clusterState` still gives up space last and never below its floor.

The note beside the numbers says the part a person cannot see: this size
is permanent, and the images this node runs decide it.

## The Machine document after a reinstall

A reinstall with a new layout leaves the cluster holding the old one.
The operator then refuses to stage the Machine's spec, because a
declared size smaller than the partition on the disk is a shrink, and
storage roles are grow-only. The machine sits in `StagingRejected` until
a person patches the Machine by hand.

The Machine document is authoritative, and this milestone does not
change that. It changes what the refusal says. The message names the
role, the declared size, the size the disk actually carries, and the
remedy, so the person reading it has no diagnosis left to do. The
install guide carries the order the edit needs: the machine publishes
its new layout in status first, and the spec is edited to match after.

## What the lab proved

A filesystem carries a UUID that `mke2fs` writes once, so the UUID is
the evidence. The drill installed node-1, read every partition's UUID
from the guest's disk images, booted `liken.reinstall`, and read them
again. Every one changed: `machineState`, `clusterState`,
`machineEphemeral`, `podStorage`, and `podEphemeral`. The console
showed the three claims and the eight new filesystems. The node then
booted and founded a cluster in half a minute, with no trace of the
one before it.

## The manual

Milestone 36 documented the menu, the report boot, and the reinstall
entry. This pass corrects what changed and fills what was assumed: that
these boots need a person at a keyboard and a screen, or a serial
console built into the stick; that the report loads storage and network
drivers only, and why; that the report can run again as often as a
person likes; and what a reinstall now erases.
