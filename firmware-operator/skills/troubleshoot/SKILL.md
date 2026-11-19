---
name: troubleshoot
description: "Find why a Firmware's upload failed or never started: a Failed entry and its message, a Conflict between two rules, a selector that does not compile or matches nothing, an upload pod that stays Pending, and a claimed device that stays Pending because its firmware is not loaded. Find why discovery found nothing: a FirmwareCatalog that is not Ready, a scan record that is Failed or CatalogMoved, a machine that is never scanned, and a discovered Firmware that keeps the entry of a machine that is off. Find why an approval's Approved condition is False: CabinetDiffers, NotOffered, or MachinesInvalid, and why the API server refuses an approval. Find why the Rehearsed condition is False with Refused or Failed, or stays Unknown, from the message of each record in status.rehearsals. Find why the Applied condition is False with Refused or Failed, or stays Unknown, from the message of each record in status.applies and the Disruption that requests the machine's turn to reboot, and release a machine that stays drained. Use when kubectl get firmware shows Failed or Conflict, when a bare chip gets no upload, when a pod that claims a camera does not start, when kubectl get firmwarecatalog shows Ready False, when a machine has no scan record, when kubectl get firmware shows Approved False, when it shows Rehearsed or Applied False or Unknown, or when a machine stays drained in a turn that the operator holds. Find why a scan or a rehearsal record stays Running after the kubelet evicted or refused its pod."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/troubleshoot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Troubleshoot uploads, scans, approvals, rehearsals, and applies

