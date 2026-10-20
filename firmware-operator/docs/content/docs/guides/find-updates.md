---
title: Find firmware updates
weight: 25
description: "Find which firmware each machine's devices keep and which newer releases the LVFS catalog offers, with the lvfs FirmwareCatalog and the scan pods of firmware-operator. Read kubectl get firmwarecatalog and its status.machines records, kubectl get firmware and the Machines and Behind columns, a discovered Firmware's status.machines entries with their cabinet URL and SHA-256, and the Events. Use when you want to know whether a board's UEFI firmware, an SSD, or a dock has an update, when a scan did not run, or to understand what a scan touches on a machine."
---

# Find firmware updates

At the end of this guide, you can list the firmware devices of each
machine in your cluster, the version each one runs, and the newer
releases that the Linux Vendor Firmware Service (LVFS) offers for
it. `firmware-operator` finds these updates and installs none of
them.

You need `firmware-operator` running (see the
[install guide](../install/)), the `lvfs` `FirmwareCatalog` that the base
installs, and machines that can open HTTPS connections to
`cdn.fwupd.org`. Only a machine whose `Machine` is `Ready` is scanned.

## How scans happen

A `FirmwareCatalog` is one source of firmware releases. The `lvfs` object
names the LVFS catalog. The operator reads the catalog's jcat, a
small file that names the current catalog file and its SHA-256. When
the jcat names a new file, the operator downloads that file once and
checks its SHA-256.

The operator then starts a scan on each machine that needs one. A scan
is a short privileged pod in `liken-system` on that machine. It runs
`fwupd` against the catalog file, and writes which firmware devices
the machine has and which releases the catalog offers for each. The
operator scans a machine in three cases:

- The machine has no record yet, or it booted since its last scan. A
  version changes only across a reboot or a flash.
- The set of device names in the machine's `ResourceSlice` changed. A
  device number that changes on a replug does not count.
- The catalog named a new file, and that file offers other releases for
  the devices the machine reported. When the new file offers the same
  releases, the operator records the new file for the machine and
  scans nothing.

A scan never starts on a timer. The operator reads the jcat once an
hour (the `--catalog-interval` flag sets the time), because LVFS
publishes no feed of its changes. An unchanged jcat costs one small
answer.

Each combination of boot, device set, and catalog file runs once. A
failed scan does not run again until one of the three changes. No
command forces a scan. To scan a machine again, reboot it. To scan
every machine again, delete the `FirmwareCatalog` and apply it again: the new
object has no records, so each `Ready` machine is scanned. The
discovered `Firmware` objects stay, and the new scans replace their
entries.

## Read the FirmwareCatalog

```sh
kubectl get firmwarecatalog
```

```
NAME   FILE                            READY   CHECKED                AGE
lvfs   firmware-09138-stable.xml.zst   True    2026-10-20T15:10:00Z   2d
```

