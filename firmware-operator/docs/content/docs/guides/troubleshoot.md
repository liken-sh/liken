---
title: Troubleshoot uploads, scans, approvals, rehearsals, and applies
weight: 30
description: "Find why a Firmware's upload failed or never started: a Failed entry and its message, a Conflict between two rules, a selector that does not compile or matches nothing, an upload pod that stays Pending, and a claimed device that stays Pending because its firmware is not loaded. Find why discovery found nothing: a FirmwareCatalog that is not Ready, a scan record that is Failed or CatalogMoved, a machine that is never scanned, and a discovered Firmware that keeps the entry of a machine that is off. Find why an approval's Approved condition is False: CabinetDiffers, NotOffered, or MachinesInvalid, and why the API server refuses an approval. Find why the Rehearsed condition is False with Refused or Failed, or stays Unknown, from the message of each record in status.rehearsals. Find why the Applied condition is False with Refused or Failed, or stays Unknown, from the message of each record in status.applies and the Disruption that asks for the machine's turn, and release a machine that stays drained. Use when kubectl get firmware shows Failed or Conflict, when a bare chip gets no upload, when a pod that claims a camera does not start, when kubectl get firmwarecatalog shows Ready False, when a machine has no scan record, when kubectl get firmware shows Approved False, when it shows Rehearsed or Applied False or Unknown, or when a machine stays drained in a turn that the operator holds."
---

# Troubleshoot uploads, scans, approvals, rehearsals, and applies

The first part of this guide covers uploads, the second part covers
the scans that find updates ([Find firmware updates](../find-updates/)),
the third part covers approvals
([Approve a firmware update](../approve-an-update/)), the fourth part
covers the rehearsal of an approved install on each machine, and the
fifth part covers the apply of the install in each machine's reboot
turn.

## Uploads

Start from the `Firmware`. `kubectl get firmware` shows whether the
rule is valid and counts the result of each port where it matched a
device. `kubectl describe firmware <name>` shows the entries in
`status.devices` and the `Event`s of the last hour. Find the symptom
below.

### The entry is Failed

The entry's `message` says why. The operator does not retry a failed
upload. A new attempt starts when the device appears again, after a
power cycle or a replug, or when you edit the `Firmware`'s spec.

