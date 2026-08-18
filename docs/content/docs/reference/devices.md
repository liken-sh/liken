---
title: Devices
weight: 25
toc: true
---

# Devices

liken gives hardware to workloads with dynamic resource allocation,
the Kubernetes API for devices. A pod asks for a device by its
properties. The scheduler selects a node that has such a device, and
the node gives that pod the device's `/dev` entries. The pod needs no
privilege, no host mounts, and no knowledge of the machine it runs
on.

[Give a workload a device](/docs/guides/devices/) gives the steps.
This page describes what liken publishes, and why.

## The driver

The driver's name is `liken.sh`. It is not a separate program. It is
two jobs in the machine operator, which runs on every node. One job
reads sysfs and publishes what it finds. The other job answers the
kubelet when a pod's claim comes to the node.

Usually a cluster administrator installs a DRA driver as a DaemonSet.
liken's driver is part of the operating system, because the operating
system is the only thing that identifies the hardware before other
software starts.

The [hardware operators](/docs/concepts/hardware-operators/) are
separate DRA drivers that you install as workloads. Each one
publishes a kind of device this driver does not, such as the
controllers paired to a Bluetooth radio.

## What a node publishes

Each node publishes one `ResourceSlice`, with the name
`<node>-liken.sh`. The slice lists the node's claimable devices:

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

A device on the PCI bus or the USB bus appears in the slice when
these three conditions are true:

1. **A driver is bound to it.** Hardware with no driver can supply
   nothing, so it goes in the machine's unclaimed report instead. To
   move a device from one report to the other, declare its module in
   the Machine's `spec.modules`.
