---
title: Devices
weight: 25
toc: true
---

# Devices

liken hands hardware to workloads through dynamic resource
allocation, the Kubernetes API for devices. A pod asks for a device
by its properties, the scheduler picks a node that has one, and the
node gives that pod the device's `/dev` entries. The pod needs no
privilege, no host mounts, and no knowledge of which machine it
landed on.

[Give a workload a device](/docs/guides/devices/) has the steps. This
page describes what liken publishes and why.

## The driver

The driver's name is `liken.sh`. It is not a separate program: it is
two jobs inside the machine operator, which already runs on every
node. One walks sysfs and publishes what it finds. The other answers
the kubelet when a pod's claim lands on the node.

A DRA driver is normally a DaemonSet a cluster administrator
installs. liken's is part of the operating system, because the OS is
the only thing that knows what the hardware is before anything else
runs.

## What a node publishes

Each node publishes one `ResourceSlice`, named `<node>-liken.sh`. It
lists the node's claimable devices:

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

A device from the PCI or USB bus appears there when all three of
these are true:

1. **A driver is bound to it.** Undriven hardware cannot deliver
   anything, so it belongs in the machine's unclaimed report instead.
   Declaring a module in the Machine's `spec.modules` is what moves a
   device from one report to the other.
2. **Claiming it would deliver something.** The device's sysfs
   subtree must carry device nodes. A network card is real hardware
   with nothing to hand a pod, so it never appears.
3. **The machine does not depend on it.** A device whose subtree
   backs a storage role is the machine's own, and never a workload's.
   A disk belongs either to the machine, through a storage role, or
   to the workloads, through a claim, and never to both.

The slice is an offer, not a census. The scheduler can only allocate
what a slice lists, so publishing is itself the enforcement.

The bus plumbing is left out: hubs, PCIe ports, and the USB core's
own devices are the structure that peripherals attach to, not
peripherals.

## Device names

A device's name is its bus and its address, with the punctuation
turned into dashes: `pci-0000-00-02-0`, `usb-2-1-1-0`.

The address names the slot, not the unit. A dongle replaced with an
identical one in the same port keeps the same device name, which is
what a claim on "the adapter on that port" needs. To pin one physical
unit instead, select on its `serial` attribute.

## Attributes

Every attribute belongs to the driver's domain, so a selector reads
it as `device.attributes["liken.sh"].<name>`. An attribute the
hardware does not have is absent rather than empty, so
`has(device.attributes["liken.sh"].serial)` means what it says.

| Attribute | Type | What it is |
|---|---|---|
| `bus` | string | `pci` or `usb` |
| `driver` | string | the bound driver's name, such as `i915` |
| `class` | string | the kind, in one word: `display`, `multimedia`, `serial-bus` |
| `classCode` | string | the whole class code the bus published: six hex digits on PCI, two on USB |
| `subsystem` | string | the kind of node a claim delivers, when they all agree: `drm`, `tty` |
| `renderNode` | bool | the device delivers a DRM render node |
| `name` | string | the device in words, from its own strings or the PCI database |
| `modalias` | string | the fingerprint the kernel matches drivers against |
| `serial` | string | the serial number the hardware carries, when it carries one |
| `vendor` | string | the vendor ID, bare lowercase hex |
| `product` | string | the product ID, bare lowercase hex |

Prefer the attribute that describes what you need. `renderNode` and
`classCode` describe the hardware's capability, and hold across a
fleet of different machines. `vendor` and `product` name one model,
and a DeviceClass written with them stops working on the next machine
you buy.

liken publishes no capability facts that need a driver stack to
measure, such as which codecs a GPU can encode. Reading those means
running libva and a vendor driver, which the image does not carry. A
pod that holds a claim on the render node can measure them for
itself, so they are not the operating system's to publish.

## Sharing

A device is allocated to one claim, unless liken publishes
`allowMultipleAllocations` for it. liken publishes it for a device
whose every delivered node is a DRM node, and for nothing else.

A DRM render node multiplexes by the kernel's own contract: the
driver arbitrates concurrent clients. The lab measured this on an
integrated GPU, where twelve concurrent encoders divided it evenly
and two pods each reached about half the throughput one pod reached
alone. A serial port has no such contract, and neither does a
dongle's control endpoint.

Only the driver can state this, because only the driver writes a
`ResourceSlice`. That is why the rule is narrow. A device wrongly
marked shareable hands the same hardware to two workloads that each
believe they hold it, and no DeviceClass, claim, or workload can take
it back. A device wrongly marked exclusive costs a claim that waits,
where a person can see it waiting.

### Sharing a claim is not the same thing

`allowMultipleAllocations` governs how many **claims** may allocate a
device. It does not govern how many **pods** may use one claim.

A `ResourceClaim` is shared by every pod that names it, and all of
them receive the device. That is deliberate in Kubernetes: it is how
two pods share one allocation. liken does not refuse the second pod.
Refusing would be a race with no owner, and delivering a device node
was never what granted exclusive access in the first place — the
kernel enforces that, through `O_EXCL` and the driver's own open
path.

To give one pod a device by itself, use a `ResourceClaimTemplate`,
which mints a separate claim for each pod. A shared `ResourceClaim`
is a deliberate act of sharing.

## What a claim delivers

Device nodes, and nothing else. The node writes a CDI specification
naming the `/dev` entries for that claim, and the container runtime
injects them into the containers that requested it. No privilege is
granted, no host path is mounted, and no kernel module is loaded on
the pod's behalf.

Everything else the workload needs is the workload's: the userspace
library that talks to the device, and the group membership its image
gives its user.

## Limits

* One slice per node, holding up to 128 devices. A node with more
  reports the overflow on its console rather than splitting the pool.
* liken ships no DeviceClasses. A DeviceClass states what a
  deployment cares about, so a deployment writes its own. The
  [guide](/docs/guides/devices/) has examples to start from.
* Devices carry no capacity and no taints yet. A device that fails
  disappears from the slice at the next pass, which stops new
  allocations but does not disturb an allocation in flight.
* A claim is not remembered across a reboot. The claim is a
  Kubernetes object, and the pod that holds it is rescheduled with
  it.

## Hardware that is not published

Hardware with no driver bound cannot be claimed. It appears in two
places instead:

* The Machine's status lists it as unclaimed hardware, with the
  modules that would drive it.
* The [hardware report](/docs/guides/install/#first-run-the-hardware-report)
  names it before the first install, as commented lines under
  `spec.modules`.

Declaring the module in `spec.modules` is the whole step. The device
appears in the node's slice within one reconcile pass.
