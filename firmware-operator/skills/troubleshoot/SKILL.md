---
name: troubleshoot
description: "Find why a Firmware's upload failed or never started: a Failed entry and its message, a Conflict between two rules, a selector that does not compile or matches nothing, an upload pod that stays Pending, and a claimed device that stays Pending because its firmware is not loaded. Find why discovery found nothing: a FirmwareCatalog that is not Ready, a scan record that is Failed or CatalogMoved, a machine that is never scanned, and a discovered Firmware that keeps the entry of a machine that is off. Use when kubectl get firmware shows Failed or Conflict, when a bare chip gets no upload, when a pod that claims a camera does not start, when kubectl get firmwarecatalog shows Ready False, or when a machine has no scan record."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/troubleshoot/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Troubleshoot uploads and scans

The first part of this guide covers uploads, and the second part covers
the scans that find updates ([Find firmware updates](https://liken.sh/firmware/docs/guides/find-updates/)).

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
  scans. See [Confirm that it leads](https://liken.sh/firmware/docs/guides/install/#confirm-that-it-leads).

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