2. **A claim on it supplies something.** The device's sysfs subtree
   must contain device nodes. A network card is hardware, but it has
   nothing to give a pod, so it never appears. A Bluetooth adapter is
   the one exception. Its driver, `btusb`, puts it in the slice,
   because the kernel gives a radio no device node at all. See
   [Bluetooth adapters](#bluetooth-adapters).
3. **The machine does not depend on it.** A device whose subtree holds a
   storage role belongs to the machine, and never to a workload. A
   disk belongs to the machine, through a storage role, or to the
   workloads, through a claim, but never to both.

The slice is an offer, and not a full record of the machine's
hardware. The
scheduler can allocate only what a slice lists, so the contents of
the slice are the control.

The slice does not list the bus structure. Hubs, PCIe ports, and the
USB core's own devices are the structure that peripherals attach to,
not peripherals.

## Device names

A device's name is its bus and its address, with dashes in place of
the punctuation: `pci-0000-00-02-0`, `usb-2-1-1-0`.

The address gives the slot, not the unit. If you replace a dongle
with an identical dongle in the same port, the device name does not
change, which is what a claim on the adapter on that port needs. To
select one physical unit instead, select on its `serial` attribute.

A constraint pairs the requests of a claim by an attribute, and never
by name. The address is also an attribute, `address`, so a claim can
constrain two requests to one physical device.

## Attributes

Every attribute belongs to the driver's domain, so a selector reads
it as `device.attributes["liken.sh"].<name>`. If the hardware does
not have an attribute, the attribute is absent, not empty. Thus
`has(device.attributes["liken.sh"].serial)` gives a correct result.

| Attribute | Type | What it is |
|---|---|---|
| `bus` | string | `pci` or `usb` |
| `address` | string | the device's address on that bus: `0000:00:02.0` on PCI, the port path on USB. Every device liken publishes for one physical device carries the same address |
| `driver` | string | the name of the bound driver, such as `i915` |
| `class` | string | the type of device, in one word: `display`, `multimedia`, `serial-bus` |
| `classCode` | string | the full class code that the bus published: six hex digits on PCI, two on USB |
| `subsystem` | string | the kind of device that liken published: `drm`, `sound`, `tty`. It is absent when the delivery is a mix that liken does not know, and when the device delivers no node of its own, as a Bluetooth adapter does |
| `renderNode` | bool | the device supplies a DRM render node |
| `displayNode` | bool | the device supplies a DRM card node, which carries modesetting |
| `name` | string | the name of the device in words, from its own strings or from the PCI database |
| `modalias` | string | the identifier that the kernel uses to match drivers |
| `serial` | string | the serial number of the hardware, when it has one |
| `vendor` | string | the vendor ID, lowercase hex with no prefix |
| `product` | string | the product ID, lowercase hex with no prefix |

Use the attribute that describes what you need. `renderNode` and
`classCode` describe a capability of the hardware, and they stay
correct across a fleet of different machines. `vendor` and `product`
give one model, and a DeviceClass that uses them stops working on the
next machine that you buy.

`address` is the attribute that pairs devices. Two cards in one
machine have different addresses, and every device liken publishes for
one card carries that card's address. So a claim with a request for a
render node and a request for a card node constrains the two with
`matchAttribute: liken.sh/address`, and both requests get halves of
the same card. The
[guide](/docs/guides/devices/#two-requests-one-card) has the claim.

liken publishes no capability facts that need a driver stack to
measure, for example the codecs that a GPU can encode. To read those
facts, you must run libva and a vendor driver, which the image does
not contain. A pod that holds a claim on the render node can measure
them for itself, so the operating system does not publish them.

## Sharing

liken allocates a device to one claim, unless liken publishes
`allowMultipleAllocations` for that device. A device's subtree can
deliver nodes of more than one kernel subsystem. When it does, liken
can publish more than one device for it, one for each kind of node. A
GPU splits again, because its render node and its card node give
different authority over the same silicon. The name of each extra
device is the primary device's name plus a suffix. A claim receives
the nodes of the one published device it allocated, and no others.

liken publishes `allowMultipleAllocations` for one kind of device:
the graphics half of a GPU. A graphics device is one that delivers a
DRM render node, and the published graphics device delivers that
render node, `/dev/dri/renderD*`. It carries `renderNode: true`. The
driver arbitrates between concurrent clients on a render node, so
more than one claim can hold it.

An audio controller is a device that delivers ALSA's nodes, and no
nodes except the ones a sound card holds. It carries
`subsystem: sound`, and it is exclusive. In practice one sound server
owns every PCM on a card and mixes its clients' streams through them,
so the card belongs to one claim. A second claimant waits in the
scheduler, where a person can see it wait, instead of meeting ALSA's
`EBUSY` at play time.

A claim on an audio controller delivers the card's whole subtree,
which is more than the `/dev/snd` nodes. ALSA registers an input
device for each jack that it can sense, and an HDA controller with
HDMI outputs has one for each display pin, so the claim also delivers
those `/dev/input/event*` nodes. A jack reports the state of an
output that the same claim plays through, so it is not a separate
device.

A GPU publishes its card node, `/dev/dri/card*`, as a separate
device. The name of that device is the primary name plus `-display`,
and it carries `displayNode: true`. This device is exclusive, because
DRM master is one for each card: the kernel gives modesetting to one
open card node, and a second display program on the same card fails
when it starts. An exclusive device makes the second claim wait in
the scheduler instead, where a person can see it wait. A workload
that modesets and also renders claims both devices, with one request
for each. On a machine with two cards, a constraint of
`matchAttribute: liken.sh/address` on those two requests keeps both on
one card.

A GPU driver also registers i2c monitor-control buses. These publish
as their own device, with `subsystem: i2c-dev`. That device stays
exclusive. An i2c node passes raw transfers to every device on its
wire, and two writers on one wire have no arbitration contract. A
DisplayPort output also registers a DisplayPort AUX channel, with
`subsystem: drm_dp_aux_dev`. This node publishes the same way, as its
own exclusive device, and it exists only while a display is
connected. The legacy framebuffer node is not delivered at all:
holding it grants display takeover, and no workload claims a bare
framebuffer.

A device that delivers one kind of node publishes as one device,
unless it is a GPU. A device that delivers a mix that liken does not
know publishes whole and exclusive, and names no `subsystem`. A
Bluetooth adapter publishes the same way, whole and exclusive with no
`subsystem`, because the only node it delivers is its usbfs node.

A DRM render node has a multiplexing contract in the kernel: the
driver arbitrates between concurrent clients. A drill measured this
on an integrated GPU. Twelve concurrent encoders divided the GPU
equally, and two pods each got approximately half of the throughput
that one pod got alone. A serial port has no such contract, and a
dongle's control endpoint has none.

Only the driver can state this, because only the driver writes a
`ResourceSlice`. Thus the rule is narrow. If a device is incorrectly
marked as shareable, two workloads get the same hardware while each
one operates as the only user, and no DeviceClass, claim, or workload
can correct it. If a device is incorrectly marked as exclusive, a
claim waits, and a person can see that it waits.

### Sharing a claim is not the same thing

`allowMultipleAllocations` controls how many **claims** can allocate
a device. It does not control how many **pods** can use one claim.

Every pod that names a `ResourceClaim` shares that claim, and all of
these pods receive the device. Kubernetes does this on purpose: it is
how two pods share one allocation. liken does not refuse the second
pod. A refusal would be a race with no owner. Also, the delivery of a
device node was never the mechanism that gave exclusive access. The
kernel gives exclusive access, through `O_EXCL` and the driver's own
open path.

To give a device to one pod only, use a `ResourceClaimTemplate`,
which makes a separate claim for each pod. If you use one
`ResourceClaim` for more than one pod, you share the device on
purpose.

## What a claim delivers

A claim delivers device nodes only. The node writes a CDI
specification that names the `/dev` entries for that claim, and the
container runtime injects them into the containers that requested the
claim. The claim grants no privilege, mounts no host path, and loads
no kernel module for the pod.

The workload supplies everything else that it needs: the userspace
library that communicates with the device, and the group membership
that its image gives its user.

### USB devices

A claim on a USB device also delivers that device's usbfs node,
`/dev/bus/usb/<busnum>/<devnum>`. A program that uses libusb, for
example Network UPS Tools, reads sysfs to find the hardware and then
opens this node to communicate with it. A node that a kernel driver
registers, such as `hidraw`, carries that driver's protocol only, so
it cannot take the place of the usbfs node.

A program that uses libusb cannot share an interface with a kernel
driver, so it detaches the kernel driver while it runs. liken
publishes only devices that have a driver, so the device leaves the
node's slice for as long as the pod runs. When the pod stops and its
claim ends, the node binds a kernel driver to the interface again, and
the device returns to the slice at the next reconcile pass.

The kernel gives the device a new device number at each enumeration.
The node changes when you unplug the device and plug it in again.
Each reconcile pass writes the current node into the specification of
every claim that the kubelet prepared, so the next pod receives the
current node. A container that already runs keeps the node that it
received, and it receives the current node when the pod restarts.

usbfs has no interface boundary. If a device has more than one
interface with a driver, each interface publishes as its own device,
and each one delivers the same usbfs node. A pod that holds one of
these claims can communicate with the whole device.

### Bluetooth adapters

A claim on a Bluetooth adapter delivers the adapter's usbfs node, and
nothing else. The adapter has no node of its own. A program reaches a
radio through an `AF_BLUETOOTH` socket, which it binds to an adapter
by index, so the kernel registers no `/dev` entry anywhere the adapter
owns. The claim states which workload owns the radio, and it holds
that workload on the machine the radio is in. A stack that drives the
radio through the kernel opens its socket with the capabilities of its
own container, because a claim grants no privilege.

liken stops the delivery walk at a `bluetooth` subtree. The kernel
puts the HID device of a connected peripheral under the adapter's own
USB interface, so a game controller's `/dev/input/event*` and
`/dev/hidraw*` nodes appear in the adapter's part of sysfs. Those
nodes belong to the controller. A claim on the adapter does not
deliver them, and a controller that connects later adds no nodes to
the claim.

The same nodes move when BlueZ changes how it drives the kernel. With
`/dev/uhid` present, BlueZ 5.73 and later create the HID device under
`/sys/devices/virtual/misc/uhid`, where nothing connects it to the
adapter. A claim that delivered these nodes would deliver them only
under one of the two arrangements.

## Limits

* One slice for each node, with a maximum of 128 devices. If a node
  has more devices, it prints the count of the devices it dropped on
  its console, and no claim can reach those devices. It does not
  divide the pool.
* liken supplies no DeviceClasses. A DeviceClass states what a
  deployment needs, so each deployment writes its own. The
  [guide](/docs/guides/devices/) has examples to start from.
* Devices have no capacity and no taints at this time. If a device
  fails, it disappears from the slice at the next pass. This stops
  new allocations, but it does not change an allocation that is in
  use.
* liken does not keep a claim across a reboot. The claim is a
  Kubernetes object, and Kubernetes reschedules the pod that holds it
  together with the claim.

## Hardware that is not published

You cannot claim hardware that has no driver bound to it. liken shows
this hardware in two other places:

* The Machine's status lists it as unclaimed hardware, with the
  modules that can drive it.
* The [hardware report](/docs/guides/install/#first-run-the-hardware-report)
  lists it before the first install, as commented lines under
  `spec.modules`.

To publish the device, declare the module in `spec.modules`. This is
the only necessary step. The device appears in the node's slice at
the next reconcile pass.
