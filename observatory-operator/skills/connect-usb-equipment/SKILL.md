---
name: connect-usb-equipment
description: "Give an INDI device its USB hardware: load the kernel driver on the liken machine, find the device in the node's ResourceSlice, write a DeviceClass, and put a claim in the device's spec.claim. Covers which astronomy equipment can reach a pod and which cannot, and the QHY cameras that need firmware-operator to load their firmware. Use when connecting a real mount, focuser, filter wheel, power box, or camera, or when a device's pod stays Pending."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/connect-usb-equipment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Connect USB equipment

At the end of this guide, a device resource such as a `Mount` drives
real USB equipment. The machine that the equipment is plugged into
publishes the USB device, a `DeviceClass` that you write selects it,
and the device's `spec.claim` claims it. During a reservation, the
device's pod runs on that machine and its INDI driver opens the
device.

Before you start, know these limits:

* Nobody has tested this path on real equipment. The operator's tests
  use the INDI simulators. The pod gets its device through the same
  Dynamic Resource Allocation that other `liken` operators use for
  their hardware, but nobody has checked that each INDI driver can open
  its device as the pod's user.
* A QHY camera that powers up with no firmware works only on a cluster
  that runs `firmware-operator`, and a QHY camera on an FX3 chip that
  needs firmware does not work at all.
  [QHY cameras that need firmware](#qhy-cameras-that-need-firmware)
  explains.

Equipment on the network, such as a mount with a Wi-Fi adapter or a
weather station that serves its data over HTTP, needs no claim. Give
the driver its address in KStars, or in the driver's own properties.

## What can reach a pod

`liken` publishes a USB device when a kernel driver controls it, or
when no driver binds any part of it, because a program drives it
through libusb. So this equipment can reach a pod:

| Equipment | How it connects | Reaches a pod |
|---|---|---|
| Most mounts: EQMOD cables, Sky-Watcher and Celestron USB ports, LX200 serial | a USB serial chip: FTDI, Prolific PL2303, WCH CH340, or USB CDC-ACM | yes, as a tty |
| Most focusers and power boxes: MoonLite, Pegasus, and the like | a USB serial chip | yes, as a tty |
| ZWO EFW filter wheels, ZWO EAF focusers | USB HID | yes, with its USB node |
| ZWO, QHY, Player One, SVBony, ToupTek, and the other vendor-SDK cameras | the vendor's library over libusb, with no kernel driver | yes, as a whole device with its USB node |
| DSLRs and mirrorless cameras through gphoto2 | libusb, with no kernel driver | yes, as a whole device with its USB node |

`liken` publishes a device with no kernel driver as one whole USB
device, named for its USB port, such as `usb-1-2`. A claim on it gives
the pod the device's USB node and no other device node. The `liken`
[device reference](https://liken.sh/docs/reference/devices/#usb-devices-with-no-kernel-driver)
describes the rule.

## QHY cameras that need firmware

Many QHY cameras keep no firmware of their own. Such a camera powers up
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
FX2 or FX2LP loader. To load QHY cameras, install
`firmware-operator` and apply `deploy/firmware/` beside the operator,
as [Install the operator](https://liken.sh/observatory/docs/guides/install/#load-the-firmware-of-qhy-cameras)
describes. `firmware-operator` then loads each camera on every machine,
with or without a reservation, and the firmware stays in the camera
until it loses power.

* Write the camera's `DeviceClass` for the product ID after the load,
  not for the bare chip. A QHY5L-II, for example, publishes as
  `1618:0920` before the load and as `1618:0921` after it. A claim on
  the bare chip does not follow it when it enumerates again.
* Some rules load their chip with `fxload -t fx3`. The `fxload` in
  `indi-qhy` cannot load an FX3 chip, so a QHY camera that powers up
  as one stays bare and does not work.
  [`deploy/firmware/qhy.yaml`](https://github.com/liken-sh/liken/blob/main/observatory-operator/deploy/firmware/qhy.yaml)
  lists every chip that the objects load.
* The rules for `04b4:8613`, a Cypress FX2 with no EEPROM, and
  `0547:1002`, an Anchor Chips EZ-USB, match generic chips, not QHY's
  own IDs. `libqhy` loads a QHY5's firmware into any device that powers
  up as either chip, which on a cluster would also flash a blank logic
  analyzer or development board. So `deploy/firmware/` has no object
  for either ID.

## 1. Load the kernel driver

A `liken` machine loads only the drivers for its disks and its network
ports. For every other device, you add its driver to the machine's
`spec.modules` once. Plug the equipment in, then list the devices that
the machine found and has no driver for:

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
          has(device.attributes["liken.sh"].vendor) &&
          device.attributes["liken.sh"].vendor == "0403" &&
          has(device.attributes["liken.sh"].product) &&
          device.attributes["liken.sh"].product == "6001" &&
          has(device.attributes["liken.sh"].serial) &&
          device.attributes["liken.sh"].serial == "A10KXYZ1"
```

Guard an attribute that a device may lack with `has()`. A read of a
missing attribute is an evaluation error, and an evaluation error
stops the whole allocation.

## 4. Claim the device

A device's `spec.claim` is a `ResourceClaimSpec`. When the reservation
starts the device's pod, the operator creates a `ResourceClaimTemplate`
from it, and the pod makes its own `ResourceClaim` from the template.
The claim ends with the pod, and the operator deletes the template when
the reservation releases the device:

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

The operator creates no template for a device in inventory, which has
no parent field, or for a device whose telescope or observatory has no
active reservation. So the hardware of a device that you describe
stays free for other uses until you reserve its telescope.

## 5. Reserve the telescope

During a reservation's `StartDevices` step, each device with a claim
waits until the scheduler allocates its hardware and its pod runs. The
pod runs on the machine that holds the device, and the driver reaches
the INDI server over the cluster network, so the devices of one
telescope can be on different machines. The guide camera is an
exception: without a claim, it runs on the node of the telescope's INDI
server, and with a claim, it runs on the node of its device.

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

The operator connects each device during the `Connect` step. When a
driver cannot open its port, the connect fails, and the step fails
with that device's name. The operator has no field for a
driver's port, and a driver in a pod does not keep its saved
configuration from one reservation to the next. To set it, change the
driver's `DEVICE_PORT` property from KStars, and then run the failed
step again as [Troubleshoot](https://liken.sh/observatory/docs/guides/troubleshoot/#run-a-failed-step-again)
describes.

When you unplug a cable, `liken` taints the device
`liken.sh/unplugged`, and Kubernetes evicts the device's pod, as the
[device reference](https://liken.sh/docs/reference/devices/#an-unplugged-device-evicts-its-pods)
describes. While the reservation is `Ready`, the operator creates the
pod again, and the new pod's claim is allocated wherever the device is
when you plug it in again: the same port, another port, or another
machine. Until then the pod stays `Pending`. The tty can then have
another number, such as `/dev/ttyUSB1` in place of `/dev/ttyUSB0`, so
check the port again. The machine must run `liken` 2026.10.28-003 or
later for the eviction.