Each section starts from the object to read, then lists symptoms and
their fixes. [Find firmware updates](https://liken.sh/firmware/docs/guides/find-updates/) explains scans,
and [Approve a firmware update](https://liken.sh/firmware/docs/guides/approve-an-update/) explains
approvals. A rehearsal is a test run of an approved install on each
machine, against a copy of the machine's firmware variables. An apply
is the real install. It runs while the machine is drained in a reboot
turn, which `machine-operator` grants through a `Disruption`, as
[Apply the update](https://liken.sh/firmware/docs/guides/approve-an-update/#apply-the-update) describes.

## A pod that the kubelet ended

The kubelet can end a pod for a reason that is not the pod's own
program. It evicts a pod when the node runs low on memory or disk, it
refuses a pod at admission when the node has no room for it, and it
ends each pod when the node shuts down. The scheduler can also preempt
a pod. Such a pod has the phase `Failed` and a pod-level `reason`, such
as `Evicted`, `OutOfcpu`, `OutOfmemory`, `NodeAffinity`, `Terminated`,
or `Shutdown`, or the condition `DisruptionTarget`. A pod that ends
with `DeadlineExceeded`, or whose program exits with a code other than
0, failed on its own, and the sections below describe it.

A scan pod and a rehearsal pod write nothing to the machine's
firmware, so when the kubelet ends one, the operator records no
failure and runs the same scan or rehearsal again in a next pod. The
next pod has the annotations of the ended pod, and the annotation
`firmware.liken.sh/tries` counts the earlier pods of the attempt that
the kubelet ended. Until the next pod ends, the record stays `Running`,
and its message names the ended pod, the kubelet's reason, the next
pod, and when the next pod starts:

```text
the kubelet ended the scan pod liken-system/lvfs-node-2-1e9f42f0e0cd: OutOfcpu: Node didn't have enough resource: cpu, requested: 10, used: 3990, capacity: 4000, so the operator scans the machine again in the scan pod liken-system/lvfs-node-2-5d0b7c2e91a4 from 2026-11-19T10:00:10Z
```

Both pods name their machine in `spec.nodeName`, so the scheduler does
not check them, and the kubelet can refuse the next pod for the same
reason. So the next pod starts 10 seconds after the creation of the
pod that the kubelet ended, and the wait doubles for each later pod of
the same attempt, up to 5 minutes. An eviction can also be the pod's
own fault, so an attempt runs at most five pods. When the kubelet ends
the fifth, the record is `Failed`, and its message gives the kubelet's
reason and says that the operator stopped after five pods.
`kubectl describe node <machine>` shows the node's conditions and
taints.

The operator does not run these again:

- **A pod that the kubelet ended for a node shutdown**, with the reason
  `Terminated` or `Shutdown`, or refused while the node shut down, with
  the reason `NodeShutdown`. The kubelet ends the pod before the
  machine reboots, and the machine's next boot needs a scan and a
  rehearsal of its own. The record is `Failed` with the kubelet's
  reason, and the operator starts the new boot's scan when the machine
  is `Ready` again.
- **A pod of an attempt that the machine no longer needs**, because its
  devices, the catalog file, the scan image, or the approval changed.
  The record is `Failed` with the kubelet's reason, and the attempt
  that the machine needs now starts.
- **An upload pod or an apply pod.** Each of them writes to a device,
  and the kubelet can end one at a node shutdown before the `Machine`
  reports the new boot. A next pod could then write on a boot that its
  attempt was not made for. So the entry or the record is `Failed`, as
  for any other end, and an apply pod that the kubelet ended after the
  signal to write is `Uncertain`.

The operator posts one `Normal` `Event` for each attempt, when it
creates the attempt's first next pod: `ScanPodRecreated` on the
`FirmwareCatalog`, and `RehearsalPodRecreated` on the `Firmware`. It
posts none for the later pods of the same attempt, and the record's
message names the latest one.

## Uploads

Start from the `Firmware`. `kubectl get firmware` shows whether the
rule is valid, and how many of the devices it matched are loaded,
running, failed, or in conflict. `kubectl describe firmware <name>`
shows the entries in `status.devices` and the `Event`s of the last
hour.

### The entry is Failed

The entry's `message` says why. The operator does not retry a failed
upload on its own. It runs the upload again when the device appears
again, after a power cycle or a replug, or when you edit the
`Firmware`'s spec. To try again without a replug, change the spec, for
example by adding an argument that the loader accepts, or delete the
`Firmware` and apply it again.

| Message | Cause and fix |
|---|---|
| `the loader exited with code <n>: <line>` | The loader failed, and `<line>` is the last line of its log. Check the command, the path of the firmware file in the image, and the loader type, such as `-t fx2` or `-t fx3`. The upload pod runs as user 1000, so a loader that needs root fails here. |
| `the loader exited 0 and the device stayed in the slice for 1m0s after it` | The loader reported success, but the bare chip did not reset. Usually the firmware file is wrong for the chip, or the chip needs a second stage, such as `fxload -s` for some older QHY cameras. |
| `the upload pod did not finish within 5m0s of its creation` | The pod never ran, or it ran for five minutes. See [The upload pod stays Pending](#the-upload-pod-stays-pending). |
| `the upload pod failed: DeadlineExceeded: ...` | The loader ran longer than five minutes, and the kubelet stopped it. |
| `the upload pod no longer exists, ...` | Someone deleted the upload pod before the operator read its result. |

### The entry is Conflict

More than one `Firmware` matches the same device, so the operator runs
none of them on it. The message names the other rules. List the
selectors with:

```sh
kubectl get firmware -o custom-columns=NAME:.metadata.name,SELECTOR:.spec.selector
```

Narrow one selector, for example with the product ID, or delete the
duplicate. The rule that still matches then uploads to the device.

### Valid is False

The CEL selector does not compile, or `spec.machines` is not a valid
label selector, so the rule runs nowhere. The condition's message
gives the error:

```sh
kubectl get firmware <name> -o jsonpath='{.status.conditions[?(@.type=="Valid")].message}'
```

A selector reads attributes as `device.attributes["liken.sh"].<name>`.
When a device does not have an attribute that the selector reads, the
selector fails on that device, and the operator treats the device as
not matched. Guard such an attribute with `has()`, for example
`has(device.attributes["liken.sh"].serial)`.

### No entry appears

The rule matched no device. Look for the device in the machine's
`ResourceSlice`:

```sh
kubectl get resourceslice <node>-liken.sh -o yaml
```

- **The device is not in the slice.** The OS publishes a USB device
  only when a driver binds it or it has a usbfs node. `liken`'s
  [hardware modules guide](https://liken.sh/docs/guides/hardware-modules/)
  covers a device that the machine does not publish.
- **The IDs differ.** Compare the `vendor` and `product` attributes
  with the selector. Each is a string of four lowercase hexadecimal
  digits.
- **The device has no `busNumber` or `deviceNumber`.** The operator
  builds the usbfs path from these attributes, so it uploads only to a
  device that has both. An older `liken` release does not publish
  them, so upgrade the machine.
- **`spec.machines` leaves the machine out.** Compare the selector
  with the labels of the `Machine`:
  `kubectl get machine <node> --show-labels`.

### The upload pod stays Pending

The entry is `Running`, but the pod in `liken-system` does not start:

```sh
kubectl -n liken-system get pods -l firmware.liken.sh/upload=true
kubectl -n liken-system describe pod <pod>
```

- **The claim is not allocated.** The pod's `ResourceClaim` selects
  the device by its USB port path and its device number, which changes
  each time the device enumerates. So when the device left, or
  enumerated again, before the scheduler allocated the claim, the
  claim matches no device. The
  operator writes `Failed` five minutes after it created the pod, and
  the device's new appearance gets its own upload. Also check that the
  `DeviceClass` exists with `kubectl get deviceclass firmware-upload`.
  The operator's manifests install it.
- **The image does not pull.** The pod's `Event`s give the error.
  Check the tag in `spec.upload.image`. The `NetworkPolicy` that
  denies the pod's traffic does not block the pull, because the node's
  container runtime pulls the image, not the pod.

### A pod that claims the loaded device stays Pending

A pod that claims the camera by its loaded IDs, such as `0921`, stays
`Pending` until a device with those IDs is in the slice. That is
expected while the upload runs. When the upload is `Failed` or
`Conflict`, fix that first. When no `Firmware` covers the camera,
nothing loads its firmware, and the pod stays `Pending`.

## Scans

Start from the `FirmwareCatalog`. `kubectl get firmwarecatalog` shows
whether the operator's last read of the catalog's jcat succeeded, and
`kubectl describe firmwarecatalog <name>` shows its `Event`s of the
last hour. `status.machines` has one record for each machine:

```sh
kubectl get firmwarecatalog <name> -o jsonpath='{range .status.machines[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

### A FirmwareCatalog is not Ready

`Ready` is `False` when the operator could not read the jcat or the
catalog file that the jcat lists. No scan starts while `Ready` is
`False`, and `status.file` keeps the last file that the operator read
successfully. The condition's reason and message give the error:

```sh
kubectl get firmwarecatalog <name> -o jsonpath='{.status.conditions[?(@.type=="Ready")]}'
```

| Reason | Cause and fix |
|---|---|
| `JcatUnreadable` | The operator could not download the jcat at `spec.url` with `.jcat` added, the HTTP response was not `200`, or the jcat did not parse. Check `spec.url`, and check that the operator's pod can reach the host over HTTPS. The `firmware-scan` policy applies only to the scan pods. |
| `FileUnreadable` | The operator downloaded the jcat, but could not download or read the versioned catalog file that it names. The file must be in the same directory as `spec.url`, so a mirror must serve it there under the name the jcat gives. |
| `ChecksumMismatch` | The SHA-256 of the catalog file differs from the one in the jcat. A mirror that serves a stale jcat or a stale file causes this. Fix the mirror. |

The operator reads the jcat when it starts, when you edit the
`FirmwareCatalog`, and once an hour (the `--catalog-interval` flag).
To read it now, edit the `FirmwareCatalog` or restart the operator.

### A scan record is Failed

The record's `message` says why. The operator does not run a failed
scan again until the machine reboots, the set of device names on the
machine changes, the jcat lists a new file that offers other
releases for the machine's devices, or the operator runs another scan
image.

| Message | Cause and fix |
|---|---|
| `downloading the jcat: ...` or `downloading <file>: ...` | The scan pod could not reach the catalog's host. Check the `NetworkPolicy` `firmware-scan`, any policy of your own that denies egress from `liken-system`, and DNS. `firmware-scan` allows only TCP port 443, so a mirror on another port needs a policy of its own. |
| `the SHA-256 of <file> is <a>, and the operator expects <b>` | The scan pod downloaded a file whose SHA-256 differs from the one the operator read. A mirror that serves different bytes to different readers causes this. |
| `copying the firmware tables: ...` | The pod could not copy the machine's `/sys/firmware`, which it mounts read-only, into the copy that fwupd reads. The rest of the message gives the error. |
| `fwupdtool refresh: ...` | fwupd refused the catalog, for example because the signature of the jcat does not verify. The message ends with the last line of fwupd's error. A catalog that the LVFS did not sign needs its signer's certificate in `spec.trust`, as in [A catalog that the LVFS did not sign](https://liken.sh/firmware/docs/guides/find-updates/#a-catalog-that-the-lvfs-did-not-sign). |
| `installing the catalog's certificates from spec.trust: ...` | The pod could not copy a certificate from the `ConfigMap` that `spec.trust` names. The message gives the error. |
| `fwupdtool get-devices: ...` or `fwupdtool get-updates: ...` | fwupd could not read the devices or match the releases to them. The message ends with the last line of fwupd's error. |
| `the scan pod did not finish within 10m0s of its creation` | The pod never ran, or it ran for 10 minutes. Check for a pod that stays `Pending` or does not pull its image. |
| `the scan pod wrote no log: ...` | The pod ended with no log, for example with `DeadlineExceeded`. The message adds the pod's reason and, if the container ran, its exit code. |
| `the scan pod wrote no result, and its last log line is: ...` | The scan program crashed. The message gives the last line of its log. |
| `the scan pod no longer exists, ...` | Someone deleted the scan pod before the operator read its result. |

The operator deletes a scan pod after it records the result, so you
cannot read the log of a failed scan afterward. To see the log,
reboot the machine to run the scan again, and read the pod's log
while the scan runs:

```sh
kubectl -n liken-system get pods -l firmware.liken.sh/scan=true
kubectl -n liken-system logs <pod>
```

### A scan record is CatalogMoved

The LVFS published a new catalog between the operator's read of the
jcat and the scan pod's read, so the scan pod read a jcat that lists a
different file than the one the operator gave it. The record's message
gives the new file name. This needs no action. The operator reads the
jcat again immediately and scans the machine against the new file.

When the record stays `CatalogMoved`, the jcat that the operator reads
still lists the old file. The operator scans the machine again when
that jcat lists a new file.

### A machine is never scanned

The machine has no record in `status.machines`. Check these in order:

- **The machine is not Ready.** The operator scans only a `Machine`
  whose `status.phase` is `Ready`. Check with
  `kubectl get machine <node>`.
- **The FirmwareCatalog is not Ready.** No scan starts against it. See
  [A FirmwareCatalog is not Ready](#a-firmwarecatalog-is-not-ready).
- **The API server refuses the scan pod.** The scan pod is privileged,
  so the API server refuses it when `liken-system` has a Pod Security
  label below `privileged`. The operator's log gives the error:
  `kubectl -n liken-system logs deployment/firmware-operator`.
- **The scan pod cannot reach the catalog.** The record is `Running`
  until the scan fails with a `downloading` message, up to 10 minutes
  after the pod started. See
  [A scan record is Failed](#a-scan-record-is-failed).
- **The operator is not the leader.** Only the copy that holds the
  `Lease` scans. See [Confirm that it leads](https://liken.sh/firmware/docs/guides/install/#confirm-that-it-leads).

### A discovered Firmware keeps an entry for a machine that is off

This is expected. A scan replaces only the entries of its own machine.
So the entries of a machine that is off, or that cannot be scanned,
keep the version and the releases from its last scan, and each
entry's `time` says when that scan ran. The operator removes an entry
when the machine's next scan does not report the device, or when you
delete the `Machine`.

The operator deletes a discovered `Firmware` when no entry is left.
An approval alone does not keep the object, because with no entry, no
machine is offered the approved release. The object stays when someone
changed its group, or when an apply on it is waiting for a turn or
writing.
The operator posts a `Deleted` `Event` for each object it deletes,
which `kubectl get events -n default --field-selector involvedObject.name=<name>`
shows for an hour. You delete an object whose group you changed with
`kubectl delete firmware <name>`, and the operator creates it again at
the next scan that reports a device of its group.

## Approvals

`kubectl get firmware` shows the `Approved` condition in the
`APPROVED` column. The condition's message names the machines, so read
it first:

```sh
kubectl get firmware <name> -o jsonpath='{.status.conditions[?(@.type=="Approved")].message}'
```

### The API server refuses the approval

The CRD validates an approval before the operator reads it:

- `an approval needs both version and cabinet`: add the one that is
  missing.
- `only an update of a component can set version`: the `Firmware`
  updates a set of GUIDs, such as `uefi-ca-7420e8dc`. No catalog
  offers a release for a set of GUIDs, so there is nothing to approve.
- `machines limits an approval`: `spec.machines` needs
  `spec.update.version`.
- A `sha256` that is not 64 lowercase hexadecimal digits, or a `url`
  that does not start with `http://` or `https://`, fails the schema.

### Approved is False with CabinetDiffers

A catalog offers the version you approved, but in a cabinet whose
SHA-256 is not the one you pinned. Compare
`spec.update.cabinet.sha256` with the release's `cabinet.sha256` in
`status.machines`. If you copied it wrong, fix it. If you copied it
correctly, the catalog now serves a different file under the same
version. Read the release again, and decide whether to approve the new
file.

### Approved is False with NotOffered

No catalog offers the version to any machine that the approval covers.
Check each of these:

- **The version.** It must match a release's `version` in
  `status.machines` exactly, as a string.
- **`spec.machines`.** It must select a `Machine` that has an entry in
  `status.machines`. List the labels with
  `kubectl get machine --show-labels`.
- **The catalog withdrew the release.** The LVFS can remove a release.
  The next scan writes entries without it, and the approval changes to
  `False`. Choose another release.

### Approved is False with MachinesInvalid

`spec.machines` is not a valid label selector, for example because a
label key has a space in it. The message gives the error. Fix the
selector.

## Rehearsals

`kubectl get firmware` shows the `Rehearsed` condition in the
`REHEARSED` column. The condition's message gives each machine with
its rehearsal's message, and `status.rehearsals` has one record for
each machine:

```sh
kubectl get firmware <name> -o jsonpath='{range .status.rehearsals[*]}{.machine}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
```

The operator runs a rehearsal once for each combination of the
machine's boot, the approval, and the catalog file. So a failed
rehearsal runs again only after the machine reboots, you change the
approval, or the machine's next scan reads a new catalog file.

### Rehearsed is False with Refused

The install would write something that the operator does not apply:

| Message | What it means |
|---|---|
| `fwupd's ... plugin updates ..., and a rehearsal covers only the Secure Boot databases and UEFI capsules` | fwupd writes this device directly, as it does an SSD or a dock, so a copy cannot rehearse the install. |
| `the machine has no ESP for its next boot, ...` | `liken` has not recorded a proven boot slot on this machine, so it keeps no link to the ESP that the next boot reads, and a capsule has nowhere to be staged. A machine has a proven slot after its first upgrade. |
| `the install created ..., which boots ..., not fwupd's updater` | The capsule's install created a boot entry for another program. |
| `the install created ... boot entries, and a capsule needs one` | The install created boot entries besides the updater's. |
| `the install set BootNext to ..., not to the updater's entry` | The install pointed the next boot at another entry. |
| `the install changed BootOrder by more than adding the updater's entry at its end` | The install reordered the machine's boot entries. |
| `the install set OsIndications to ..., which requests more than capsule-on-disk` | The install requested more from the firmware than the capsule. |
| `the install wrote ... on the ESP, where a capsule writes nothing` | The install wrote a file other than fwupd's updater and the capsule, such as a shim or a loader. |
| `the install changed ..., which a capsule does not write` | The install changed a variable other than the boot entries, `BootNext`, `BootOrder`, `OsIndications`, and fwupd's own variables. |
| `the capsule's files need ... bytes on the ESP, and it has ... free` | The ESP that the next boot reads has too little free space for the updater and the capsule. |
| `the install changed ..., which is not ...`, `the install created ...`, `the install removed ...` | The install writes a variable other than its device's own. |
| `the install wrote ... under the ESP path` | The install puts a file on the ESP. |
| `the install rewrote ... instead of appending to it` | The install replaces the list instead of appending to it. |
| `the append to ... has the attributes ...` | The write is not an authenticated append, so the firmware would check no signature. |
| `the append to ... is not a sequence of authenticated writes: ...` | The appended bytes do not split into authenticated writes, each made of a header, a PKCS7 certificate, and signature lists. The apply writes each one in its own call, so it cannot write bytes that it cannot split. The rest of the message gives the first byte that is not part of a write. |
| `the install did not change ...` | The device already has the release's entries. |
| `the append to ... needs ... bytes, and the variable store has ... free` | The machine's variable store has too little free space for the write. |

### Rehearsed is False with Failed

The rehearsal could not run:

| Message | What to check |
|---|---|
| `the SHA-256 of the cabinet is ..., and the approval pins ...` | The cabinet at `spec.update.cabinet.url` is not the file you pinned. Check the URL and the SHA-256. |
| `downloading the cabinet: ...` | The pod could not download the cabinet. The `firmware-rehearsal` policy in `deploy/rehearsal.yaml` allows egress only to DNS and to TCP port 443, so a mirror on another port needs a policy of its own. |
| `fwupd reports no device ...` | fwupd in the rehearsal pod does not find the device that the scan reported. |
| `copying the firmware tables: the machine has no UEFI variable store` | The machine booted in BIOS mode. |
| `fwupdtool install: ...` | fwupd refused the install. The rest of the line is fwupd's reason. |
| `the rehearsal pod did not finish within 10m0s of its creation` | The pod never ran, for example because its image does not pull, or it hung. Check `kubectl -n liken-system get events`. |
| `the rehearsal pod no longer exists, ...` | Someone deleted the pod before it ended. |

### Rehearsed stays Unknown

A pending machine waits for its rehearsal. Check each of these:

- **The machine is not `Ready`.** A rehearsal starts only on a `Ready`
  machine.
- **The machine's last scan read an older catalog file.** The
  rehearsal loads the catalog file that the scan read, so it waits for
  a scan of the current file.
- **The rehearsal found that the catalog moved.** The record's result
  is `CatalogMoved`, and its message starts with `the jcat now lists`
  and the new file. The LVFS published a new catalog after the
  machine's scan. This needs no action, and the operator posts a
  `Normal` `RehearsalCatalogMoved` `Event`. The operator reads the
  jcat again immediately, and the machine rehearses again against the
  new file, after a new scan when the file offers the machine other
  releases.
- **The kubelet ended the rehearsal pod.** The record's message starts
  with `the kubelet ended the rehearsal pod`, and the operator
  rehearses again in a next pod. See
  [A pod that the kubelet ended](#a-pod-that-the-kubelet-ended).
- **The API server refuses the rehearsal pod.** The pod mounts a
  `hostPath` volume, which the `baseline` Pod Security level forbids.
  So a cluster that labels `liken-system` with a level below
  `privileged` refuses it. Look for the refusal in the operator's log.

## Applies

`kubectl get firmware` shows the `Applied` condition in the `APPLIED`
column. The condition's message gives each machine with its record's
message, and `status.applies` has one record for each machine:

```sh
kubectl get firmware <name> -o jsonpath='{range .status.applies[*]}{.machine}{"\t"}{.result}{"\t"}{.disruption}{"\t"}{.message}{"\n"}{end}'
```

After a record reads `Refused` or `Failed`, the operator does not
write again on its own. To try again, withdraw the approval and
approve it again. The withdrawal removes the machine's rehearsal and
its apply record, so the machine rehearses again, and requests a turn
again when the rehearsal passes.

### Applied is False with Refused

The message is `the firmware refused the append to <variable>:
permission denied`. The kernel reports the firmware's refusal of an
authenticated write as permission denied. The firmware refuses an
append whose signer does not chain to `KEK`, or to `PK` for a `KEK`
update. This happens when the machine's `KEK` has only an older
Microsoft key and a newer one signed the release. A refused append changes nothing,
and the operator deletes the `Disruption`, so the machine leaves its
turn without a reboot. Choose the release of a component whose signer
the machine's `KEK` has, such as `microsoft-dbx-x64` instead of
`microsoft-dbx-x64-kek2023`.

A capsule's record is also `Refused`, with
`the machine's status.firmware.bootOrderKept is not true, ...`, when
the machine's firmware did not keep `BootOrder` through its last
reset, or when the machine has not proven a release yet. The operator
requests no turn. Read the machine's `status.firmware.bootOrderKept`,
and see `liken`'s guide
[A reboot did not keep a component's BootNext](https://liken.sh/docs/guides/troubleshoot/#a-reboot-did-not-keep-a-components-bootnext).

### Applied is False with Failed

| Message | What to check |
|---|---|
| `the Disruption ... is Complete, so the machine left its reboot turn before machine-operator held it ...` | The machine rebooted without holding its turn, so nothing was written. A `machine-operator` from a `liken` release whose `Disruption` has no `spec.hold` does this. Upgrade the cluster, then approve again. |
| `the Disruption ... no longer exists, so the machine left its reboot turn before machine-operator held it ...` | Someone deleted the `Disruption` before `machine-operator` held it. Approve again. |
| `the append's SHA-256 is now ..., and the rehearsal recorded ...` | Something else changed the machine's variables after the rehearsal, such as another operating system or the firmware's setup menu. Nothing was written. Approve again to rehearse against the variables as they are now. |
| `the rehearsal before the write did not pass, so nothing was written: ...` | The rehearsal that the apply pod runs before it writes failed or was refused. The rest of the message is the rehearsal's, and the [rehearsal tables](#rehearsals) explain it. |
| `the apply pod ended before the signal to write, so nothing was written: ...` | The apply pod stopped while it prepared or waited for the machine's turn, for example because the machine rebooted or the kernel killed the pod for memory. The rest of the message gives its last line or the pod's reason. The pod writes only after the signal, so the operator deletes the `Disruption`, if it created one, and the machine leaves its turn without a reboot. Approve again. |
| `the apply pod no longer exists and did not finish preparing, so nothing was written and the operator created no Disruption` | Someone deleted the apply pod, or a drain for another turn evicted it, before it was `Ready`. Approve again. |
| `marking the prepare as done: ..., so nothing was written` | The apply pod could not create its marker file in `/run/apply`, so its readiness probe could never pass. The rest of the message gives the error. |
| `the variable ... changed while the apply waited for its turn, so nothing was written` | Something else changed the variable after the apply pod prepared and before the signal, such as another operating system or the firmware's setup menu. Approve again to rehearse against the variable as it is now. |
| `waiting for the signal to write: ..., so nothing was written` | The apply pod could not read its annotations. The rest of the message gives the error. |
| `clearing the immutable flag of ...: operation not permitted` | The pod lacks `CAP_LINUX_IMMUTABLE`. A policy of your cluster that removes capabilities from pods in `liken-system` causes this. |
| `writing the append to ...: read-only file system` | The machine's `efivarfs` is mounted read-only, so the pod cannot write it through its read-write mount of `/sys/firmware`. |
| `writing the append to ...: ...` | The kernel refused the write for another reason, which the message gives. |
| `fwupdtool install: ..., and it wrote nothing` | fwupd refused a capsule's install on the machine and changed nothing. The rest of the line is fwupd's reason. |
| `the FirmwareCatalog ... has no current file ...` | The `FirmwareCatalog` that the machine's scan used was deleted, or it never read its jcat. See [A FirmwareCatalog is not Ready](#a-firmwarecatalog-is-not-ready). |
| `the machine rebooted after the write, and its next scan still reads version ..., not ...` | The firmware accepted the write but did not keep it across the reboot. A firmware that resets Secure Boot to its defaults discards the appended entries. Check the firmware's Secure Boot settings before you approve again. |
| `the machine rebooted, and its next scan still reads version ..., not ..., so the append was not written` | An uncertain apply, described below, did not write. Approve again. |

A failed or refused record makes the operator delete its
`Disruption`, unless the operator already removed `spec.hold`. In that
case the turn still reboots the machine.

### Applied is Unknown with Uncertain

The apply pod ended after the signal to write without reporting
whether it wrote, or the firmware accepted only some of the writes of
the append. The write may have reached the firmware, so the operator
removes the hold, the machine reboots in its turn, and the next scan
reads the version. The record then ends, or changes to `Failed` when
the machine still reads the old version.

| Message | What to check |
|---|---|
| `the apply pod no longer exists, ...` | Someone deleted the apply pod before it ended. |
| `the apply pod did not finish within 10m0s of the signal to write` | The pod hung after the signal, or a pod that the operator started again during the hold never ran, for example because its image does not pull. Check `kubectl -n liken-system get events`. |
| `the apply pod wrote no result, and its last log line is: ...` or `the apply pod wrote no log: ...` | The apply program crashed. The message gives its last line, or the pod's reason. |
| `the apply pod reported the result ..., which the operator does not recognize` | The scan image and the operator are from different releases. Pin both to one tag. |
| `fwupd's writes differ from the rehearsal's: ...` | fwupd wrote different bytes to the machine than to the rehearsal's copies, for example because a capsule file changed. The reboot boots the proven slot, so the capsule does not run. Approve again to rehearse against the machine as it is now. |
| `the install ..., so the reboot keeps liken's BootNext and the capsule does not run` | fwupd wrote something on the machine that a capsule does not write. The message names it, as in the [rehearsal tables](#rehearsals). |
| `fwupdtool install: ..., after it wrote to the machine, so ...` | fwupd stopped with an error after it wrote. The reboot boots the proven slot. |
| `the variable ... changed, so the machine must reboot: the firmware accepted ... of the ... authenticated writes, and write ... returned: ...` | The firmware accepted the first writes of the append and refused or failed the one that the message gives. `permission denied` means a signature that does not chain to `KEK`, as in [Applied is False with Refused](#applied-is-false-with-refused). The next scan reads which entries the variable has. |

### Applied stays Unknown

Find the machine's record, and its `Disruption`:

```sh
kubectl get disruptions -l firmware.liken.sh/firmware=<name>
```

- **The machine has no record.** Its rehearsal has not passed on its
  current boot. See [Rehearsals](#rehearsals). The operator also
  requests no turn on a `liken` release that has no `Disruption` kind.
  An apply pod that found the catalog moved leaves no record either.
  It wrote nothing and requested no turn. The operator posts a
  `Normal` `ApplyCatalogMoved` `Event`, reads the jcat again
  immediately, and starts the apply again with the new file.
- **The record is `Waiting` with no `disruption`.** The apply pod is
  preparing on the machine, and the operator requests the turn when
  the pod is `Ready`. List the pod with
  `kubectl -n liken-system get pods -l firmware.liken.sh/apply=true`.
  A pod that has prepared logs a line whose `phase` is `prepared`.
  A pod that does not finish the prepare in 10 minutes ends `Failed`. The pod mounts a `hostPath`
  volume, which the `baseline` Pod Security level forbids, so a
  cluster that labels `liken-system` with a level below `privileged`
  refuses it. Look for the refusal in the operator's log.
- **The record is `Waiting` with a `disruption`.** The pod has
  prepared, and the machine waits for its turn. An `APPROVED` of
  `False` on the `Disruption` means the machine's `rebootPolicy` is
  `Manual`, and the turn waits for you to approve it.
  [Apply the update](https://liken.sh/firmware/docs/guides/approve-an-update/#apply-the-update) gives the
  command. A `GRANTED` of `False` means a budget has no room, and the
  `Granted` condition's message says what the turn waits for. When the
  turn is granted, `machine-operator` drains the machine and sets
  `HELD` to `True`.
- **The record is `Writing`.** The operator sent the apply pod the
  signal, and the pod writes on the drained machine. The pod's
  annotation `firmware.liken.sh/write-at` gives the time of the
  signal, and the operator waits 10 minutes from that time for the
  pod's result.
- **The record is `Written` or `Uncertain`.** The machine reboots, and
  its next scan ends the record. A scan waits until the machine is
  `Ready`.

### A machine stays drained

A machine whose `Disruption` shows `HELD` `True` is drained and waits
for the operator, and a hold has no deadline. The operator removes the
hold after a write, and deletes the `Disruption` after a refusal or a
failure. So a machine stays drained when the operator is not running
or is not the leader. Check the operator first: see
[Confirm that it leads](https://liken.sh/firmware/docs/guides/install/#confirm-that-it-leads). The
operator's pod tolerates the cordon's taint
`node.kubernetes.io/unschedulable`, so the `liken` drain does not
evict it, and the scheduler can place it on the drained machine.

To release the machine yourself, remove the hold or delete the
`Disruption`. Removing the hold reboots the machine:

```sh
kubectl patch disruption <machine>.firmware-<name> --type merge -p '{"spec":{"hold":null}}'
```

Deleting the `Disruption` ends the turn without a reboot:

```sh
kubectl delete disruption <machine>.firmware-<name>
```

If the record shows `Writing` and the apply pod is still running, wait
for the pod to end first, so that the record gets its result. If you
deleted the `Disruption` and the pod then writes, or ends without
reporting whether it wrote, the operator creates the `Disruption`
again with no hold. The write takes effect only at a reboot, so the
machine takes its turn again, within its budgets, and reboots.
