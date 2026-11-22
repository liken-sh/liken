---
title: Disk settings
weight: 27
toc: true
---

# Disk settings

The Machine's [`spec.disks`](/docs/reference/machine/#spec--disks)
sets how the kernel sends requests to a whole disk. Its one setting,
`maxRequestSize`, is the largest single request that the kernel sends
to the disk: the `/sys/block/<disk>/queue/max_sectors_kb` attribute.
`liken` sets it on the disk that holds `clusterState`, so that etcd's
writes do not wait behind large writes from pods.

## The default on the clusterState disk

On every machine, `liken` sets `maxRequestSize` to `128Ki` on the disk
that holds the
[`clusterState`](/docs/reference/machine/#specstorage--clusterstate)
storage role. Every other disk keeps the kernel's value. A machine
whose `clusterState` is in memory gets no default.

The kernel's own value is usually much larger. Mainline Linux caps it
at 4 MB (`BLK_DEF_MAX_SECTORS_CAP` in `block/blk.h`), and below that
cap it uses the largest request that the disk accepts. When one disk
holds both etcd and the containers' storage, a pod that writes hard
fills the disk's queue with requests of that size. etcd writes its
log in small writes and waits for each one to reach the disk with
`fdatasync`. That wait includes every request queued before it.

A consumer SSD writes fast for about a minute, and then falls to
about 50 MB/s when its write cache is full. At that rate, 50 queued
requests of 4 MB are 200 MB, which take 4 seconds to drain. An etcd
member that waits seconds for its log stops answering in time, and
the API server on that leader drops out of service.

With smaller requests, the kernel's writeback throttle keeps the
queue short, and etcd's write waits behind less data. These are the
results on a 512 GB SATA SSD with the `mq-deadline` scheduler. A probe
wrote 2,300 bytes and then called `fdatasync`, as etcd does for its
log, while one pod wrote files of 1 GiB with an `fsync` after each:

| `max_sectors_kb` | `fdatasync` calls of 1 s or more, in 160 s | Longest wait after the first 30 s |
| --- | --- | --- |
| 4096 | 13 to 23 a minute | 3 to 6 s, and up to 21 s when the pod wrote to etcd's own filesystem |
| 1280 | about 27 a minute | 2.2 s |
| 512 | 1 | 664 ms |
| 256 | 1, in the first second | 545 ms |
| 128 | 1, in the first second | 355 ms |

At 128 the writeback throttle kept 4 to 12 requests in the queue. The
long waits also stopped when the pod wrote to etcd's own filesystem,
and when the writer ran in the root cgroup, as containerd does when
it unpacks an image. Many NVMe drives already accept requests of at
most 128 KiB, and one such drive wrote 265 to 362 MB/s, so the
default does not limit the speed of a fast disk.

## Change the value or opt out

Each entry of `spec.disks` names one disk and its value:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  storage:
    clusterState:
      device: /dev/disk/by-id/ata-EXAMPLE_SSD_512GB_0123456789
      size: 64Gi
  disks:
    # A larger value on the clusterState disk, in place of the default.
    - device: /dev/disk/by-id/ata-EXAMPLE_SSD_512GB_0123456789
      maxRequestSize: 256Ki
    # A value on a disk that holds no storage role.
    - device: /dev/disk/by-id/ata-EXAMPLE_HDD_4TB_9876543210
      maxRequestSize: 512Ki
```

- `device` takes the names that `spec.storage.<role>.device` takes: a
  device path such as `/dev/sda`, or a name under `/dev/disk/by-id/`
  or `/dev/disk/by-path/`.
  [`status.hardware.blockDevices[].stableNames`](/docs/reference/machine/#statushardwareblockdevices--stablenames)
  lists the names of each disk. The machine looks the name up on every
  boot and every pass, so use a by-id name. A device path can name
  another disk after the kernel finds the disks in a different order.
- `maxRequestSize` is a binary quantity in whole KiB, from `4Ki` up to
  the disk's hardware limit. The value `Kernel` keeps the kernel's own
  value, so it turns off the default on the `clusterState` disk:

  ```yaml
  spec:
    disks:
      - device: /dev/disk/by-id/ata-EXAMPLE_SSD_512GB_0123456789
        maxRequestSize: Kernel
  ```

  When an entry has no `maxRequestSize`, the default applies to that
  disk.

The setting is for a whole disk, not for a partition. Several storage
roles can share one disk, and a disk has one request queue.

## When a value takes effect

`init` writes the values at boot, after it mounts the storage roles
and before k3s starts. `machine-operator` writes them again on each
reconcile pass, so an edit to `spec.disks` takes effect without a
reboot. The kernel sends no event when the attribute changes, so
every ten seconds `machine-operator` also reads each disk that it set,
and writes the value back when another program changed it.

When an edit removes an entry, the disk keeps the value of that entry
until the next boot, as a parameter removed from `spec.sysctls` does.
To return a disk to the kernel's value at once, set it to `Kernel`.

The boot applies the Machine manifest that it booted with. An edit
that needs no reboot does not change that manifest, so until the next
staged change, a boot first applies the values from before the edit,
and `machine-operator` writes the current values when it starts.

## See what is applied

[`status.hardware.blockDevices`](/docs/reference/machine/#statushardwareblockdevices)
reports, for each disk, the value in effect as `maxRequestSize`, read
back from the kernel, and the hardware limit as
`maxHardwareRequestSize`:

```console
$ kubectl get machine node-1 -o jsonpath='{.status.hardware.blockDevices}' | jq '.[] | {name, maxRequestSize, maxHardwareRequestSize}'
{
  "name": "sda",
  "maxRequestSize": "128Ki",
  "maxHardwareRequestSize": "32767Ki"
}
```

The `DiskSettingsApplied` condition is `True` with the reason
`Applied` when every value is in place, and its message names the
value of each disk and where the value came from. It is `True` with
the reason `NothingToApply` when `clusterState` is in memory and
`spec.disks` sets nothing.

## When a value is refused

The machine skips an entry that it cannot apply, and the disk of that
entry keeps the default: `128Ki` on the `clusterState` disk, and the
kernel's value on any other disk. The `DiskSettingsApplied` condition
is then `False`, and the machine is Degraded. The reason is the one
of the first refused entry, and the message names every refused
entry:

| Reason | Cause | Fix |
| --- | --- | --- |
| `DiskNotFound` | No attached disk has the name in `device`. | Correct the name, or attach the disk. The machine applies the entry when the disk appears. |
| `AboveHardwareLimit` | The value is larger than the disk's `maxHardwareRequestSize`. | Use a value at or below the limit. |
| `InvalidSetting` | The value is not `Kernel` or a whole number of KiB of at least `4Ki`, the name is a `by-uuid` name or matches two disks, or two entries name the same disk. When two entries name one disk, the machine uses the first one. | Correct the entry. |
| `ApplyFailed` | The kernel refused the write. The message has the kernel's error. | Read the error. |

The API server refuses most of these mistakes when you apply the
Machine. A manifest on an install stick does not pass through the API
server, and only the machine knows a disk's hardware limit, so the
machine checks each entry again. At boot, `init` prints each refused
entry on the console.

The kernel also refuses a value above the hardware limit, or lowers it
to the limit, depending on its version. `liken` refuses the value on
every kernel, so the condition shows that the disk cannot take it.
