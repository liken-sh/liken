# Storage, declared by purpose

Milestone 04 — Completed. A machine declares storage by role, then
claims and formats blank disks itself.

Before this milestone the whole machine runs in RAM. The goal is to put
the k3s state on persistent storage. Then container images do not import
again on every boot, and cluster state survives a reboot.

The system declares storage by *purpose*, and not by mount path.
`spec.storage` is a map keyed by a singleton role, with `clusterState`
first. Each entry names a device and an optional fixed size. liken
derives the GPT partition tables from the roles, grouped by device. It
formats blank disks at runtime, and names each partition `liken:<role>`.
On each later boot, liken finds the partitions by name, read from sysfs.
There is no udev. The `device:` field is an input for first-boot
claiming only, because the kernel does not guarantee its enumeration
order.

Reconciliation never destroys data. The system claims a blank disk,
mounts its own partitions, and refuses a disk that is foreign or
ambiguous. If the system cannot reconcile a declared role, the boot
stops: init prints the full explanation to the console and powers the
machine off, and k3s does not start. The reason is this. A machine that
promises persistent cluster state, but boots with ephemeral storage,
loses data with no warning. A person can recover a machine that is down,
but nobody can recover data written to the wrong place. Undeclared roles
go where everything goes today: the root tmpfs. `status.storage` lists
where each role landed, either `Partition` or `Memory`.
`status.hardware.blockDevices` reports the raw inventory.
1. [x] A disk exists. `make run` attaches a gitignored qcow2 file. Init
   finds block devices from `/sys/block` and adds them to its boot-time
   report.
2. [x] Claiming. Init writes the GPT itself, a small checksummed binary
   format. Init makes the filesystem. The open question was the
   mechanism, because the image has no libc, so mkfs must be a static
   binary or Go code. Init mounts `clusterState` where k3s will use it,
   all before k3s starts. The init/ package unit-tests every reason to
   refuse a spec: foreign disks, cloned disks, disks that are too small,
   and partial claims. It tests these against fake sysfs trees. A
   refusal stops the boot from one place in main.go.
3. [x] Prove persistence. Boot, power off, and boot again. Images import
   once, and the cluster comes back. The reboot cycles in milestone 5
   proved this: the cluster survived staged-spec reboots and a hard
   power cut, on the same disks.
4. [x] The API. `spec.storage` and `status.storage` in the Machine CRD.
   The operator publishes the landing table and the hardware inventory.
