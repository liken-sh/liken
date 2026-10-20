---
title: How uploads work
weight: 10
---

# How uploads work

A device that needs firmware from the host appears on a `liken`
machine as a bare chip, with its own vendor and product ID. A
`Firmware` names that chip with a CEL selector, and names the image
and the command that write the firmware into it. `firmware-operator`
runs that command once for each appearance of a matching device, on
the device's machine, and records the result. The chip then resets
and enumerates again as the real device, with a new product ID, and
the bare chip leaves the machine's `ResourceSlice`.

## When an upload starts

`machine-operator` publishes each machine's devices in one
`ResourceSlice`. `firmware-operator` watches those slices, so a bare
chip that appears wakes it at once, with no polling. For each device
that a `Firmware` matches, it creates two objects in its own
namespace, `liken-system`:

* a pod with a node selector for the device's machine. It runs the
  `Firmware`'s image and command as user 1000, with no capabilities,
  no service account token, and a read-only root file system. A
  `NetworkPolicy` in the manifests denies all its traffic.
* a `ResourceClaim` of the `DeviceClass` `firmware-upload`, which
  selects the one device by its USB port path, `address`, and its
  `deviceNumber`. The claim gives the pod the device's usbfs node, and
  the operator sets `LIKEN_DEVICE` to the node's path, such as
  `/dev/bus/usb/001/012`. Kubernetes expands `$(LIKEN_DEVICE)` in the
  command.

The pod owns the claim, and the `Firmware` owns the pod, so a deleted
`Firmware` deletes its uploads.

`spec.machines` limits a `Firmware` to the `Machine`s whose labels it
selects. With no `spec.machines`, the rule applies on every machine,
so a portable camera loads on whichever machine you connect it to.

## How the operator decides the result

| What happens | Result |
|---|---|
| The loader exits 0, and the bare device leaves the slice | `Loaded` |
| The loader exits 0, and the bare device is still in the slice one minute later | `Failed` |
| The loader exits with another code | `Failed`, with the exit code and the last line of the loader's log |
| The pod has not finished five minutes after the operator created it | `Failed` |
| The pod is deleted before the operator reads its result | `Failed` |
| Another `Firmware` matches the same device | `Conflict`, and no pod runs |

The exit code alone is not enough, because a loader can exit 0 after
it writes firmware that the chip does not run. A chip that took its
firmware resets and leaves the slice: a QHY5L-II's FX2 chip left
14 seconds after `fxload` reset it. The device that comes back is not
part of the check, because the `Firmware` does not name its IDs.

The operator writes the result into the `Firmware`'s status first,
and then deletes the pod and its claim. An operator that restarts
between the two reads the same pod again and writes the same result.

## One attempt for each appearance

The kernel gives a USB device a new device number each time it
enumerates. So the machine, the port path, and the device number
together identify one appearance of a device. Each appearance gets
one upload for each generation of the `Firmware`. A failure goes to
the status and to an `Event`, and nothing retries it on a timer,
because a loop that writes to a device again and again can disturb
it. Three things start a new attempt:

* a power cycle or a replug, which gives the device a new device
  number;
* an edit to the `Firmware`'s spec, which moves its generation;
* the end of a `Conflict`, when the other `Firmware` is deleted or no
  longer matches.

The operator keeps no record of its own. The status entry and the
pod's annotations are the record, so a restarted operator starts no
second upload for an appearance.

## Two rules on one device

The API server checks each `Firmware` alone, so it cannot refuse a
second rule that matches the same device as the first. The operator
runs neither rule on that device and writes `Conflict` into the status
of both, with the names of the other rules. Fix the selectors so that
one rule matches the device.

## What the status holds

`status.devices` has one entry for each port, a machine and a port
path, where the rule matched a device. A camera that is power-cycled
every night keeps one entry, because each new appearance replaces the
entry for its port. A `Loaded` entry stays after the bare device
leaves, so it shows which rule loaded the device that is there now.
`status.counts` counts the entries in each result, and
`kubectl get firmware` shows the counts as columns.

The `Valid` condition says whether the selector and `spec.machines`
compile. A `Firmware` that is not valid runs nowhere.

## The operator and the OS

The OS publishes each machine's devices, with the USB bus number and
device number that an upload needs. It holds no vendor firmware and
runs no upload. A cluster without `firmware-operator` loses no
function of the OS: its bare chips stay bare, and a pod that claims
the loaded device stays `Pending`.