| Message | Cause and fix |
|---|---|
| `the loader exited with code <n>: <line>` | The loader failed. The last line of its log follows the code. Check the command, the path of the firmware file in the image, and the loader type, such as `-t fx2` or `-t fx3`. A loader that runs only as root fails here, because the upload pod runs as user 1000. |
| `the loader exited 0 and the device stayed in the slice for 1m0s after it` | The loader reported success, and the bare chip did not reset. The firmware file is usually wrong for the chip, or the chip needs a second stage, such as `fxload -s` for some older QHY cameras. |
| `the upload pod did not finish within 5m0s of its creation` | The pod never ran, or ran for five minutes. See [The upload pod stays Pending](#the-upload-pod-stays-pending). |
| `the upload pod failed: DeadlineExceeded: ...` | The loader ran longer than five minutes, and the kubelet stopped it. |
| `the upload pod is gone; ...` | Someone deleted the upload pod before the operator read its result. |

To try again without a replug, edit the spec, for example with an
argument that the loader accepts, or delete the `Firmware` and apply
it again.

### The entry is Conflict

Two or more `Firmware` objects match the same device, so the operator
runs none of them on it. The message names the other rules:

```sh
kubectl get firmware -o custom-columns=NAME:.metadata.name,SELECTOR:.spec.selector
```

Narrow one selector, for example with the product ID, or delete the
duplicate. The rule that still matches then uploads to the device
that is in the slice.

### Valid is False

The selector or `spec.machines` does not compile, and the rule runs
nowhere. The condition's message gives the compiler's error:

```sh
kubectl get firmware <name> -o jsonpath='{.status.conditions[?(@.type=="Valid")].message}'
```

A selector reads attributes as `device.attributes["liken.sh"].<name>`.
An attribute that a device does not have makes the selector fail on
that device, and the device counts as not matched. Guard such an
attribute with `has()`, for example
`has(device.attributes["liken.sh"].serial)`.

### No entry appears

The rule matched no device. Check the device in the machine's slice:

```sh
kubectl get resourceslice <node>-liken.sh -o yaml
```

- **The device is not in the slice.** The OS publishes a USB device
  only when a driver binds it or it has a usbfs node. `liken`'s
  [hardware modules guide](https://liken.sh/docs/guides/hardware-modules/)
  covers a device that the machine does not publish.
- **The IDs differ.** Compare the `vendor` and `product` attributes
  with the selector. Each is four lowercase hexadecimal digits, as a
  string.
- **The device has no `busNumber` or `deviceNumber`.** The operator
  uploads only to a device with both, because it builds the usbfs path
  from them. An older `liken` release does not publish them; upgrade
  the machine.
- **`spec.machines` leaves the machine out.** Compare the selector
  with the labels of the `Machine`:
  `kubectl get machine <node> --show-labels`.

### The upload pod stays Pending

The entry is `Running`, and the pod in `liken-system` does not start:

```sh
kubectl -n liken-system get pods -l firmware.liken.sh/upload=true
kubectl -n liken-system describe pod <pod>
```

- **The claim is not allocated.** The claim has the pod's name, and
  selects the device by its port path and device number. When the
  device left or enumerated again before the scheduler allocated the
  claim, the claim matches nothing. The operator writes `Failed` five
  minutes after it created the pod, and the new appearance gets its
  own upload. Check the class with
  `kubectl get deviceclass firmware-upload`: the base ships it.
- **The image does not pull.** The pod's `Event`s name the error.
  Check the tag in `spec.upload.image`. The network policy that
  denies the pod's traffic does not apply to the pull, because the
  node's container runtime pulls the image, not the pod.

### A pod that claims the loaded device stays Pending

A pod that claims the camera by its loaded IDs, such as `0921`, stays
`Pending` until the device with those IDs is in the slice. That is
the expected wait while the upload runs. When the upload is `Failed`
or `Conflict`, fix that first. When the camera has no `Firmware` at
all, nothing loads it, and the pod waits for good.

## Scans

Start from the `FirmwareCatalog`. `kubectl get firmwarecatalog` shows whether the
last read of the jcat succeeded, and `kubectl describe firmwarecatalog <name>`
shows its `Event`s of the last hour. The record of each machine is in
`status.machines`:

```sh
kubectl get firmwarecatalog <name> -o jsonpath='{range .status.machines[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

### A FirmwareCatalog is not Ready

`Ready` is `False` when the operator could not read the jcat or the
file it names. The condition's message gives the error. The status
keeps the last good `file`, and no scan starts while `Ready` is `False`.

```sh
kubectl get firmwarecatalog <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
```

| Reason | Cause and fix |
|---|---|
| `JcatUnreadable` | The operator could not download the jcat at `spec.url` with `.jcat` added, the answer was not `200`, or the jcat did not parse. Check `spec.url`, and check that the operator's pod can reach the host over HTTPS. The `firmware-scan` policy applies only to the scan pods. |
| `FileUnreadable` | The operator downloaded the jcat, and could not download or read the versioned file that it names. The file must be in the same directory as `spec.url`. A mirror must serve the versioned file under the name the jcat gives. |
| `ChecksumMismatch` | The SHA-256 of the versioned file differs from the one in the jcat. A mirror that serves a stale jcat, or a stale file, causes this. Fix the mirror. |

The operator reads the jcat again after an hour, when you edit the
`FirmwareCatalog`, and when the operator starts. To read it now, edit the
`FirmwareCatalog` or restart the operator.

### A scan record is Failed

The record's `message` says why. A failed scan does not run again
until the machine boots, its set of device names changes, or the
catalog names a new file that offers other releases for its devices.

| Message | Cause and fix |
|---|---|
| `downloading the jcat: ...` or `downloading <file>: ...` | The scan pod could not reach the catalog's host. Check the `NetworkPolicy` `firmware-scan`, a policy of your own that denies egress from `liken-system`, and DNS. A mirror on a port other than 443 needs a policy of its own. |
| `the SHA-256 of <file> is <a>, and the operator expects <b>` | The scan pod downloaded a file whose SHA-256 differs from the one the operator read. A mirror that serves different bytes to two readers causes this. |
| `mounting efivarfs at /sys/firmware/efi/efivars: ...` | The pod could not mount `efivarfs`. Check that the pod is privileged, and that the policy of your cluster admits a privileged pod. |
| `fwupdtool refresh: ...` | fwupd refused the catalog, for example because the signature of the jcat does not verify. The message is the last line of fwupd's error. |
| `fwupdtool get-devices: ...` or `fwupdtool get-updates: ...` | fwupd failed to read the devices or to match the releases. The message is the last line of fwupd's error. |
| `the scan pod did not finish within 10m0s of its creation` | The pod never ran, or ran for 10 minutes. Check for a pod that stays `Pending` or does not pull its image. |
| `the scan pod wrote no log: ...` | The pod ended with no log, for example with `DeadlineExceeded`. The message adds the pod's reason and, if the container ran, its exit code. |
| `the scan wrote no result; its last line was: ...` | The scan program crashed. The message holds the last line of its log. |
| `the scan pod is gone; ...` | Someone deleted the scan pod before the operator read its result. |

The operator deletes a scan pod after it writes the result, so the
log of a failed scan is gone. Reboot the machine to run the scan again.
While a scan runs, its pod is in `liken-system`:

```sh
kubectl -n liken-system get pods -l firmware.liken.sh/scan=true
kubectl -n liken-system logs <pod>
```

### A scan record is CatalogMoved

The scan pod read a jcat that names another file than the one the
operator gave it, because LVFS published a new catalog between the
operator's read and the scan's. The record's message gives the new
file name. The operator reads the jcat at once. When the jcat names the
new file, the `FirmwareCatalog`'s `status.file` changes, and the machine is
scanned against the new file. This needs no action.

When the record stays `CatalogMoved`, the jcat that the operator reads
still names the old file. The machine is scanned again when that jcat
names a new file.

### A machine is never scanned

A machine has no record in `status.machines`. Check these in order:

- **The machine is not Ready.** The operator scans only a `Machine`
  whose `status.phase` is `Ready`. Check with
  `kubectl get machine <node>`.
- **The FirmwareCatalog is not Ready.** No scan starts against it. See
  [A FirmwareCatalog is not Ready](#a-firmwarecatalog-is-not-ready).
- **The API server refuses the scan pod.** A privileged pod fails the
  admission of a `liken-system` that carries a Pod Security level below
  `privileged`. The operator's log gives the error:
  `kubectl -n liken-system logs deployment/firmware-operator`.
- **The scan pod cannot reach the catalog.** The record is `Running`
  until the scan fails with a `downloading` message, up to 10 minutes
  after the pod started. See
  [A scan record is Failed](#a-scan-record-is-failed).
- **The operator does not lead.** Only the copy that holds the `Lease`
  scans. See [Confirm that it leads](../install/#confirm-that-it-leads).

### A discovered Firmware keeps an entry for a machine that is off

This is expected. A scan replaces the entries of its own machine only,
so the entries of a machine that is off, or that cannot be scanned,
stay at the version and the releases of its last scan. The `time` of
each entry says when that was. An entry leaves when the machine's next
scan does not report the device, or when you delete the `Machine`.

The operator deletes a discovered `Firmware` when no entry is left and
nobody edited its spec. An object whose spec someone edited stays, and
you delete it with `kubectl delete firmware <name>`. The operator then
creates it again at the next scan that reports a device of its group.

## Approvals

`kubectl get firmware` shows the `Approved` condition in the
`APPROVED` column. The condition's message names the machines, so read
it first:

```sh
kubectl get firmware <name> -o jsonpath='{.status.conditions[?(@.type=="Approved")].message}'
```

### The API server refuses the approval

The CRD checks an approval before the operator sees it:

- `an approval needs both version and cabinet`: add the one that is
  missing.
- `only an update of a component takes a version`: the `Firmware` is
  for a set of GUIDs, such as `uefi-ca-7420e8dc`. No catalog offers its
  device a release, so it has nothing to approve.
- `machines limits an approval`: `spec.machines` needs
  `spec.update.version`.
- A `sha256` that is not 64 lowercase hex digits, or a `url` that does
  not start with `https://`, fails the schema.

### Approved is False with CabinetDiffers

A catalog offers the version you approved, but in a cabinet whose
SHA-256 is not the one you pinned. Compare your `spec.update.cabinet.sha256`
with the release's `cabinet.sha256` in `status.machines`. When you
copied it wrong, fix it. When you copied it right, the catalog serves
another file under the same version. Read the release again, and
decide whether to approve the new file.

### Approved is False with NotOffered

No catalog offers the version to a machine that the approval covers.
Check each of these:

- **The version.** It must match a release's `version` in
  `status.machines` exactly, as a string.
- **`spec.machines`.** It must select a `Machine` that has an entry in
  `status.machines`. List the labels with
  `kubectl get machine --show-labels`.
- **The catalog withdrew the release.** LVFS can remove a release. The
  scan after that writes entries with no such release, and the
  approval moves to `False`. Choose another release.

### Approved is False with MachinesInvalid

`spec.machines` does not compile, such as a label key with a space in
it. The message gives the error. Fix the selector.

## Rehearsals

`kubectl get firmware` shows the `Rehearsed` condition in the
`REHEARSED` column. The condition's message names each machine with
its rehearsal's message, and `status.rehearsals` holds the records:

```sh
kubectl get firmware <name> -o jsonpath='{range .status.rehearsals[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

A rehearsal runs once for each boot of the machine, the approval, and
the catalog file. So a rehearsal that failed runs again only after the
machine reboots, you change the approval, or the machine's next scan
reads a new catalog file.

### Rehearsed is False with Refused

The install would write something that the operator does not apply:

| Message | What it means |
|---|---|
| `fwupd's uefi_capsule plugin updates ...` | The release is a capsule, such as a board's UEFI firmware. A capsule writes boot variables and a file on the ESP, and `liken` does not apply capsules yet. |
| `the install changed ..., which is not ...`, `the install created ...`, `the install removed ...` | The install writes a variable other than its device's own. |
| `the install wrote ... under the ESP path` | The install stages a file on the ESP. |
| `the install rewrote ... instead of appending to it` | The install replaces the list instead of appending to it. |
| `the append to ... has the attributes ...` | The write is not an authenticated append, so the firmware would check no signature. |
| `the install did not change ...` | The device already holds the release's entries. |
| `the append to ... needs ... bytes, and the variable store has ... free` | The machine's variable store is too full for the write. |

### Rehearsed is False with Failed

The rehearsal could not run:

| Message | What to check |
|---|---|
| `the SHA-256 of the cabinet is ..., and the approval pins ...` | The cabinet at `spec.update.cabinet.url` is not the file you pinned. Check the URL and the SHA-256. |
| `downloading the cabinet: ...` | The pod could not download the cabinet. The policy in `deploy/rehearsal.yaml` allows HTTPS on port 443 only. |
| `the jcat now names ...` | The catalog moved since the machine's last scan. The scan of the new file starts a new rehearsal. |
| `fwupd reports no device ...` | fwupd in the pod does not find the device that the scan reported. |
| `copying the firmware tables: the machine has no UEFI variable store` | The machine booted in BIOS mode. |
| `fwupdtool install: ...` | fwupd refused the install. The rest of the line is fwupd's reason. |
| `the rehearsal pod did not finish within 10m0s of its creation` | The pod never ran, such as an image that does not pull, or it hung. Check `kubectl -n liken-system get events`. |
| `the rehearsal pod is gone` | Someone deleted the pod before it ended. |

### Rehearsed stays Unknown

A pending machine waits for its rehearsal. Check each of these:

- **The machine is not `Ready`.** A rehearsal starts only on a `Ready`
  machine.
- **The machine's last scan read an older catalog file.** The
  rehearsal loads the file that the scan read, so it waits for the scan
  of the current file.
- **The API server refuses the rehearsal pod.** The pod mounts a host
  path, which the `baseline` Pod Security level forbids. A cluster
  that labels `liken-system` with a level below `privileged` refuses
  it. Look for the refusal in the operator's log.

## Applies

`kubectl get firmware` shows the `Applied` condition in the `APPLIED`
column. The condition's message names each machine with its record's
message, and `status.applies` holds the records:

```sh
kubectl get firmware <name> -o jsonpath='{range .status.applies[*]}{.machine}{"\t"}{.result}{"\t"}{.disruption}{"\t"}{.message}{"\n"}{end}'
```

The operator does not write again on its own after a record reads
`Refused` or `Failed`. To try again, withdraw the approval and approve
it again. The withdrawal removes the machine's rehearsal and its
record, so the machine rehearses again, and asks for its turn again
when the rehearsal passes.

### Applied is False with Refused

The message is `the firmware refused the append to <variable>:
permission denied`. The kernel reports the firmware's refusal of an
authenticated write as permission denied. The firmware checks the
append's signature against `KEK`, or against `PK` for a `KEK` update,
and refuses an append whose signer does not chain to one of them. A
`KEK` that holds only an older Microsoft key, while the release is
signed by a newer one, causes this. A refused append changes nothing,
and the operator deletes the `Disruption`, so the machine leaves its
turn with no reboot. Choose the release of the component whose signer
the machine's `KEK` holds, such as `microsoft-dbx-x64` instead of
`microsoft-dbx-x64-kek2023`.

### Applied is False with Failed

| Message | What to check |
|---|---|
| `the machine left its turn before the hold took effect: the Disruption ... is Complete ...` | The machine rebooted without holding its turn, so nothing was written. A `liken` release whose `Disruption` has no `spec.hold` does this. Upgrade the cluster, then approve again. |
| `the machine left its turn before the hold took effect: the Disruption ... is gone ...` | Someone deleted the `Disruption` before the hold. Approve again. |
| `the append's SHA-256 is now ..., and the rehearsal recorded ...` | Another writer changed the machine's variables after the rehearsal, such as another operating system or the firmware's setup menu. Nothing was written. Approve again to rehearse against the variables as they are now. |
| `the rehearsal before the write did not pass, so nothing was written: ...` | The rehearsal in the apply pod failed or was refused. The rest of the message is the rehearsal's, and the [rehearsal tables](#rehearsals) explain it. |
| `the apply pod ended before the signal to write, so nothing was written: ...` | The apply pod stopped while it waited for the machine's turn, such as at a reboot of the machine or when the kernel killed it for memory. The rest of the message gives its last line or the pod's reason. The pod writes only after the signal, so the operator deletes the `Disruption`, and the machine leaves its turn with no reboot. Approve again. |
| `the variable ... changed while the apply waited for its turn, so nothing was written` | Another writer changed the variable after the apply pod prepared and before the signal, such as another operating system or the firmware's setup menu. Approve again to rehearse against the variable as it is now. |
| `waiting for the signal to write: ..., so nothing was written` | The apply pod could not watch its annotations. The rest of the message gives the error. |
| `clearing the immutable flag of ...: operation not permitted` | The pod lacks `CAP_LINUX_IMMUTABLE`. A policy of your cluster that removes capabilities from pods in `liken-system` causes this. |
| `writing the append to ...: read-only file system` | The machine's `efivarfs` is mounted read-only, so the pod's read-write mount of `/sys/firmware` cannot write it. |
| `writing the append to ...: ...` | The kernel refused the write for another reason, which the message gives. |
| `the FirmwareCatalog ... has no current file ...` | The `FirmwareCatalog` of the machine's scan was deleted or never read its jcat. See [A FirmwareCatalog is not Ready](#a-firmwarecatalog-is-not-ready). |
| `the machine rebooted after the write, and its next scan still reads version ..., not ...` | The firmware took the write and did not keep it across the reboot. A firmware that resets Secure Boot to its defaults discards the appended entries. Check the firmware's Secure Boot settings before you approve again. |
| `the machine rebooted, and its next scan still reads version ..., not ..., so the append was not written` | An uncertain apply, below, did not write. Approve again. |

A failed or refused record deletes its `Disruption`, unless the
operator already removed `spec.hold`. Then the turn still reboots the
machine.

### Applied is Unknown with Uncertain

The apply pod ended after the signal to write without saying whether
it wrote. A write may have landed before it stopped, so the operator removes the hold, the
machine reboots in its turn, and the next scan reads the version. The
record then ends, or turns `Failed` when the machine still reads the
old version.

| Message | What to check |
|---|---|
| `the apply pod is gone, ...` | Someone deleted the apply pod before it ended. |
| `the apply pod did not finish within 10m0s of the signal to write` | The pod hung after the signal, or a pod that the operator started at the hold never ran, such as an image that does not pull. Check `kubectl -n liken-system get events`. |
| `the apply wrote no result; its last line was: ...` or `the apply pod wrote no log: ...` | The apply program crashed. The message holds its last line, or the pod's reason. |
| `the apply answered the result ..., which the operator does not know` | The scan image and the operator are from different releases. Pin both to one tag. |

### Applied stays Unknown

Find the machine's record and its `Disruption`:

```sh
kubectl get disruptions -l firmware.liken.sh/firmware=<name>
```

- **The machine has no record.** Its rehearsal has not passed on the
  boot it runs. See [Rehearsals](#rehearsals). The operator also asks
  for no turn on a `liken` release that has no `Disruption` kind.
- **The record is `Waiting`.** The apply pod has prepared, or still
  prepares, and the machine waits for its turn. The pod runs on the
  machine before the drain:
  `kubectl -n liken-system get pods -l firmware.liken.sh/apply=true`.
  Its log ends with a line whose `phase` is `prepared` once it waits.
  The pod mounts a host path, which the `baseline` Pod Security level
  forbids, so a cluster that labels `liken-system` with a level below
  `privileged` refuses it. Look for the refusal in the operator's log.
  An
  `APPROVED` of `False` means the machine's `rebootPolicy` is `Manual`,
  and the request waits for you:
  [Apply the update](../approve-an-update/#apply-the-update) gives the
  command that approves it. A `GRANTED` of `False` means a budget has
  no room, and the `Granted` condition's message names what the turn
  waits for. Once the turn is granted, `machine-operator` drains the
  machine and sets `HELD` to `True`.
- **The record is `Writing`.** The operator sent the apply pod the
  signal, and the pod writes on the drained machine. Its annotation
  `firmware.liken.sh/write-at` gives the time of the signal, and the
  operator waits 10 minutes from it for the pod's answer.
- **The record is `Written` or `Uncertain`.** The machine reboots, and
  its next scan ends the record. A scan waits for the machine to be
  `Ready`.

### A machine stays drained

A machine whose `Disruption` reads `HELD` `True` is drained and waits
for the operator, and a hold has no deadline. The operator removes the
hold after a write, and deletes the `Disruption` after a refusal or a
failure. A machine stays drained when the operator does not run, or
does not lead. Check the operator first: see
[Confirm that it leads](../install/#confirm-that-it-leads). The
operator's pod tolerates the cordon by its key, so it can run on the
drained machine. A `liken` drain that evicts it anyway, on a cluster
whose other machines take no pod of the operator's, leaves the
operator with nowhere to run, and the machine stays drained until you
release it.

To release the machine yourself, remove the hold or delete the request.
Removing the hold reboots the machine:

```sh
kubectl patch disruption <machine>.firmware-<name> --type merge -p '{"spec":{"hold":null}}'
```

Deleting the request ends the turn with no reboot:

```sh
kubectl delete disruption <machine>.firmware-<name>
```

If the record reads `Writing` and the apply pod is still running, wait
for it to end first, so that its result is in the record. When the pod writes, or ends without
saying whether it wrote, after you deleted the request, the operator
files the request again with no hold. The write takes effect only at a
reboot, so the machine takes its turn again, under its budgets, and
reboots.
