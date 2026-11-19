---
title: Running an observatory
weight: 60
---

# Running an observatory

The observatory operators run a telescope from a `liken` cluster. You
describe the observatory's equipment as Kubernetes resources: the
mount, the cameras, the focuser, the dome, the weather station, and
the other devices. A `Reservation` gives one holder the use of one
`Telescope`. While the reservation is active, the cluster runs the
equipment, and a program such as KStars drives it.

For example, an observatory can open its dome only while its weather
station reports safe weather. When the weather turns unsafe, the dome
parks after every mount has parked, and opens again only after 20
minutes of safe weather. At the end of a reservation, the camera warms
to 5 °C before its cooler turns off, because a sensor that loses its
cooler at -10 °C can crack.

## The operators

[`observatory-operator`](https://liken.sh/observatory/) controls an
observatory's hardware through INDI, under the API group
`observatory.liken.sh`. While a reservation is active, it runs each of
the telescope's INDI devices in its own pod, serves them on the
telescope's INDI server, and connects and configures each device in a
fixed order. It runs the activation procedures that each resource
lists, and starts PHD2 for the telescope's `Guider`, connected to the
guide camera and the mount. At the end of the reservation, it runs
each resource's deactivation procedures and reports that the devices
are safe to power off. The dome and the weather station run on the
observatory's own INDI server, which every telescope there shares. A
telescope serves one reservation at a time, and other reservations
wait in order of their start times.

[`astrophotography-operator`](https://liken.sh/astrophotography/)
takes the frames you ask for, under the API group
`astrophotography.liken.sh`. You declare a `Target`, an object and the
frames you want of it, and reserve a telescope for the operator. While
that reservation is active, the operator chooses a target, slews to
it, centers it with a plate solve, guides through PHD2, and takes the
frames through the telescope's INDI server. It moves each frame to the
archive the target names, and keeps a target's progress across
nights. It runs the plate solver, astrometry.net, in a pod for each
`Solver`.

## What they depend on

`observatory-operator` claims USB equipment from the devices that the
operating system publishes. Each device resource, such as a camera or
a mount, has a `spec.claim` field that holds a `ResourceClaim` spec.
The operator creates a `ResourceClaimTemplate` from it, so each pod of
the device gets its own `ResourceClaim`. You write a `DeviceClass` for
each piece of equipment, which selects the device by its vendor,
product, and serial number. USB serial and HID
equipment reaches the pod as its device node. A camera that a vendor's
SDK or gphoto2 drives has no kernel driver, so `liken` publishes the
whole USB device, and a claim gives the pod its USB node. See
[USB devices with no kernel driver](/docs/reference/devices/#usb-devices-with-no-kernel-driver).
Many QHY cameras power up as a bare chip with no firmware, and
[`firmware-operator`](https://liken.sh/firmware/) writes the firmware
into the chip before the camera's pod can open it. See
[QHY cameras that need firmware](https://liken.sh/observatory/docs/guides/connect-usb-equipment/#qhy-cameras-that-need-firmware).

The device pods run the project's `indi` images: one image of
`indiserver` and every core INDI driver, and one image for each family
of vendor drivers. PHD2 runs in its own pod beside a headless Weston
compositor.

## Extension points

* The INDI server. When a reservation is ready, its `status.endpoint`
  holds the address of the INDI server, on port 7624. KStars, or any
  other INDI client, drives the telescope through it.
* The PHD2 event server. The `Guider`'s `status.endpoint` holds the
  address of PHD2's event server, on port 4400, for a client that
  guides.
* The activation and deactivation procedures. Each resource lists the
  actions that run when its reservation starts and ends.
* The Go packages. The `observatory-operator` module holds the API
  types, a Go client of the INDI protocol, and a Go client of PHD2's
  event server, for a program of your own.

The operator serves each INDI server and each guider through a
`Service` of type `ClusterIP`, and you decide how a client outside the
cluster reaches it. INDI has no authentication and no encryption: any client
that reaches port 7624 can slew the mount.
