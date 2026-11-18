---
title: How uploads work
weight: 10
---

# How uploads work

A device that needs firmware from the host appears on a `liken`
machine as a bare chip, with its own vendor ID and product ID. A
`Firmware` selects that chip with a CEL selector and gives the image
and the command that write the firmware into it. `firmware-operator`
runs that command once each time a matching device appears, on the
device's machine, and records the result. After a good upload the chip
resets and enumerates again as the real device, with a new product ID,
and the bare chip leaves the machine's `ResourceSlice`.

## When an upload starts

`machine-operator` publishes each machine's devices in one
`ResourceSlice`. `firmware-operator` watches those slices, so it starts
an upload as soon as a bare chip appears. For each device that a
`Firmware` matches, it creates two objects in its own namespace,
`liken-system`:

* A pod with a node selector for the device's machine. It runs the
  `Firmware`'s image and command as user ID 1000, with no capabilities,
  no service account token, and a read-only root file system. A
  `NetworkPolicy` in the manifests denies all its traffic.
* A `ResourceClaim` that requests one device of the `DeviceClass`
  `firmware-upload`. Its selector matches the device's USB port path,
  `address`, and its `deviceNumber`. The claim gives the pod the
  device's usbfs node, and the operator sets `LIKEN_DEVICE` to the
  node's path, such as `/dev/bus/usb/001/012`. Kubernetes expands
  `$(LIKEN_DEVICE)` in the command.

The pod owns the claim, and the `Firmware` owns the pod, so deleting a
`Firmware` deletes its uploads.

`spec.machines` is a label selector on `Machine` objects, and limits a
`Firmware` to the machines it matches. Without `spec.machines`, the
rule applies on every machine, so a portable camera loads on whichever
machine you connect it to.

## How the operator decides the result

| What happens | Result |
|---|---|
| The pod has not finished yet | `Running` |
| The loader exits 0, and the bare device leaves the slice | `Loaded` |
| The loader exits 0, and the bare device is still in the slice one minute later | `Failed` |
| The loader exits with another code | `Failed`, with the exit code and the last line of the loader's log |
| The pod has not finished five minutes after the operator created it | `Failed` |
| The pod is deleted before the operator reads its result | `Failed` |
| Another `Firmware` matches the same device | `Conflict`, and no pod runs |

A loader can exit 0 after it writes firmware that the chip does not
run, so the exit code alone does not prove the upload. A chip that
accepted its firmware resets and leaves the slice, so the operator
waits up to one minute after the loader exits for the bare device to
disappear. It does not check the device that comes back, because the
`Firmware` does not give that device's IDs.

The operator writes the result into the `Firmware`'s status before it
deletes the pod and its claim. If the operator restarts between the
two, it reads the same pod again and writes the same result.

## One attempt for each appearance

The kernel gives a USB device a new device number each time it
enumerates. So the machine, the port path, and the device number
together identify one appearance of a device. The operator runs one
upload for each appearance and each `metadata.generation` of the
`Firmware`. A failure goes to the status and to an `Event`, and
nothing retries it on a timer, because writing to a device again and
again can disturb it. A new attempt starts after:

* a power cycle or a replug, which gives the device a new device
  number;
* an edit to the `Firmware`'s spec, which increments its generation;
* the end of a `Conflict`, when the other `Firmware` is deleted or no
  longer matches.

The operator reads the status entry and the pod's annotations to find
earlier attempts, so a restarted operator does not start a second
upload for the same appearance.

## A device that more than one rule matches

The API server validates each `Firmware` on its own, so it cannot
refuse a rule that matches the same device as another rule. The
operator runs no rule on that device and writes `Conflict` into the
status of each rule, with the names of the others. Fix the selectors
so that only one rule matches the device.

## What the status holds

`status.devices` has one entry for each port, meaning a machine and a
port path, where the rule matched a device. A camera that is
power-cycled every night keeps one entry, because each new appearance
replaces the entry for its port. A `Loaded` entry stays after the bare
device leaves, so it shows which rule loaded the device that is there
now. `status.counts` counts the entries with each result, and
`kubectl get firmware` shows the counts as columns.

The `Valid` condition says whether the selector and `spec.machines`
compile. A `Firmware` that is not valid runs nowhere.

## The operator and the OS

The OS publishes each machine's devices, with the USB bus number and
device number that an upload needs. The OS contains no vendor firmware
and runs no upload. On a cluster without `firmware-operator`, the OS
still works, but its bare chips stay bare, and a pod that claims the
loaded device stays `Pending` because that device never appears.
