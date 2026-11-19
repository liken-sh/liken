---
title: Devices
weight: 25
toc: true
---

# Devices

`liken` gives hardware to workloads through dynamic resource allocation
(DRA), the Kubernetes API for devices. A pod requests a device by its
properties. The scheduler picks a node that has a matching device, and
the node adds the device's `/dev` entries to the pod's containers. The
pod needs no privilege, no host mounts, and no knowledge of the machine
it runs on.

[Give a workload a device](/docs/guides/devices/) has the steps.

## The driver

The DRA driver is named `liken.sh`. It is part of the machine operator,
which runs on every node, not a separate program. The machine operator
reads sysfs and publishes the devices it finds, and it responds to the
kubelet when a pod's claim is prepared on the node.

A DRA driver is usually a DaemonSet that an administrator installs.
This one is part of the operating system, so you install nothing to
publish a node's devices.

The [device operators](/docs/concepts/claiming-hardware/) are separate
DRA drivers that you install as workloads. Each one publishes a kind of
device that this driver does not, such as the controllers paired to a
Bluetooth radio.

## What a node publishes

Each node publishes one `ResourceSlice`, named `<node>-liken.sh`. The
slice lists the node's claimable devices:

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

The node considers every device on the PCI bus and the USB bus, and
every device on the board that has a device node. A board device is one
that the firmware describes and no bus enumerates: a firmware TPM, a
laptop's own keyboard behind the i8042 controller, the ACPI power
button, or a serial port on the board. A device appears in the slice
when all of these are true:

