# Growing a role on the system disk fails the boot

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
The code was checked on 2026-11-19, and the evidence below holds. The
kernel's refusal shows only on a disk with a mounted partition, so no
unit test can show it. A run on a `lab` guest, as "What would confirm
it" describes, can.

When a declared size grows a partition on the disk that holds the
running system's boot slot, init rewrites the partition table and asks
the kernel to read it again. The kernel refuses while a partition of
that disk is mounted, and the boot slot is mounted, so
`reconcileStorage` returns the error and the boot stops. The same
happens after a disk grows, such as a cloud volume expansion, when a
role on it has no declared size, because that role then extends to the
new end of the disk.

## The evidence

`applyGrowth` (`init/grow.go:166`) calls `disks.WriteTable`
(`init/grow.go:189`) whenever the plan has edits. `WriteTable`
(`disks/gpt.go:437`) ends with the `BLKRRPART` ioctl
(`disks/gpt.go:448`). The comment on `WriteTableInPlace`
(`disks/gpt.go:454` to `462`) says that the kernel refuses that ioctl
on a disk with any partition mounted, and that the disk of the running
system always has its boot slot mounted. `applyGrowth` uses
`WriteTableInPlace` only when the plan has no edits
(`init/grow.go:173`).

The header of `init/grow.go` (lines 13 to 16) says that growth runs
before init mounts any role, but that covers the roles, not the boot
slot that the root image was mounted from. `planAllGrowth` refuses to
grow the system slots and the boot roles, but not other roles on the
system disk. `reconcileStorage` returns the error from `applyGrowth`
(`init/storage.go:296`).

## What would confirm it

A `lab` guest with one disk that holds both the system slots and
`podStorage`. Raise `podStorage`'s size in the `Machine`, or resize the
disk with `qemu-img resize` when `podStorage` has no size, and boot.

## What a fix has to decide

How the kernel learns the new size of a partition on a disk with a
mounted partition. `BLKPG_RESIZE_PARTITION` changes one partition's
size without a full re-read, and it works on a disk in use. Otherwise
growth on the system disk must be refused when the spec is planned, as
the system slots are.