`FILE` is the catalog file that the last good read of the jcat
named. `READY` is `False` when the jcat or the file could not be read,
and the [troubleshoot guide](../troubleshoot/#a-firmwarecatalog-is-not-ready)
lists the reasons. `CHECKED` is the time of the last read of the jcat.

The record of each machine's last scan is in `status.machines`:

```sh
kubectl get firmwarecatalog lvfs -o jsonpath='{range .status.machines[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

```
node-1   Scanned   4 devices, no updates
node-2   Scanned   3 devices, 1 with updates
node-3   Failed    downloading the jcat: Get "https://cdn.fwupd.org/downloads/firmware.xml.zst.jcat": dial tcp: i/o timeout
```

The `result` of a record is one of these:

| Result | Meaning |
|---|---|
| `Running` | The scan pod is running. |
| `Scanned` | The scan answered, and the discovered `Firmware` objects hold its devices. |
| `Failed` | The scan failed. The `message` says why. |
| `CatalogMoved` | The jcat named another file than the one the scan was given. |

A record also holds the `boot` time and the `file` of its scan, the
hash of the device names in `devices`, and the `guids` of the updatable
devices. The operator reads these after a restart, so a restart starts
no second scan.

## Read the discovered Firmware objects

The operator keeps one `Firmware` for each catalog component that
offers a release to a device a scan reports. Its name is the component
ID with no top-level domain and no `firmware` part:
`com.microsoft.dbx.x64.firmware` becomes `microsoft-dbx-x64`, and
`work.frame.Laptop.RyzenAI300.BIOS.firmware` becomes
`frame-laptop-ryzenai300-bios`. When another `Firmware` already has
that name, the operator uses the whole component ID in lowercase. An
updatable device that no catalog has offered a release gets an object
named by the device and a hash of its GUIDs, such as
`uefi-ca-7420e8dc`, and the operator moves its entry to the
component's object when a release appears. [How discovery
works](../../concepts/discovery/#a-firmware-is-a-catalog-component)
gives the reasons. The label `firmware.liken.sh/discovered` marks
these objects, so you can list them apart from your upload rules:

```sh
kubectl get firmware -l firmware.liken.sh/discovered
```

```
NAME                           VALID   LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION   APPROVED   PENDING   AGE
frame-laptop-ryzenai300-bios                                                3          1                             0         2d
microsoft-dbx-x64                                                           3          2                             0         2d
uefi-ca-7420e8dc                                                            1          0                             0         2d
```

`MACHINES` counts the machines that have an entry in the object.
`BEHIND` counts the machines for which the catalog offers at least one
release. A machine whose device installed the newest release stays in
`MACHINES` and leaves `BEHIND`. A discovered `Firmware` has no `Valid` condition and no
upload counts, so those columns are empty. `VERSION`, `APPROVED`, and
`PENDING` belong to an approval, which
[Approve a firmware update](../approve-an-update/) covers.

The entries are in `status.machines`, one for each machine's device:

```sh
kubectl get firmware frame-laptop-ryzenai300-bios -o yaml
```

```yaml
spec:
  update:
    component: work.frame.Laptop.RyzenAI300.BIOS.firmware
status:
  counts:
    behind: 1
    machines: 3
  machines:
    - machine: node-2
      device: System Firmware
      deviceId: 3fcf9b4a0e1d2c5b6a7f8e9d0c1b2a3f4e5d6c7b
      version: 0.0.3.5
      catalog: lvfs
      file: firmware-09138-stable.xml.zst
      releases:
        - version: 0.0.4.2
          id: work.frame.Laptop.RyzenAI300.BIOS.firmware
          cabinet:
            url: https://fwupd.org/downloads/9c085d03…-Framework_Laptop_13_RyzenAI300_fwupd_4.02.cab
            sha256: 9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5
            size: 36134500
      time: "2026-10-20T15:12:04Z"
```

Each entry says which device it is, by name and by fwupd's ID
(`device` and `deviceId`), which version the device runs (`version`), which
`FirmwareCatalog` and which catalog file the scan matched it against
(`catalog` and `file`), and the releases fwupd offers, newest first.
An empty `releases` means the device runs the newest release the
catalog has for it. A release gives the URL of its cabinet file and the
cabinet's SHA-256. The example is a Framework Laptop 13 that runs
BIOS 0.0.3.5, and the catalog offers 0.0.4.2.

A scan replaces its machine's entries for its `FirmwareCatalog` in every
discovered object. An entry leaves only when that machine's next scan
does not report the device, or when its `Machine` is deleted. So a
machine that is off keeps the entries of its last scan, and the `time`
of each entry says how old they are.

The operator deletes a discovered object when no entry is left and
nobody edited its spec. It never deletes an object that someone
changed.

## Read the Events

The operator posts an `Event` for each step, in the namespace
`default`. `kubectl describe firmwarecatalog lvfs` and
`kubectl describe firmware <name>` show the last hour of them.

| Object | Reason | When |
|---|---|---|
| `FirmwareCatalog` | `Fetched`, `JcatUnreadable`, `FileUnreadable`, or `ChecksumMismatch` | The `Ready` condition changed. A change to `False` is a `Warning`. |
| `FirmwareCatalog` | `ScanStarted` | The operator created a scan pod. |
| `FirmwareCatalog` | `Scanned` | A scan answered. The message gives the machine and what it found. |
| `FirmwareCatalog` | `ScanFailed` | A scan failed. This is a `Warning`. |
| `FirmwareCatalog` | `CatalogMoved` | A scan found another file in the jcat than the one it was given. |
| `Firmware` | `Discovered` | The operator created the object for a new component or set of GUIDs. |
| `Firmware` | `UpdateAvailable` | The newest release offered for a machine's device changed. |
| `Firmware` | `ManyComponents` | A catalog offered one device releases of more than one component. The device's entry is in the object of the newest release's component, and the message names the others. This is a `Warning`. |

## What a scan touches

A scan reads the machine and changes nothing on it. The pod:

- is privileged, because fwupd reads the SMBIOS tables, `efivarfs`,
  `sysfs`, and device nodes, and it holds no device claim, because
  fwupd reads many devices;
- mounts `efivarfs` read-only, because fwupd's UEFI plugin reads the
  firmware resource table of the machine from it;
- gives fwupd an empty directory in place of the EFI system partition
  (ESP), so fwupd reads and writes no ESP;
- runs only the `fwupdtool` commands `refresh`, `get-devices`, and
  `get-updates`, so it stages no capsule and writes nothing to the boot
  chain;
- has no service account token, and an egress policy that allows only
  DNS and port 443.

The operator reads the result from the pod's log, writes it to the
status, and deletes the pod. A scan that is still running after 10
minutes is `Failed`.

## What comes next

This release finds updates and records them, and
[Approve a firmware update](../approve-an-update/) records which
release you want. It does not apply one. A later release adds the
apply. Until then, no `Firmware` that the operator discovers changes a
device. [How discovery works](../../concepts/discovery/) gives the
reasons for the design.
