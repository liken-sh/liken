---
title: Upload firmware to a device
weight: 20
description: "Find out whether a USB device needs firmware from the host, write a Firmware with spec.upload for it, and read status.devices and the Events to see whether it loaded. Covers the QHY5L-II guide camera on an FX2 chip, translating a vendor's udev rule, spec.machines, and what a Conflict means. Use when a camera, logic analyzer, or other USB device appears as a bare chip and no driver opens it."
---

# Upload firmware to a device

At the end of this guide, a USB device that needs firmware from the
host loads it each time it appears on any machine of your cluster.
You need `firmware-operator` running (see the
[install guide](../install/)), the device connected to a `liken`
machine, and a container image that holds the device's loader and its
firmware file. The example is the QHY5L-II guide camera, which is
built on a Cypress FX2 chip.

## Find out whether the device needs firmware

A device that needs firmware from the host has one product ID before
its firmware loads and another after. To list the USB devices that a
machine publishes, read the machine's `ResourceSlice`:

```sh
kubectl get resourceslice <node>-liken.sh -o yaml
```

A QHY5L-II with no firmware is the vendor `1618` and the product
`0920`:

```yaml
- name: usb-1-4-1-2
  attributes:
    address: {string: 1-4.1.2}
    bus: {string: usb}
    busNumber: {int: 1}
    deviceNumber: {int: 12}
    product: {string: "0920"}
    vendor: {string: "1618"}
```

After the firmware loads, the same port has the product `0921` and a
new device number. The vendor's Linux package shows which IDs are the
bare chip, because its udev rules run a loader, often `fxload`, for
those IDs. INDI's `85-qhyccd.rules` has this rule for the QHY5L-II:

```
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /lib/firmware/qhy/QHY5II.HEX -D $env{DEVNAME}"
```

## Write the Firmware

A `Firmware` has the same parts as the udev rule: the IDs to match,
the loader, and the firmware file.

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: qhy5l-ii
spec:
  selector: |
    device.attributes["liken.sh"].vendor == "1618" &&
    device.attributes["liken.sh"].product == "0920"
  upload:
    image: ghcr.io/liken-sh/indi-qhy:<tag>
    command: [fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]
```

- `spec.selector` is a CEL expression that uses the same variables as a
  `DeviceClass` selector. Write it against the attributes in the
  `ResourceSlice`, as `device.attributes["liken.sh"].<name>`.
- `spec.upload.image` is an image that contains the loader and the
  firmware file. The firmware comes from the vendor, so it goes in this
  image and not in the OS image.
- `spec.upload.command` is the loader's command. In place of the udev
  rule's `$env{DEVNAME}`, write `$(LIKEN_DEVICE)`. The operator sets
  the environment variable `LIKEN_DEVICE` to the path of the device's
  usbfs node, such as `/dev/bus/usb/001/012`, and the kubelet expands
  `$(LIKEN_DEVICE)` in the command.

The upload pod runs the command as user 1000, with no capabilities and
a read-only root file system. So the loader must work without root,
and user 1000 must be able to read the firmware file. An FX2 or FX3
load is a series of USB control transfers on the usbfs node, and the
pod's device claim gives the pod access to that node.

Apply it:

```sh
kubectl apply -f qhy5l-ii.yaml
```

### Limit it to some machines

When `spec.machines` is not set, the rule applies on every machine, so
a portable camera loads its firmware wherever you connect it. To limit
the rule, select `Machine` objects by their labels:

```yaml
spec:
  machines:
    matchLabels:
      observatory: "true"
```

A machine that has no `Machine` object has no labels, so a rule that
sets `spec.machines` does not apply to it.

## Read what it did

Connect the camera or power it on. Within a few seconds the operator
starts the upload. `kubectl get firmware` counts the results on the
ports where the rule matched a device:

```
NAME       VALID   LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION   APPROVED   PENDING   REHEARSED   APPLIED   AGE
qhy5l-ii   True    1        0         0        0                                                                                         5m
```

The columns from `MACHINES` to `APPLIED` are for the `Firmware`
objects that record and approve updates, so they are empty for an
upload rule.

`status.devices` has one entry for each port:

```sh
kubectl get firmware qhy5l-ii -o jsonpath='{.status.devices}'
```

```yaml
- machine: liken-1
  device: usb-1-4-1-2
  address: 1-4.1.2
  deviceNumber: 12
  generation: 1
  result: Loaded
  message: the loader exited 0 and the bare device left the slice
  time: "2026-10-19T22:01:09Z"
```

The entry has the device number of the bare chip that the operator
loaded. After the load, the camera is a new device in the
`ResourceSlice`, with the product `0921` and its own device number,
and the pod that uses the camera claims that device. While the upload
runs, the `result` is `Running`, and the `message` gives the name of
the upload pod in `liken-system`.

The `Event`s from the last hour show the same history:

```sh
kubectl describe firmware qhy5l-ii
```

```
Events:
  Type    Reason         Age   From               Message
  ----    ------         ----  ----               -------
  Normal  Compiled       5m    firmware-operator  The selectors compile
  Normal  UploadStarted  2m    firmware-operator  usb-1-4-1-2 on liken-1: the upload pod liken-system/qhy5l-ii-3f2a9c1b0d4e runs the loader
  Normal  Loaded         2m    firmware-operator  Loaded usb-1-4-1-2 on liken-1: the loader exited 0 and the bare device left the slice
```

The device keeps the firmware until it loses power. After a power
cycle or a replug, the bare chip appears with a new device number, and
the operator uploads again and replaces the port's entry.

## A Conflict

When two `Firmware` objects match one device, the operator runs
neither. The entry for that port in each object has the `result`
`Conflict`, and its `message` lists the other rules. The operator posts
a `Warning` `Event` with the reason `Conflict` on each object. Narrow
one selector or delete one of the rules, and the rule that still
matches uploads to the connected device.

## Change the rule

The operator does not retry a failed upload on its own. An edit to the
spec changes the `Firmware`'s generation, and the operator then
uploads again with the new spec to each matched device in a
`ResourceSlice`. So to try a fix after a failed upload, edit the
rule.
