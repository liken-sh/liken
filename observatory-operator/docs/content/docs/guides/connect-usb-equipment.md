---
title: Connect USB equipment
weight: 30
description: "Give an INDI device its USB hardware: load the kernel driver on the liken machine, find the device in the node's ResourceSlice, write a DeviceClass, and put a claim in the device's spec.claim. Covers which astronomy equipment can reach a pod today and which cannot, and the QHY cameras that need firmware-operator to load their firmware. Use when connecting a real mount, focuser, filter wheel, power box, or camera, or when a device's pod stays Pending."
---

# Connect USB equipment

This guide gives a device resource its real hardware. The machine
the equipment plugs into publishes the USB device, a `DeviceClass`
selects it, and the device's `spec.claim` claims it. During a
reservation, the device's pod runs on that machine and its INDI
driver opens the device.

Two warnings come first.

* No drill has run this path on real equipment yet. The operator's
  tests and drills use the INDI simulators. The claim reaches the pod
  through the same Kubernetes machinery that other `liken` operators
  use for their hardware, but whether each INDI driver can open its
  device as the pod's user has not been checked.
* A QHY camera that powers up with no firmware works only on a cluster
  that runs `firmware-operator`, and a QHY camera on an FX3 chip that
  needs firmware does not work yet.
  [QHY cameras that need firmware](#qhy-cameras-that-need-firmware)
  explains.

Equipment on the network, such as a mount with a Wi-Fi adapter or a
weather station that serves its data over HTTP, needs no claim. Give
the driver its address in KStars, or in the driver's own properties.

## What can reach a pod

`liken` publishes a USB device when a kernel driver controls it, or
when no driver binds any part of it, because a program drives it
through libusb. That decides what works:

| Equipment | How it connects | Reaches a pod |
|---|---|---|
| Most mounts: EQMOD cables, Sky-Watcher and Celestron USB ports, LX200 serial | a USB serial chip: FTDI, Prolific PL2303, WCH CH340, or USB CDC-ACM | yes, as a tty |
| Most focusers and power boxes: MoonLite, Pegasus, and the like | a USB serial chip | yes, as a tty |
| ZWO EFW filter wheels, ZWO EAF focusers | USB HID | yes, with its USB node |
| ZWO, QHY, Player One, SVBony, ToupTek, and the other vendor-SDK cameras | the vendor's library over libusb, with no kernel driver | yes, as a whole device with its USB node |
| DSLRs and mirrorless cameras through gphoto2 | libusb, with no kernel driver | yes, as a whole device with its USB node |

A device with no kernel driver publishes whole, named by its USB port,
such as `usb-1-2`, and a claim on it delivers its USB node and nothing
else. The `liken`
[device reference](https://liken.sh/docs/reference/devices/#usb-devices-with-no-kernel-driver)
gives the rule.

## QHY cameras that need firmware

Many QHY cameras hold no firmware of their own. Such a camera powers up
as a bare Cypress EZ-USB chip, and the host must write the camera's
firmware into the chip before `indi_qhy_ccd` can open it. On a Linux
distribution, a udev rule of INDI's `libqhy` runs `fxload` to do that.
A `liken` machine runs no udev rules, so
[`firmware-operator`](https://liken.sh/firmware/) does it instead.
When the bare chip appears on any machine, `firmware-operator` runs
`fxload` from the `indi-qhy` image in a pod. The chip then resets and
enumerates again as the camera, with a new product ID.

The operator's `deploy/firmware/` holds one `Firmware` object for each
bare chip that a rule of `libqhy`'s `85-qhyccd.rules` loads with an
FX2 or FX2LP loader: 49 chips. To load QHY cameras, install
`firmware-operator` and apply `deploy/firmware/` beside the operator,
as [Install the operator](/docs/guides/install/#load-the-firmware-of-qhy-cameras)
describes. `firmware-operator` then loads each camera on every machine,
with or without a reservation, and the firmware stays in the camera
until it loses power.

* Write the camera's `DeviceClass` for the product ID after the load,
  not for the bare chip. A QHY5L-II, for example, publishes as
  `1618:0920` before the load and as `1618:0921` after it. A claim on
  the bare chip does not follow it when it enumerates again.
* The rules name 65 more chips that load with `fxload -t fx3`. The
  `fxload` in `indi-qhy` does not load FX3 chips, so a QHY camera that
  powers up as one of them stays bare and does not work yet.
  [`deploy/firmware/qhy.yaml`](https://github.com/liken-sh/liken/blob/main/observatory-operator/deploy/firmware/qhy.yaml)
  lists every chip that the objects load.
* Two of the rules match generic chips, not QHY's own IDs: `04b4:8613`,
  a Cypress FX2 with no EEPROM, and `0547:1002`, an Anchor Chips
  EZ-USB. `libqhy` loads a QHY5's firmware into any device that powers
  up with one of them. On a cluster that would also flash a logic
  analyzer or a development board that powers up blank, so
  `deploy/firmware/` has no object for either ID.

## 1. Load the kernel driver

A `liken` machine loads only the drivers for its disks and its network
ports. Every other device needs its driver named once in the
machine's `spec.modules`. Plug the equipment in, then read what the
machine found and cannot drive:

```sh
kubectl get machine <node> -o jsonpath='{.status.hardware.unclaimed}' | jq
```

Each entry names the device and the modules that can drive it. The
USB serial chips use `ftdi_sio`, `pl2303`, `ch341`, or `cdc_acm`, and
HID devices use `usbhid`. The `liken`
[hardware modules guide](https://liken.sh/docs/guides/hardware-modules/)
gives the steps to declare them. A camera that a vendor library or
gphoto2 drives has no kernel module to declare, so go on to step 2.

## 2. Find the device

When a driver controls the device, or when the device has no kernel
driver at all, it appears in the node's `ResourceSlice`:

```sh
kubectl get resourceslice <node>-liken.sh -o yaml
```

Look for the device by its `name`, `vendor`, and `product`
attributes. A USB serial adapter has `subsystem: tty`. Note the
`serial` attribute when it has one: an EQMOD cable and a focuser can
both use FTDI chips with the same vendor and product IDs, and the
serial number is what tells them apart. The `liken`
[device reference](https://liken.sh/docs/reference/devices/) lists
every attribute.

## 3. Write a DeviceClass

`liken` ships no `DeviceClass`, because only you know what each piece
of equipment is. Write one for each device, and select it as narrowly
as you need. This class selects one FTDI cable by its serial number:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: east-mount-cable
spec:
  selectors:
    - cel:
        expression: |
          device.driver == "liken.sh" &&
          device.attributes["liken.sh"].vendor == "0403" &&
          device.attributes["liken.sh"].product == "6001" &&
          has(device.attributes["liken.sh"].serial) &&
          device.attributes["liken.sh"].serial == "A10KXYZ1"
```

Guard an attribute that a device may lack with `has()`. A read of a
missing attribute is an evaluation error, and an evaluation error
stops the whole allocation.

## 4. Claim the device

A device's `spec.claim` is a `ResourceClaimSpec`. The operator creates
a `ResourceClaim` from it when the reservation starts the device's
pod, and deletes the claim when the pod stops:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Mount
metadata:
  name: east
spec:
  telescope: east
  driver:
    name: indi_eqmod_telescope
  claim:
    devices:
      requests:
        - name: mount
          exactly:
            deviceClassName: east-mount-cable
```

The operator creates no claim for a device on the shelf, or for a
device whose telescope has no active reservation. So equipment that
you describe stays free for other uses until you reserve it.

## 5. Reserve the telescope

During a reservation's `StartDevices` step, each device with a claim
waits until the scheduler allocates its hardware and its pod runs. The
pod runs on the machine that holds the device, and the driver reaches
the INDI server over the cluster network, so the devices of one
telescope can be on different machines. The guide camera is the
exception that the scheduler does not decide: without a claim, it runs
on the node of the telescope's INDI server, and with a claim, it runs
where its device is.

```sh
kubectl get rsv -n observatory -w
kubectl get resourceclaims -n observatory
```

A pod that stays `Pending` has a claim that no device satisfies. The
step's summary names the device it waits for, and
`kubectl describe pod` gives the scheduler's reason. A QHY camera's
pod also stays `Pending` until `firmware-operator` loads the camera,
because its claim selects the product ID that the camera has after the
load.

## 6. Check the port

The pod receives the device node at the same path that it has on the
machine, and no other tty. FTDI, PL2303, and CH340 adapters are
`/dev/ttyUSB<n>`, and CDC-ACM adapters are `/dev/ttyACM<n>`, numbered
in the order the kernel found them. Many INDI serial drivers open
their default port, usually `/dev/ttyUSB0`, and search the other
serial ports when that open fails. A driver with no search, or one
whose default port is a different name, does not find its device.

The operator connects each device during the `Connect` step. A device
that cannot open its port answers the connect with an error, and the
step fails with that device's name. The operator has no field for a
driver's port, and a driver in a pod does not keep its saved
configuration from one reservation to the next. Today, set the
driver's `DEVICE_PORT` property from KStars, and then run the failed
step again as [Troubleshoot](/docs/guides/troubleshoot/#run-a-failed-step-again)
describes.

When you unplug a cable that a device's claim holds, `liken` taints
the device, and Kubernetes evicts the device's pod. The operator
creates the pod again, and the new pod's claim takes the device where
you plug it in, with the nodes of that enumeration. The tty can then
have another number, such as `/dev/ttyUSB1` in place of
`/dev/ttyUSB0`, so check the port again. `liken`'s
[device reference](https://liken.sh/docs/reference/devices/#an-unplugged-device-evicts-its-pods)
gives the rules.
