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
   nothing to give a pod, so it never appears.
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

## Attributes

Every attribute belongs to the driver's domain, so a selector reads
it as `device.attributes["liken.sh"].<name>`. If the hardware does
not have an attribute, the attribute is absent, not empty. Thus
`has(device.attributes["liken.sh"].serial)` gives a correct result.

| Attribute | Type | What it is |
|---|---|---|
| `bus` | string | `pci` or `usb` |
| `driver` | string | the name of the bound driver, such as `i915` |
| `class` | string | the type of device, in one word: `display`, `multimedia`, `serial-bus` |
| `classCode` | string | the full class code that the bus published: six hex digits on PCI, two on USB |
| `subsystem` | string | the type of node that a claim supplies, when all the nodes agree: `drm`, `tty` |
| `renderNode` | bool | the device supplies a DRM render node |
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

liken publishes no capability facts that need a driver stack to
measure, for example the codecs that a GPU can encode. To read those
facts, you must run libva and a vendor driver, which the image does
not contain. A pod that holds a claim on the render node can measure
them for itself, so the operating system does not publish them.

## Sharing

liken allocates a device to one claim, unless liken publishes
`allowMultipleAllocations` for that device. A device's subtree can
deliver nodes of more than one kernel subsystem. When it does, liken
can publish more than one device for it, one for each kind of node.
The name of each extra device is the primary device's name plus the
subsystem. A claim receives the nodes of the one published device it
allocated, and no others.

liken publishes `allowMultipleAllocations` on a graphics device only.
A graphics device is one that delivers a DRM render node, and the
published graphics device delivers only the `/dev/dri` nodes. A GPU
driver registers i2c monitor-control buses. These publish as their
own device, with `subsystem: i2c-dev`. That device stays exclusive.
An i2c node passes raw transfers to every device on its wire, and two
writers on one wire have no arbitration contract. The legacy
framebuffer node is not delivered at all: holding it grants display
takeover, and no workload claims a bare framebuffer.

A device that delivers one kind of node publishes as one device. A
device that delivers a mix that liken does not know publishes whole
and exclusive, and names no `subsystem`.

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
