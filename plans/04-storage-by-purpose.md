# Storage, declared by purpose

Milestone 4 — Done

Storage starts with the question of whether a disk exists at
all. The whole machine is RAM today. The goal is to put k3s's
state on persistent storage. This way, container images stop
re-importing on every boot, and cluster state survives a reboot.
The system declares storage by *purpose*, not by mount path.
`spec.storage` is a map keyed by a singleton role, with
`clusterState` first. Each entry names a device and an optional
fixed size. liken derives GPT partition tables from the roles,
grouped by device. It formats blank disks at runtime, and names
each partition `liken:<role>`. On every later boot, liken
recognizes partitions by name, read from sysfs. There is no
udev. The `device:` field is an input for first-boot claiming
only, because the kernel does not guarantee its enumeration
order. Reconciling never destroys data. The system claims a
blank disk, mounts its own partitions, and refuses anything
foreign or ambiguous. If a declared role cannot be reconciled,
the boot stops: init prints the full explanation to the console
and powers the machine off, and k3s never starts. The reasoning
is this: a machine that promises persistent cluster state, but
boots with ephemeral storage anyway, will lose data without
warning. A machine that is down can be recovered, but data
written to the wrong place cannot be recovered. Undeclared roles
land in the same place everything lands today: the root tmpfs.
`status.storage` lists where every role actually landed, either
`Partition` or `Memory`. `status.hardware.blockDevices` reports
the raw inventory.
1. [x] A disk exists. `make run` attaches a gitignored qcow2 file.
   Init discovers block devices from `/sys/block` and adds them
   to its boot-time report.
2. [x] Claiming. Init writes the GPT itself, a small checksummed
   binary format and a good lesson to learn. Init makes the
   filesystem; the open question was the mechanism, since the
   image has no libc, so mkfs must be a static binary or Go
   code. Init mounts `clusterState` where k3s will use it, all
   before k3s starts. The init/ package unit-tests every reason
   a spec can be refused: foreign disks, cloned disks, disks too
   small, and partial claims. It tests these against fake sysfs
   trees. A refusal halts the boot from one place in main.go.
3. [x] Prove persistence. Boot, power off, and boot again. Images
   import once, and the cluster comes back. (Milestone 5's
   reboot cycles proved this: the cluster survived staged-spec
   reboots and a hard power cut, on the same disks.)
4. [x] The API. `spec.storage` and `status.storage` in the Machine
   CRD. The operator publishes the landing table and the
   hardware inventory.
