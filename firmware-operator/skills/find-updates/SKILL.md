---
name: find-updates
description: "Find which firmware each machine's devices keep and which newer releases the LVFS catalog offers, with the lvfs FirmwareCatalog and the scan pods of firmware-operator. Read kubectl get firmwarecatalog and its status.machines records, kubectl get firmware and the Machines and Behind columns, a discovered Firmware's status.machines entries with their cabinet URL and SHA-256, and the Events. Use when you want to know whether a board's UEFI firmware, an SSD, or a dock has an update, when a scan did not run, or to understand what a scan touches on a machine."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/find-updates/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Find firmware updates

At the end of this guide, you can list the firmware devices of each
machine in your cluster, the version each one runs, and the newer
releases that the Linux Vendor Firmware Service (LVFS) offers for
it. Finding updates changes nothing on a machine.

You need `firmware-operator` running (see the
[install guide](https://liken.sh/firmware/docs/guides/install/)), the `lvfs` `FirmwareCatalog` that the base
installs, and machines that can open HTTPS connections to
`cdn.fwupd.org`. Only a machine whose `Machine` is `Ready` is scanned.

## How scans happen

A `FirmwareCatalog` is a source of firmware releases, and the `lvfs`
object points to the LVFS catalog. The operator reads the catalog's
jcat, a small signed file that lists the current catalog file and its
SHA-256. When the jcat lists a new file, the operator downloads the
file once and checks its SHA-256.

The operator then starts a scan on each machine that needs one. A scan
is a short privileged pod in `liken-system` on that machine. It runs
`fwupd` against the catalog file and reports which firmware devices
the machine has and which releases the catalog offers for each. The
operator scans a machine when:

- The machine has no scan record yet, or it booted since its last
  scan, because a version changes only across a reboot or a flash.
- The set of device names in the machine's `ResourceSlice` changed. A
  device number that changes on a replug does not count.
- The catalog has a new file that offers other releases for the
  devices the machine reported. When the new file offers the same
  releases, the operator writes the new file name into the machine's
  scan record and its `Firmware` entries, and does not scan.
- The scan image differs from the one that ran the machine's last
  scan. An upgrade of `firmware-operator` can bring a newer `fwupd`
  that finds devices and releases the old one did not, so each machine
  is scanned once with the new image.

A scan never starts on a timer. The operator reads the jcat once an
hour, because LVFS publishes no feed of its changes. The operator's
`--catalog-interval` flag sets the interval.

The operator does not retry a failed scan until the machine's boot,
its device names, the catalog file, or the scan image changes. A scan
pod that the kubelet evicted or refused, for a reason other than a
node shutdown, did not fail, so the operator scans the machine again
in a new pod, at most five pods for one scan, as
[A pod that the kubelet ended](https://liken.sh/firmware/docs/guides/troubleshoot/#a-pod-that-the-kubelet-ended)
describes. No command forces a scan. To scan a machine again,
reboot it. To scan every machine again, delete the `FirmwareCatalog`
and apply it again. The new object has no scan records, so the operator
scans each `Ready` machine. The discovered `Firmware` objects stay, and
the new scans replace their entries.

## Read the FirmwareCatalog

```sh
kubectl get firmwarecatalog
```

```
NAME   FILE                            READY   CHECKED                AGE
lvfs   firmware-09138-stable.xml.zst   True    2026-10-20T15:10:00Z   2d
```

`FILE` is the catalog file that the last good read of the jcat
listed. `READY` is `False` when the operator could not read the jcat
or the file, and the [troubleshoot guide](https://liken.sh/firmware/docs/guides/troubleshoot/#a-firmwarecatalog-is-not-ready)
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
| `Scanned` | The scan finished, and the discovered `Firmware` objects list its devices. |
| `Failed` | The scan failed. The `message` says why. |
| `CatalogMoved` | The jcat listed another file than the one the scan was given. |

A record also has the `boot` time and the `file` of its scan, a hash
of the device names in `devices`, and the `guids` of the updatable
devices. The operator decides whether a machine needs a scan from
these fields, so a restart of the operator does not scan again.

## A catalog that the LVFS did not sign

fwupd reads a catalog only when it can verify the catalog's jcat
against a key that it trusts, and the scan image trusts only the
LVFS's keys. A vendor's own catalog or a test catalog is signed by
another key, so you must give the operator the signer's certificate.
Put the certificate in a `ConfigMap` in the operator's namespace. fwupd
reads the key's file extension to tell what kind of certificate it is,
so name a PEM certificate's key with `.pem`:

```sh
kubectl -n liken-system create configmap acme-catalog-signer --from-file=signer.pem
```

Then set the `ConfigMap`'s name in the catalog's `spec.trust`:

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: FirmwareCatalog
metadata:
  name: acme
spec:
  url: https://firmware.acme.example/firmware.xml.zst
  trust:
    configMapRef:
      name: acme-catalog-signer
```

Each scan, rehearsal, and apply pod for this catalog mounts the
`ConfigMap` and adds its certificates to the LVFS's keys before it
reads the catalog. The certificates apply only in that pod, so a scan
for another catalog does not trust this signer. When the `ConfigMap`
does not exist, the kubelet cannot start the scan pod, and its record
becomes `Failed` at the scan's deadline, 10 minutes after the operator
created the pod.

The scan, rehearsal, and apply pods can connect only to DNS and to
port 443. A catalog served on another port, such as a mirror over HTTP,
needs a `NetworkPolicy` of your own that lets the pods connect to it.
Kubernetes allows a connection that any policy allows, so yours only
needs the mirror's address and port:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: firmware-acme-catalog
  namespace: liken-system
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/managed-by: firmware-operator
  policyTypes: [Egress]
  egress:
    - to: [{ipBlock: {cidr: 192.0.2.10/32}}]
      ports: [{protocol: TCP, port: 80}]
```

## Read the discovered Firmware objects

The operator creates one `Firmware` for each catalog component that
the catalog offers to a device that a scan reports. Its name is the component
ID with no top-level domain and no `firmware` part:
`com.microsoft.dbx.x64.firmware` becomes `microsoft-dbx-x64`, and
`work.frame.Laptop.RyzenAI300.BIOS.firmware` becomes
`frame-laptop-ryzenai300-bios`. When another `Firmware` already has
that name, the operator uses the whole component ID in lowercase. An
updatable device that no catalog offers a release for gets an object
named from the device's name and a hash of its GUIDs, such as
`uefi-ca-7420e8dc`. When the catalog offers that device a release,
the operator moves its entry to the component's object. [How discovery
works](https://liken.sh/firmware/docs/concepts/discovery/#one-firmware-for-each-catalog-component)
explains why. The operator labels these objects
`firmware.liken.sh/discovered`, so you can list them apart from your
upload rules:

```sh
kubectl get firmware -l firmware.liken.sh/discovered
```

```
NAME                           VALID   LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION   APPROVED   PENDING   REHEARSED   APPLIED   AGE
frame-laptop-ryzenai300-bios                                                3          1                             0                               2d
microsoft-dbx-x64                                                           3          2                             0                               2d
uefi-ca-7420e8dc                                                            1          0                             0                               2d
```

`MACHINES` counts the machines that have an entry in the object, and
`BEHIND` counts the machines that the catalog offers at least one
release to. A machine whose device runs the newest release counts in
`MACHINES` and not in `BEHIND`. The upload columns, from `VALID` to
`CONFLICT`, are empty for a discovered `Firmware`. `VERSION`,
`APPROVED`, `PENDING`, `REHEARSED`, and `APPLIED` show an approval,
which [Approve a firmware update](https://liken.sh/firmware/docs/guides/approve-an-update/) covers.

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

Each entry has the device's name in `device` and fwupd's ID for it in
`deviceId`, and the version the device runs in `version`. `catalog`
and `file` give the `FirmwareCatalog` and the catalog file that the
scan matched the device against. `releases` lists the releases that
fwupd offers, newest first, each with the URL and SHA-256 of its
cabinet file. When `releases` is empty, the device runs the newest
release that the catalog has for it. In the example, a Framework
Laptop 13 runs BIOS 0.0.3.5, and the catalog offers 0.0.4.2.

A scan replaces its machine's entries for its `FirmwareCatalog` in
every discovered object. The operator removes an entry only when the
machine's next scan does not report the device, or when the machine's
`Machine` is deleted. So a machine that is off keeps the entries of
its last scan, and the `time` of each entry shows when that was.

The operator deletes a discovered object when it has no entries,
unless someone changed its `spec.update.component` or
`spec.update.guids`, or an apply on it waits for its reboot turn or is
writing. An approval alone does not keep the object, because with no
entries no machine is offered the approved release.

## Read the Events

The operator posts `Event`s in the namespace `default`, because both
kinds are cluster-scoped. `kubectl describe firmwarecatalog lvfs` and
`kubectl describe firmware <name>` show the last hour of them.

| Object | Reason | When |
|---|---|---|
| `FirmwareCatalog` | `Fetched`, `JcatUnreadable`, `FileUnreadable`, or `ChecksumMismatch` | The `Ready` condition changed. A change to `False` is a `Warning`. |
| `FirmwareCatalog` | `ScanStarted` | The operator created a scan pod. |
| `FirmwareCatalog` | `ScanPodRecreated` | The kubelet ended a scan pod before the scan finished, and the operator created the scan's first next pod. Later pods of the same scan post none. |
| `FirmwareCatalog` | `Scanned` | A scan finished. The message gives the machine and what it found. |
| `FirmwareCatalog` | `ScanFailed` | A scan failed. This is a `Warning`. |
| `FirmwareCatalog` | `CatalogMoved` | A scan found another file in the jcat than the file it was given. |
| `Firmware` | `Discovered` | The operator created the object for a new component or set of GUIDs. |
| `Firmware` | `Deleted` | The operator deleted the object, because it has no entries and nothing above keeps it. The `Event` remains for an hour after the object is gone. |
| `Firmware` | `UpdateAvailable` | The newest release offered for a machine's device changed. |
| `Firmware` | `ManyComponents` | A catalog offered one device releases of more than one component. The device's entry is in the object of the newest release's component, and the message lists the other components. This is a `Warning`. |

## What a scan touches

A scan reads the machine and changes nothing on it. The scan pod:

- is privileged, because fwupd reads `sysfs` and device nodes. It has
  no device claim, because fwupd reads every device it finds.
- mounts the machine's `/sys/firmware` read-only and gives fwupd a
  copy of the EFI variable store, the firmware resource table, and the
  SMBIOS tables. fwupd's UEFI capsule plugin finds devices only in a
  variable store that it can write to, so the copy is a directory in
  memory, and the scan cannot write to the machine's own variables.
- gives fwupd an empty directory in place of the EFI system partition
  (ESP), so fwupd reads and writes no real ESP.
- runs only the `fwupdtool` commands `refresh`, `get-devices`, and
  `get-updates`, so it stages no capsule and writes nothing to the boot
  chain.
- has no service account token, and an egress policy that allows only
  DNS and port 443.

The operator reads the result from the pod's log, writes it to the
status, and deletes the pod. A scan that is still running after 10
minutes becomes `Failed`.

## What comes next

To choose a release and have the operator install it, read
[Approve a firmware update](https://liken.sh/firmware/docs/guides/approve-an-update/). [How discovery
works](https://liken.sh/firmware/docs/concepts/discovery/) explains why a scan changes nothing
on a machine.
