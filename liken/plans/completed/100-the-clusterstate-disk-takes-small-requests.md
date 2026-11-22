# 100. The clusterState disk takes small requests

Milestone 100. Proposed and built on 2026-11-21. The first three
drills ran the same day on lab guests with virtio and SATA disks. The
fourth, the measurement on a leader, has not run.

When one disk holds both etcd and the containers' storage, a pod that
writes hard can make etcd wait seconds for its log to reach the disk.
An etcd member that waits that long stops answering in time, and the
API server on that leader drops out of service.

## The cause

etcd writes its write-ahead log in small writes, each followed by
`fdatasync`, and the call returns only when every request queued
before it has reached the disk. Mainline Linux caps the default
`max_sectors_kb` of a disk at 4 MB (`BLK_DEF_MAX_SECTORS_CAP` in
`block/blk.h`), so a pod that writes large files fills the queue with
requests of 4 MB. A consumer SSD writes fast for about a minute, and
then falls to about 50 MB/s when its write cache is full. At that
rate, about 50 queued requests of 4 MB are 200 MB, and etcd's
`fdatasync` waits 4 seconds behind them.

## What was measured

The measurements ran on a 512 GB SATA SSD with the `mq-deadline`
scheduler. A probe wrote 2,300 bytes and then called `fdatasync`, as
etcd does for its log. At the same time, one pod wrote files of 1 GiB
with an `fsync` after each.

| `max_sectors_kb` | `fdatasync` calls of 1 s or more, in 160 s | Longest wait after the first 30 s |
| --- | --- | --- |
| 4096 | 13 to 23 a minute | 3 to 6 s, and up to 21 s with the writer on etcd's own filesystem |
| 1280 | about 27 a minute | 2.2 s |
| 512 | 1 | 664 ms |
| 256 | 1, in the first second | 545 ms |
| 128 | 1, in the first second | 355 ms |

At 128, the kernel's writeback throttle (wbt) kept 4 to 12 requests in
the queue. The value 128 also removed the stalls when the writer was
on etcd's own filesystem, and when the writer ran in the root cgroup,
as containerd does when it unpacks an image.

Many NVMe drives accept requests of at most 128 KiB themselves
(`max_hw_sectors_kb` is 128), and one such drive wrote 265 to
362 MB/s. So 128 KiB does not limit the speed of a fast disk.

## The design

- **The default.** On every machine, init sets `max_sectors_kb` to 128
  on the whole disk that holds `clusterState`, and leaves every other
  disk at the kernel's value. A machine whose `clusterState` is on the
  RAM root gets nothing. Later boots find the role by its partition
  name, so init goes from the partition to the disk that holds it
  (`sda5` to `sda`, `nvme0n1p5` to `nvme0n1`) through `/sys/block`.
  When the disk's `max_hw_sectors_kb` is below 128, the default is the
  hardware limit. The default is on and not only an option, because
  the failure it prevents takes a leader's API server out of service.
- **`spec.disks`.** A list of entries, each with a `device` and a
  `maxRequestSize`. `device` takes the names that
  `spec.storage.<role>.device` takes, and the machine resolves the name
  on every boot and every pass. `maxRequestSize` is a binary quantity
  in whole KiB, as `spec.storage` sizes are, or `Kernel` to keep the
  kernel's value. An entry sets a different value on the
  `clusterState` disk, sets a value on any other disk, or turns the
  default off with `Kernel`. An entry with no `maxRequestSize` leaves
  the default in place.
- **When it applies.** init applies the settings of its manifest after
  storage is mounted and before k3s starts. machine-operator applies
  them again on every pass, as it does sysctls, so an edit takes
  effect without a reboot. The kernel recomputes its own value when 0
  is written, so `Kernel` returns a disk to the kernel's value live.
  The kernel sends no event when the attribute changes, so the
  operator's ten-second sysctl check also reads each disk it set, and
  a changed value starts a pass with the cause `disk drift`. An entry
  that an edit removes keeps its value until the next boot, as a
  removed sysctl does. The setting is not in `status.boot`, because
  no change to it needs a reboot, as `spec.sysctls` and
  `spec.network.hostEntries` are not.
- **Readback.** `status.hardware.blockDevices[]` has `maxRequestSize`
  and `maxHardwareRequestSize`, read from sysfs, in the syntax of the
  spec. init writes them into the facts, and the operator reads them
  again after its writes.
- **Bad input.** The machine skips an entry that it cannot apply, and
  the disk keeps the default. The `DiskSettingsApplied` condition is
  then `False` with the reason `DiskNotFound`, `AboveHardwareLimit`,
  `InvalidSetting`, or `ApplyFailed`, and the machine is Degraded. The
  CRD refuses a value that is not `Kernel` or whole KiB of at least
  4 KiB, and a `by-uuid` name. A manifest on an install stick does not
  pass admission, and admission cannot read a disk's hardware limit,
  so the machine checks each entry again. The kernel that `liken`
  ships lowers a value above `max_hw_sectors_kb` to the limit, and
  older kernels refuse it. `liken` refuses it on every kernel, so a
  person sees that the disk cannot take the value.

## Alternatives set aside

- **A field on a storage role.** Several roles share one disk and
  could ask for different values, and a disk has one request queue.
- **A map keyed by device name**, like `spec.sysctls`. A device name
  is a path, and a list of objects keyed by `device` follows the
  Kubernetes convention for named entries, as `spec.network.interfaces`
  does, and leaves room for a second attribute of a disk.
- **An integer in KiB**, as the kernel's attribute is. Every size in
  the spec is a binary quantity, and the attribute's name, which says
  sectors and means KiB, would then leak into the API.
- **A value applied only at boot.** The operator is privileged, so its
  `/sys` is writable, and `/sys/block` is not namespaced. The same
  write that init makes works live.
- **I/O priority classes** (`io.prio.class`) did not remove the stalls
  in the measurements, and **`io.cost`** QoS did not either and also
  switched wbt off.

## Drills

- Boot a lab machine with `clusterState` on a SATA disk and read
  `status.hardware.blockDevices` for `128Ki` on that disk and the
  kernel's value on the others.
- Set the `clusterState` disk to `Kernel` on a running machine, and
  read the kernel's value back within one pass.
- Set a value above the hardware limit, and read `AboveHardwareLimit`
  in the condition.
- Run the measurement above on a leader with and without the default,
  and compare `etcd_disk_wal_fsync_duration_seconds` from plan 99's
  metrics.
