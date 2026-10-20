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

A device that needs firmware from the host appears with one product
ID before its firmware and another after it. List the USB devices that
the machine publishes:

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

After its firmware, the same port holds the product `0921`, with a new
device number. The vendor's Linux package tells you which IDs are the
bare chip: its udev rules run a loader, often `fxload`, for the bare
IDs. INDI's `85-qhyccd.rules` holds this rule for the QHY5L-II:

```
ATTRS{idVendor}=="1618", ATTRS{idProduct}=="0920", RUN+="/sbin/fxload -t fx2 -I /lib/firmware/qhy/QHY5II.HEX -D $env{DEVNAME}"
```

## Write the Firmware

A `Firmware` holds the same three facts as the udev rule: the IDs
that match, the loader, and the firmware file.

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

- `spec.selector` is a CEL expression with the same variables as a
  `DeviceClass` selector. Write it against the attributes in the
  slice, as `device.attributes["liken.sh"].<name>`.
- `spec.upload.image` is an image that holds the loader and the
  firmware file. The firmware comes from the vendor and stays in that
  image, never in the OS image.
- `spec.upload.command` is the loader's command. In place of the udev
  rule's `$env{DEVNAME}`, write `$(LIKEN_DEVICE)`. The operator sets
  `LIKEN_DEVICE` to the device's usbfs node, such as
  `/dev/bus/usb/001/012`, and Kubernetes expands it.

The upload pod runs the command as user 1000, with no capabilities and
a read-only root file system, so the loader must run as a user that
is not root, and the firmware file must be readable by that user. An
FX2 or FX3 load is a series of USB control transfers on the usbfs
node, and the claim gives the pod that node.

Apply it:

```sh
kubectl apply -f qhy5l-ii.yaml
```

### Limit it to some machines

With no `spec.machines`, the rule applies on every machine, so a
portable camera loads wherever you connect it. To limit it, select
the `Machine`s by their labels:

```yaml
spec:
  machines:
    matchLabels:
      observatory: "true"
```

A machine with no `Machine` object has no labels, so a rule with
`spec.machines` does not apply there.

## Read what it did

Connect the camera, or power it on. Within a few seconds the operator
starts the upload. `kubectl get firmware` counts the result of each
port where the rule matched a device:

```
NAME       VALID   LOADED   RUNNING   FAILED   CONFLICT   MACHINES   BEHIND   VERSION   APPROVED   PENDING   AGE
qhy5l-ii   True    1        0         0        0                                                                   5m
```

`MACHINES`, `BEHIND`, `VERSION`, `APPROVED`, and `PENDING` belong to
the `Firmware` objects that find and approve updates, so an upload
rule leaves them empty.

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

The entry keeps the device number of the bare chip that loaded. The
camera after the load is a new device in the slice, with the product
`0921` and its own device number, and the pod that uses the camera
claims it by those IDs. While the upload runs, the entry is `Running`,
and its message names the upload pod in `liken-system`.

The `Event`s give the same history for the last hour:

```sh
kubectl describe firmware qhy5l-ii
```

```
Events:
  Type    Reason         Age   From               Message
  ----    ------         ----  ----               -------
  Normal  Compiled       5m    firmware-operator  the selector compiled
  Normal  UploadStarted  2m    firmware-operator  usb-1-4-1-2 on liken-1: the upload pod liken-system/qhy5l-ii-3f2a9c1b0d4e runs the loader
  Normal  Loaded         2m    firmware-operator  loaded usb-1-4-1-2 on liken-1: the loader exited 0 and the bare device left the slice
```

The firmware stays until the device loses power. After a power cycle
or a replug, the bare chip appears with a new device number, and the
operator uploads again. The new appearance replaces the port's entry.

## A Conflict

Two `Firmware` objects that match one device run neither. Each entry
for that port is `Conflict`, and its message names the other rules.
The operator posts a `Conflict` `Event`, a Warning, on each. Narrow
one selector, or delete one of the rules. The rule that still matches
then uploads to the device that is there.

## Change the rule

An edit to the spec moves the `Firmware`'s generation, and the
operator uploads again to each matched device that is in a slice,
with the new spec. Use it to try a fix after a failed upload: the
operator does not retry a failure on its own.
