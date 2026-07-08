# Storage, declared by purpose

Milestone 4 — Done

Storage, which starts with a disk existing at all. The whole
machine is RAM today. The goal is to put k3s's state on
persistent storage, so that container images stop re-importing
every boot and cluster state survives a reboot. Storage is
declared by *purpose*, not by mount path: `spec.storage` is a
map keyed by singleton role (`clusterState` first), each entry
naming a device and an optional fixed size. liken derives GPT
partition tables from the roles grouped by device, formats blank
disks at runtime, and names each partition `liken:<role>`.
Recognition on every later boot is by partition name read from
sysfs. There is no udev, and `device:` is an input for
first-boot claiming only, since kernel enumeration order is not
guaranteed. Reconciling never destroys data: a blank disk is
claimed, our own partitions are mounted, and anything foreign or
ambiguous is refused. A declared role that can't be reconciled
stops the boot: init prints the full explanation to the console
and powers the machine off, and k3s never starts. The reasoning:
a machine that promised persistent cluster state but boots
ephemeral anyway will silently lose data, and a machine that is
down can be recovered while data written to the wrong place
cannot. Undeclared roles simply land where everything lands
today, the root tmpfs. `status.storage` enumerates where every
role actually landed (`Partition` or `Memory`), while
`status.hardware.blockDevices` reports the raw inventory.
1. [x] A disk exists: `make run` attaches a gitignored qcow2, and
   init discovers block devices from `/sys/block` and adds them
   to its boot-time report.
2. [x] Claiming: init writes the GPT itself (a small, checksummed
   binary format, and a good lesson), makes the filesystem (the
   open question was mechanism: the image has no libc, so mkfs
   must be a static binary or Go code), and mounts
   `clusterState` where k3s will use it, all before k3s starts.
   Every reason a spec can be refused (foreign disks, cloned
   disks, disks too small, partial claims) is unit-tested in
   init/, against fake sysfs trees; a refusal halts the boot
   from one place in main.go.
3. [x] Prove persistence: boot, power off, and boot again; images
   import once and the cluster comes back. (Proven by milestone
   5's reboot cycles: the cluster survived staged-spec reboots
   and a hard power cut, on the same disks.)
4. [x] The API: `spec.storage` and `status.storage` in the Machine
   CRD, the operator publishing the landing table and the
   hardware inventory.