1. **A driver is bound to it.** Hardware with no driver cannot give a
   pod anything, so it appears in the Machine's unclaimed hardware
   report instead. To move a device from that report to the slice,
   declare its module in the Machine's `spec.modules`. A USB device
   that no driver binds is the exception, because a program drives it
   through libusb. See
   [USB devices with no kernel driver](#usb-devices-with-no-kernel-driver).
2. **It has device nodes to give a pod.** The device's sysfs subtree
   must contain device nodes. A network card has none, so it
   never appears. A Bluetooth adapter is the exception. The kernel
   registers no device node for a radio, but its driver, `btusb`, puts
   the adapter in the slice anyway. See
   [Bluetooth adapters](#bluetooth-adapters).
3. **The machine does not depend on it.** A device whose subtree
   contains a partition that backs a storage role belongs to the
   machine, and a workload cannot claim it. A disk belongs either to
   the machine, through a storage role, or to workloads, through a
   claim, and never to both. `machine-operator` reads which partitions
   back a role from the facts that init publishes. When it cannot read
   the facts, it cannot tell a system disk from a spare one, so it
   publishes no disk, and the Machine's `FactsPublished` condition is
   `False`. A device with no disk in its subtree, such as a GPU, stays
   in the slice. A claim that already holds a disk cannot start its
   next container until the operator reads the facts again. The
   operator also checks each disk when it prepares a claim, so an
   allocation made from an older slice cannot provide a system disk.

No claim receives these nodes, because the machine itself uses them:

* The console, which the kernel lists in
  `/sys/class/tty/console/active`. The kernel writes the boot messages
  and its log to it.
* `/dev/rtc0`, the clock that init writes the system time into. The
  kernel lets only one process at a time open it.
* The TPM's nodes, `/dev/tpm*` and `/dev/tpmrm*`. A process that opens
  them can use the TPM directly. On most boards the TPM's owner and
  lockout passwords are empty, so an unprivileged pod could set them,
  clear the TPM, or extend its PCRs until the next boot, and each of
  those affects every later user of the TPM.
* The tty of a serial line that a `spec.serio` entry matches. The
  machine keeps that line attached to the kernel's serio layer, and a
  pod that received the tty could end the attachment for every other
  claim. See [Serial-line adapters](#serial-line-adapters).

A device whose only nodes are these does not appear.

The node also publishes up to three virtualization devices, which
belong to no bus: `/dev/kvm`, `/dev/vhost-net`, and `/dev/vhost-vsock`.
A program that boots a virtual machine opens them, and the container
runtime's default device rules refuse them to every container that has
no claim. Each one appears when its module is loaded. See
[Virtualization devices](#virtualization-devices).

The slice lists what workloads can claim, not all of the machine's
hardware, and the scheduler can allocate only a device that the slice
lists. It leaves out the devices that peripherals attach to: hubs, PCIe
ports, and the USB core's own devices.

The node walks its devices again when the kernel sends a uevent for a
device that arrives, leaves, or changes its driver. The node waits
until one second passes with no further uevent, or five seconds at most,
so that the many uevents of one USB device cause one walk. Then it
writes the slice. A device reaches the slice about a second after the
kernel reports it. The uevents of the virtual network devices that pods
add and remove do not start a walk.

## Device names

A device's name is its bus and its address, in lowercase, with dashes in
place of punctuation: `pci-0000-00-02-0`, `usb-2-1-1-0`. A board
device's address is the name that the firmware gives it, so a firmware
TPM is `platform-msft0101-00`. A virtualization device's name is `misc-`
followed by the kernel's name for its node: `misc-kvm`,
`misc-vhost-net`, and `misc-vhost-vsock`. A name has at most 63
characters, the limit of a DNS label.

The address identifies a position, not a unit. When you replace a
dongle with an identical dongle in the same port, the device name stays
the same, which is what a claim on "the adapter on that port" needs. To
select one physical unit, select on its `serial` attribute.

A constraint pairs the requests of a claim by an attribute, not by
name. The address is also an attribute, `address`, so a claim can
constrain two requests to one physical device.

## Attributes

Every attribute except `sound.liken.sh/supportsSound` and
`resource.kubernetes.io/pciBusID` is in the driver's domain, so a
selector reads it as `device.attributes["liken.sh"].<name>`. When the
hardware does not have an attribute, the attribute is absent, not
empty, so `has(device.attributes["liken.sh"].serial)` tells you whether
a device has a serial number.

No attribute is on every device. A virtualization device such as
`misc-vhost-net` has no `bus` and no `driver`. So a selector must check
each attribute with `has()` before it reads it. The scheduler runs a `DeviceClass` selector against every
device of every node, and a selector that reads an absent attribute
fails with the CEL error "no such key". The scheduler then fails every
claim of that class on that node, not only the claim for the device
that lacks the attribute. So write
`has(device.attributes["liken.sh"].bus) && device.attributes["liken.sh"].bus == "usb"`,
not the comparison alone.

| Attribute | Type | What it is |
|---|---|---|
| `bus` | string | `pci` or `usb`. For a board device, the bus the kernel registered it on, such as `platform` |
| `address` | string | the device's address on that bus: `0000:00:02.0` on PCI, the port path on USB, and the firmware's name for a board device, such as `MSFT0101:00`. Every device that `liken` publishes for one physical device has the same address |
| `driver` | string | the name of the bound driver, such as `i915`. A virtualization device has no bound driver, so this is the module that registered its node: on `misc-kvm`, the vendor module, `kvm_intel` or `kvm_amd`, and on `misc-vhost-net` and `misc-vhost-vsock`, `vhost_net` and `vhost_vsock`. A module that is built into the kernel and has no parameters has no entry in `/sys/module`, and the device then has no `driver` |
| `class` | string | the type of device, in one word: `display`, `multimedia`, `serial-bus`, or `virtualization` for a virtualization device |
| `classCode` | string | the full class code that the bus reports: six hex digits on PCI, two on USB |
| `subsystem` | string | the kind of node that `liken` publishes the device for: `drm`, `sound`, `tty`, `cec`. It is absent when the device's nodes are a mix that `liken` has no name for, and when the device has no node of its own, as with a Bluetooth adapter |
| `renderNode` | bool | the device supplies a DRM render node |
| `displayNode` | bool | the device supplies a DRM card node, which provides modesetting |
| `kvm` | bool | the device supplies `/dev/kvm` |
| `nested` | bool | on `misc-kvm`, the vendor module's `nested` parameter is on, so a guest can run KVM itself |
| `vhostNet` | bool | the device supplies `/dev/vhost-net` |
| `vhostVsock` | bool | the device supplies `/dev/vhost-vsock` |
| `name` | string | the name of the device in words, from its own strings or from the PCI database |
| `modalias` | string | the identifier that the kernel uses to match drivers |
| `serial` | string | the serial number of the hardware, when it has one |
| `vendor` | string | the vendor ID, lowercase hex with no prefix |
| `product` | string | the product ID, lowercase hex with no prefix |
| `busNumber` | int | the number of the USB bus that the device is on, from `busnum` in sysfs. Only USB devices have it |
| `deviceNumber` | int | the number that the kernel assigned to the USB device on its bus, from `devnum` in sysfs. The kernel assigns a new number each time the device enumerates, after a replug, a reset, or a firmware upload. Only USB devices have it |
| `sound.liken.sh/supportsSound` | bool | a sound server can run against this device. The attribute has its own domain, so a selector reads it as `device.attributes["sound.liken.sh"].supportsSound`. The domain belongs to no single driver, so a `DeviceClass` that selects the attribute names no driver, and any driver can set it on a device that supports a sound server |
| `resource.kubernetes.io/pciBusID` | string | the address of a PCI device, the same value as `address`, on every device that `liken` publishes for a PCI device. Kubernetes defines the attribute and its format, Domain:Bus:Device.Function, in the [standard device attributes](https://kubernetes.io/docs/reference/node/dra-standard-device-attributes/). The domain belongs to no single driver, so another driver can publish the same attribute for the same card, and a `matchAttribute` constraint can pair a device of `liken` with a device of that driver. A USB device has no such attribute |

Select on the attribute that describes what you need. `renderNode` and
`classCode` describe a capability of the hardware, so a selector that
uses them works across a fleet of different machines. `vendor` and
`product` identify one model, so a `DeviceClass` that uses them stops
matching on the next model that you buy.

`address` is the attribute that pairs devices. Two cards in one machine
have different addresses, and every device that `liken` publishes for
one card has that card's address. A claim with a request for a render
node and a request for a card node can constrain the two with
`matchAttribute: liken.sh/address`, and both requests then get devices
of the same card. The
[guide](/docs/guides/devices/#two-requests-one-card) has the claim.

`liken.sh/address` pairs only devices that `liken` publishes, because a
bare attribute name belongs to the driver that published it. To pair a
device of `liken` with a device of another driver for the same card,
constrain the requests with
`matchAttribute: resource.kubernetes.io/pciBusID`.

`liken` does not publish capabilities that can only be measured with a
driver stack, such as the codecs that a GPU can encode. Measuring them
needs libva and a vendor driver, which the image does not contain. A
pod that holds a claim on the render node can measure them itself.
[`media-operator`](https://liken.sh/media/) does so and publishes the
results as devices of its own driver, `media.liken.sh`, which pair with
the render node through `resource.kubernetes.io/pciBusID`.

## Sharing

`liken` allocates a device to one claim at a time, unless it publishes
`allowMultipleAllocations` for that device. A device's sysfs subtree can
contain nodes of more than one kernel subsystem. When it does, `liken`
can publish more than one device for it, one for each kind of node. A
GPU is split further, because its render node and its card node give
different authority over the same silicon. The name of each extra
device is the primary device's name plus a suffix. A claim receives the
nodes of the published device that it was allocated, and no others.

`liken` publishes `allowMultipleAllocations` for the graphics device of
a GPU. That device provides the DRM render node, `/dev/dri/renderD*`,
and has `renderNode: true`. The kernel driver arbitrates between
concurrent clients of a render node, so more than one claim can hold
it.

An audio controller is a device whose nodes are all ALSA nodes of a
sound card. It has `subsystem: sound` and
`sound.liken.sh/supportsSound: true`, and it is exclusive. In practice
one sound server owns every PCM of a card and mixes its clients'
streams through them, so the card belongs to one claim. A second claim
for the card stays pending in the scheduler, which is easier to
diagnose than a second program that fails with ALSA's `EBUSY` when it
starts to play.

A claim on an audio controller provides the card's whole subtree, which
includes more than the `/dev/snd` nodes. ALSA registers an input device
for each jack that it can sense, and an HDA controller with HDMI outputs
has one for each display pin, so the claim also provides those
`/dev/input/event*` nodes. A jack reports the state of an output that
the same claim plays through, so it is not a separate device.

A GPU publishes its card node, `/dev/dri/card*`, as a separate device
named with the primary name plus `-display`, with `displayNode: true`.
This device is exclusive, because the kernel allows one DRM master for
each card: it gives modesetting to one open card node, and a second
display program on the same card fails when it starts. Because the
device is exclusive, a second claim stays pending in the scheduler.
A workload that modesets and also renders claims both devices, with one
request for each. On a machine with two cards, a constraint of
`matchAttribute: liken.sh/address` on those two requests keeps both on
one card.

A GPU driver also registers i2c monitor-control buses. Each publishes
as its own exclusive device, with `subsystem: i2c-dev`, because an i2c
node passes raw transfers to every device on its wire, and the kernel
does not arbitrate between two writers on one wire. A DisplayPort
output also registers a DisplayPort AUX channel, with
`subsystem: drm_dp_aux_dev`. It publishes the same way, as its own
exclusive device, and it exists only while a display is connected. The
legacy framebuffer node is never provided, because a claim on it would
let a workload take over the display, and no workload needs a bare
framebuffer.

A USB-CEC adapter that a `spec.serio` entry attaches publishes two
devices, both exclusive: the CEC bus, with `subsystem: cec`, and the
TV remote's input device, with the suffix `-input` and
`subsystem: input`. [Serial-line adapters](#serial-line-adapters) gives
the reasons.

The virtualization devices publish `allowMultipleAllocations`.
Each open of `/dev/kvm` creates a separate virtual machine, and each
open of a vhost node creates a separate vhost worker, so what one claim
does through its open file cannot reach another claim. The devices have
no capacity. A guest's memory is the memory of its monitor's process, so
the pod's own resource requests limit how many guests a node runs.

A device with one kind of node publishes as one device, unless it is a
GPU. When a device's nodes are a mix that `liken` has no name for,
`liken` publishes the whole device as exclusive and omits `subsystem`. A
Bluetooth adapter is also published whole and exclusive with no
`subsystem`, because the only node it provides is its usbfs node.

A serial port and a dongle's control endpoint have no arbitration like
a render node's, so they are exclusive.

Only the driver can set `allowMultipleAllocations`, because only the
driver writes a `ResourceSlice`. A wrong value has a cost either way. When a device is
wrongly marked as shareable, two workloads use the same hardware while
each assumes that it is the only user, and no `DeviceClass`, claim, or
workload can prevent that. When a device is wrongly marked as
exclusive, a second claim stays pending in the scheduler, where
Kubernetes reports it.

### Sharing a claim is not the same thing

`allowMultipleAllocations` controls how many **claims** can allocate a
device. It does not control how many **pods** can use one claim.

Every pod that names a `ResourceClaim` shares that claim, and all of
these pods receive the device. Kubernetes does this on purpose, as the
way for two pods to share one allocation. `liken` does not refuse the
second pod, because a check for it would race with pod creation and no
component would own it. A device node in a container never made access
exclusive anyway. Only the kernel does that, through `O_EXCL` and the
driver's own open path.

To give a device to one pod only, use a `ResourceClaimTemplate`, which
creates a separate claim for each pod. When you use one `ResourceClaim`
for more than one pod, you share the device on purpose.

## What a claim provides

A claim provides device nodes only. The node writes a CDI specification
that lists the `/dev` entries for that claim, and the container runtime
adds them to the containers that requested the claim. The claim grants
no privilege, mounts no host path, and loads no kernel module for the
pod.

The workload supplies everything else that it needs: the userspace
library that talks to the device, and the group membership that its
image gives its user.

### USB devices

A claim on a USB device also provides that device's usbfs node,
`/dev/bus/usb/<busnum>/<devnum>`, with three digits in each number. A
program that uses libusb, such as Network UPS Tools, reads sysfs to
find the hardware and then opens this node to talk to it. A node that a
kernel driver registers, such as `hidraw`, supports only that driver's
protocol, so it cannot replace the usbfs node. The devices of an
attached serial line do not get the usbfs node, for the reason in
[Serial-line adapters](#serial-line-adapters).

The `busNumber` and `deviceNumber` attributes give the two numbers of
the path. Do not select on them in a claim. Both change each time the
device enumerates, so a claim that selects them stops matching after a
replug.

A program that uses libusb cannot share an interface with a kernel
driver, so it detaches the kernel driver while it runs. The kernel then
shows `usbfs` as the interface's driver. `liken` does not publish an
interface that a program holds, so the device leaves the node's slice
for as long as the pod runs. When the pod stops and its claim ends, the
node binds a kernel driver to the interface again, and the device
returns to the slice at the next reconcile pass.

The kernel assigns the device a new device number at each enumeration,
so the usbfs node's path changes when you unplug the device and plug it
in again. Each reconcile pass writes the current node into the
specification of every claim that the kubelet prepared, so the next pod
receives the current node. A container that already runs keeps the node
that it received, and it receives the current node when the pod
restarts.

While the hardware is unplugged, the pass writes a node that does not
exist into the claim's specification instead. The old node would be
unsafe, because the kernel assigns its name to the next device that
arrives: another USB disk becomes `/dev/sda`. A container that starts
while the hardware is away fails to start. The unplug also taints the
device, and the taint evicts the claim's pods, as
[An unplugged device evicts its pods](#an-unplugged-device-evicts-its-pods)
describes. When the hardware returns to the same port, the next pass
writes its nodes back, so a pod that tolerates the taint starts its
next container with them.

usbfs has no interface boundary. When a device has more than one
interface with a driver, each interface publishes as its own device,
and each one provides the same usbfs node. A pod that holds one of
these claims can talk to the whole device.

### USB devices with no kernel driver

Some USB devices have no kernel driver, because the vendor's driver is a
program that uses libusb. ZWO's astronomy cameras, smart card readers
that `pcscd` serves, and many software-defined radios are of this kind.
When no interface of a USB device has a driver, the device publishes
whole, named for its port path, such as `usb-1-2`. A claim on it
provides the device's usbfs node and nothing else. The `class` and
`classCode` attributes come from the device's first interface, so a
`DeviceClass` can select a smart card reader with `classCode == "0b"`.
The device is exclusive.

A device with a driver on any interface does not publish whole. Each
driven interface publishes as its own device with the usbfs node,
because a claim on the whole device would give the same hardware to a
second workload. A device leaves the slice while a program holds one of
its interfaces, because the kernel then shows `usbfs` as that
interface's driver, and it returns when the program releases the
interface.

A device whose kernel module is not loaded yet also publishes whole,
until the module binds an interface. Then the driven interface
publishes in its place.

### Bluetooth adapters

A claim on a Bluetooth adapter provides the adapter's usbfs node,
`/dev/uinput`, the 32 legacy input event nodes, and, on a machine whose
`spec.modules` declares `uhid`, the `/dev/uhid` node. The adapter has no
node of its own. A program reaches a radio through an `AF_BLUETOOTH`
socket, which it binds to an adapter by index, and the kernel registers
no `/dev` entry for the adapter. The claim therefore records which
workload owns the radio, and it keeps that workload on the machine that
has the radio. A stack that drives the radio through the kernel opens
its socket with the capabilities of its own container, because a claim
grants no privilege.

`/dev/uhid` belongs to the adapter's claim because it is how a HID
stack in userspace creates input devices in the kernel. BlueZ sends HID
over GATT through it, and writes to this node to present a BLE
peripheral as an input device. Classic Bluetooth HID passes through
`hidp` inside the kernel and needs no node. Declare the `uhid` module on
a machine whose adapter serves BLE input devices. Without it, such a
device pairs and connects, but no input node appears, and `bluetoothd`
logs `input-hog profile accept failed`.

When `liken` collects the nodes of an adapter, it does not descend into
the `bluetooth` directory of the adapter's sysfs subtree. The kernel puts the HID device of a connected peripheral under the
adapter's own USB interface, so a game controller's `/dev/hidraw*` node
appears in the adapter's part of sysfs. That node belongs to the
controller, and a claim on the adapter does not provide it. The node
also moves when BlueZ changes how it drives the kernel. With `/dev/uhid`
present, BlueZ 5.73 and later create the HID device under
`/sys/devices/virtual/misc/uhid`, where nothing connects it to the
adapter.

The input event nodes are provided by number instead. The kernel fixes
the numbers of the first 32 event devices: character major 13, minors 64
to 95, at `/dev/input/event0` to `/dev/input/event31`. A node in that
range exists only while a device is registered at that minor, and a
peripheral that connects over the air registers one after the claim was
prepared. So the claim lists each of the 32 nodes by its major and
minor numbers. The container runtime creates them with `mknod` and
allows them in the device cgroup, and a program in the container can
open a controller's node as soon as the kernel registers it. The
Bluetooth stack uses these nodes together with `/dev/uinput`, the
kernel's interface for creating a virtual input device. It relays a
controller's events from the real node, which comes and goes with the
radio link, into a virtual device whose node stays. `uinput` is built
into the `liken` kernel, so every machine has `/dev/uinput`. A machine
can have at most 32 input devices provided this way.

### Serial-line adapters

Some USB devices present a serial line, and their kernel driver binds
only after a program attaches the line to the kernel's serio layer and
keeps it attached. A USB-CEC adapter is one: `cdc_acm` creates
`/dev/ttyACM0`, and the adapter's driver, `pulse8_cec` or
`rainshadow_cec`, binds to a serio port that exists only while the
attachment lasts. The kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
describes the setup on a general-purpose distribution. On `liken`, a
[`spec.serio`](/docs/reference/machine/#spec--serio) entry declares the
attachment, and the machine keeps it attached until shutdown.
[Load the drivers for a machine's hardware](/docs/guides/hardware-modules/#attach-a-usb-cec-adapter)
has the steps.

The attached driver creates a CEC adapter, `/dev/cec0`, and the CEC core
registers an input device for the TV remote's keys, with an event node
under `/dev/input`. `liken` publishes the adapter's serial-line
interface as two devices:

* **The CEC bus**, with the suffix `-cec` and `subsystem: cec`. A claim
  on it provides the `/dev/cec*` node. It is exclusive. The CEC core
  lets several processes open one adapter, but only one can be the
  exclusive initiator or follower, and two programs that configure
  logical addresses on one adapter undo each other's work.
* **The TV remote**, with the suffix `-input` and `subsystem: input`. A
  claim on it provides the event node. It is exclusive, because one
  reader must own the remote's key presses.

The two devices serve different claims. A program that talks on the bus
claims the first, and a program that reads the remote claims the second.
Both have the adapter's attributes, so one claim can pair them with
`matchAttribute: liken.sh/address`. The remote-control core registers no
LIRC node for a device whose only protocol is CEC, so a claim provides
none.

The tty is never published, before or after the attachment. A pod that
received it could set another line discipline or write to the line, and
either one ends the attachment for every other claim. So before the
attachment exists, the adapter's serial line publishes nothing.

Without a `spec.serio` entry, the adapter is an ordinary serial device,
published under the interface's name with no suffix, and a claim on it
receives the tty and the usbfs node. When you add the entry, the node
rewrites that claim's CDI specification to list a node that does not
exist, so its container cannot start again. The claim never receives
the CEC node, because neither attached device uses the name with no
suffix. Delete the claim, and claim the `-cec` or `-input` device
instead.

The usbfs node is not provided either, because through it a program
could detach `cdc_acm` or reset the adapter and end the attachment the
same way. The adapter's other interfaces publish their own devices
without the usbfs node. For example, the Pulse-Eight's HID interface
still publishes its `hidraw` and input nodes.

An unplug taints both devices, and the taint evicts the pods that hold
them, as
[An unplugged device evicts its pods](#an-unplugged-device-evicts-its-pods)
describes. A pod that tolerates the taint keeps file descriptors for
nodes that are gone. A program that holds one of these claims must exit
when a read or an `ioctl` returns `ENODEV`. The kubelet then restarts
the container, and the new container receives the nodes that the
claim's CDI specification lists at that moment.

While a device is absent, its claim's specification lists a node that
does not exist, so a container cannot start. The old nodes would be
unsafe: an event node has a fixed device number, and another input
device can take that number while the adapter is gone. The kubelet
retries the container with its restart backoff, which doubles from 10
seconds to 5 minutes. The node rewrites every claim's specification on
each pass, so when the adapter returns to the same USB port, the devices
return with the same names, and the container starts at its next retry
with no new allocation. After a long absence, that retry can come up to
5 minutes after the adapter returns. A published name follows the USB
port, so an adapter that you move to another port is a different device.
When a pod owns the claim, the eviction causes the pod's controller to
create a new pod, and the new pod's claim is allocated the adapter in
its new port.

### Virtualization devices

A program that boots a virtual machine, such as QEMU, Firecracker, or
Cloud Hypervisor, opens `/dev/kvm`. QEMU also opens `/dev/vhost-net` to
move a tap device's packets inside the kernel, and `/dev/vhost-vsock` to
give a guest a vsock address. The kernel registers these nodes as misc
devices, which belong to no bus, and the node publishes each one as a
device of its own when it exists:

| Device | Node | Module to declare in `spec.modules` |
|---|---|---|
| `misc-kvm` | `/dev/kvm` | `kvm_intel` or `kvm_amd` |
| `misc-vhost-net` | `/dev/vhost-net` | `vhost_net` |
| `misc-vhost-vsock` | `/dev/vhost-vsock` | `vhost_vsock` |

The kernel registers `/dev/kvm` only after the vendor module loads, and
the vendor module loads only when the CPU has VT-x or AMD-V and the
firmware has turned it on. So `misc-kvm` in the slice means that virtual
machines work on that node. When the firmware has virtualization turned
off, the Machine's `status.modules` reports the module as `Failed`.

The node publishes no other misc device. Some of them give whatever
opens them control of the machine itself: `loop-control` and
`device-mapper` create block devices on the host. `/dev/net/tun` needs
no claim, because the container runtime's default device rules already
admit it to every container. A pod that needs a tap device creates the
node with `mknod /dev/net/tun c 10 200`, which the default capabilities
allow, and adds the `NET_ADMIN` capability to create the interface in
its own network namespace.

A claim provides each node with mode `0666`, so a monitor that runs as
a user other than root can open it. This is the mode that systemd's
default udev rules give these three nodes. The node on the machine
itself keeps mode `0600`, and the container runtime refuses it to every
container that has no claim. A pod with `hostUsers: false` runs in a
user namespace, where the runtime bind-mounts the machine's own node in
place of a new one, so such a pod cannot open these nodes.

A guest's vsock address, its CID, must be unique in its network
namespace. `liken` sets `net.vsock.child_ns_mode` to `local` when the
`vsock` module is loaded, so each pod's network namespace has its own
addresses. Two pods can each start a guest at CID 3, and no pod can
reach another pod's guests. A pod with `hostNetwork: true` shares the
machine's own namespace, which contains no pod's guests. The node
publishes `misc-vhost-vsock` only while the parameter is `local`, so a
Machine whose `spec.sysctls` sets it to `global` publishes no
`misc-vhost-vsock`. The kernel accepts one value for the parameter and
refuses a different one afterward, so a change to it in `spec.sysctls`
takes effect at the next boot. Until then, the Machine's
`SysctlsApplied` condition reports the refused write.

A pod that connects to its guest opens a socket of the family
`AF_VSOCK`. In containerd 2.3.7, the version that k3s v1.37.2 ships,
the default seccomp profile allows only the socket families that it
lists, and `AF_VSOCK` is not one of them. So a pod whose
`securityContext` sets `seccompProfile.type` to `RuntimeDefault` cannot
open that socket. A pod that sets no profile runs with none, and can
open it. A pod that needs both a profile and a vsock socket supplies its
own profile with the type `Localhost`. QEMU's use of `/dev/vhost-vsock`
opens no such socket, so the claim itself needs no change.

## An unplugged device evicts its pods

Kubernetes allocates a claim once, and does not allocate it again while
a pod holds it. A published name follows the USB port, so a claim on a
device that moves to another port, or to another machine, still names
the old port. So when a device that a prepared claim holds is unplugged,
and a pod owns the claim, the node taints the device
`liken.sh/unplugged` with the effect `NoExecute`. Kubernetes evicts the
pod and deletes its claim with it. The pod's controller, such as a
`Deployment` or a `DaemonSet`, creates a new pod with a new claim, and
the scheduler allocates that claim to a matching device on any machine.

A pod owns the claim that a `ResourceClaimTemplate` creates for it, and
the claim that an operator creates for each pod. A named `ResourceClaim`
that you create yourself does not get the taint. It outlives its pods,
so the controller's next pod would reserve it while the evicted pod
still held it, Kubernetes would never allocate it again, and the taint
would evict each new pod in turn. While its device is gone, a named
claim's container fails to start because its node does not exist. To
repair it, delete the pods and the claim, and create the claim again.
Use a template to have the repair happen on its own, as
[the devices guide](/docs/guides/devices/#5-give-one-pod-a-device-alone)
shows.

The node treats a device as unplugged when its directory leaves
`/sys/bus`, or when another enumeration takes its address: another unit,
or the same unit with a new device number. A program that detaches a
kernel driver leaves the device in place, so that is not an unplug. A
device that comes back to its port keeps the taint until the kubelet
unprepares the claim, because a container that kept running still has
the nodes of the old enumeration. While the device is gone, the slice
lists its name with the taint and no attributes, so no new claim matches
it.

The node posts a `DeviceUnplugged` `Event` on its `Machine` when it
taints a device. The message names the device and the claim. When the
kubelet unprepares the claim, the node publishes its slice again without
the taint.

A pod that no controller owns is evicted, and nothing creates it again.

A program that follows its hardware by itself can tolerate the taint in
its claim's request and keep running:

    exactly:
      deviceClassName: sound-card
      allocationMode: All
      tolerations:
        - key: liken.sh/unplugged
          operator: Exists
          effect: NoExecute

`audio-operator` tolerates it, because PipeWire serves every sound card
of a node, and `firmware-operator`'s upload tolerates it, because the
upload makes the chip enumerate again. A toleration with
`tolerationSeconds` delays the eviction by that many seconds.

A claim that the kubelet prepares again after a reboot is a different
case. The kubelet can prepare a claim before its USB device has
enumerated, so a device that is missing then is not an unplug. Prepare
fails, the kubelet retries it, and the pod waits in `ContainerCreating`
until the device appears. When the device moved to another port while
the machine was off, delete the pod.

## Limits

* A node publishes one slice, with at most 128 devices. When a node has
  more devices, it logs the device count and drops the overflow, and no
  claim can reach the dropped devices. It does not split the devices
  into more than one pool.
* `liken` supplies no `DeviceClass`. A `DeviceClass` states what one
  deployment needs, so each deployment writes its own. The
  [guide](/docs/guides/devices/) has examples to start from.
* Devices publish no capacity, so a claim cannot request part of a
  device.
* When a device fails, it disappears from the slice at the next pass, which stops new allocations. When a prepared
  claim that a pod owns holds the device, the device takes the
  `liken.sh/unplugged` taint, which evicts the pod
  ([An unplugged device evicts its pods](#an-unplugged-device-evicts-its-pods)).
* The node keeps no state for a claim across a reboot. The claim is a
  Kubernetes object, so it stays allocated through the reboot, and the
  kubelet prepares it again when its pod starts on the machine.

## Hardware that is not published

You cannot claim hardware that has no driver bound to it, except a whole
USB device with no driver on any interface. `liken` shows this hardware
in two other places:

* The Machine's status lists it as unclaimed hardware, with the modules
  that can drive it.
* The [hardware report](/docs/guides/install/#first-run-the-hardware-report)
  lists it before the first install, as commented lines under
  `spec.modules`.

To publish the device, declare the module in `spec.modules`. The device
appears in the node's slice at the next reconcile pass.
