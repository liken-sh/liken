# 89. An unplugged device evicts its pod

Milestone 89. Proposed 2026-10-28. Not built. When someone unplugs a
USB device that a claim holds, the pod that holds the claim stays in
`CrashLoopBackOff` until a person deletes it, even after the device is
plugged into another port or another machine. This milestone taints
such a device with a `NoExecute` taint. Kubernetes then evicts the
pod, the claim's allocation clears, and the replacement pod receives a
new allocation by the claim's selector, wherever the device is now.
It replaces [milestone 83](rejected/83-a-usb-device-is-named-for-its-unit.md),
which renamed USB devices for their unit and covered only a move on the
same machine.

## The failure

`machine-operator` names a USB device by its port (`deviceName` in
[`dra.go`](../machine-operator/dra.go)), so a UPS on interface
`1-4:1.0` is `usb-1-4-1-0`. A claim selects it by its `serial`. The
scheduler writes the name into the claim's allocation once and does
not allocate the claim again while a pod reserves it
(`pkg/scheduler/framework/plugins/dynamicresources/dynamicresources.go`
in Kubernetes 1.37.1).

When someone moves the cable to another port, the slice publishes the
device as `usb-1-1-1-0`, and the allocation still names
`usb-1-4-1-0`. The refresh writes `/dev/liken.sh/device-absent` into
the claim's CDI spec (`refreshCDISpec` in
[`cdi.go`](../machine-operator/cdi.go)), so the container fails to
start. The kubelet prepares a claim only when it creates a pod sandbox,
not when it restarts a container, so the pod never receives the device.

The upstream repair is to end the pod. When a claim's last pod is gone,
`kube-controller-manager` clears the claim's allocation
(`pkg/controller/resourceclaim/controller.go`, the `len(remaining) ==
0` branch), and the next pod that names the claim is allocated again
from the current slices. A `Deployment` or a `DaemonSet` creates that
next pod. Today a person must delete the pod by hand.

## The design

While a claim that the kubelet prepared on this machine holds a device
that discovery no longer finds, `machine-operator` publishes the
device in its slice with a `NoExecute` taint, `liken.sh/unplugged`.
Device taints are GA and on by default in Kubernetes 1.37
(`DRADeviceTaints`), and the taint-eviction controller deletes each pod
whose claim holds a device with an untolerated `NoExecute` taint.

The eviction is the repair:

1. Someone unplugs the device. The uevent wakes the pass, and the pass
   publishes the device's name with the taint and writes
   `device-absent` into the claim's spec, as it does today.
2. The taint-eviction controller deletes the pod.
3. `kube-controller-manager` clears the claim's allocation.
4. The pod's controller creates a new pod. The scheduler allocates the
   claim to a device that matches its selector, on any machine.
5. The kubelet unprepares the old claim, and the taint leaves the
   slice with it.

A device that a pod uses through libusb, such as a UPS under Network
UPS Tools, is absent from the slice while the program holds it,
because its interface is bound to `usbfs` and delivers no node. It is
still present in sysfs, so discovery finds it and nothing is tainted.
The rule reads discovery, not the slice.

`bluetooth-operator` already repairs a disconnected controller this
way (`disconnectedTaint` in `bluetooth-operator/devices.go`).

### The taint stays until unprepare

A device that returns to the same port keeps the taint, and the claim's
spec keeps `device-absent`, until the kubelet unprepares the claim.
Two failures require this:

- A container that kept running holds the device nodes of the old
  enumeration. A USB serial adapter can come back as `/dev/ttyUSB1` in
  place of `/dev/ttyUSB0`. Only a new container receives the new
  nodes, and the eviction makes one. This is the restart that
  [a replugged USB device](../../observatory-operator/plans/open-problems/a-replugged-usb-device.md)
  in `observatory-operator` asks for.
- A port name belongs to the port, so a different unit in the old port
  takes the old name. Today the refresh writes that unit's nodes into
  the claim's spec, and the claim receives a device that its selector
  may not match. A claim whose device has gone receives no device
  again.

A reseat in the same port therefore costs one pod restart. A consumer
that prefers to wait can tolerate `liken.sh/unplugged` with
`tolerationSeconds` in its claim's request. The toleration delays the
eviction and does not prevent it.

### What the pass reads

The tainted entry carries the device's name and no attributes, so no
`DeviceClass` selector and no new claim matches it. `NoExecute` also
keeps the scheduler from allocating it.

The pass must know which allocated devices are gone after a restart
of `machine-operator`, so the answer comes from the CDI spec files and
not from memory. Each file is one claim that the kubelet prepared on
this machine (`cdi.go`), and the files are on tmpfs, so they last one
boot. The first time the refresh finds a claim's device gone, it writes
an annotation, `liken.sh/unplugged`, into that claim's file. Each pass
taints every device name that an annotated file names. The record
needs no new watch and no new RBAC.

After a reboot the kubelet prepares each claim again. Today a prepare
whose device is gone fails, and the kubelet retries it, so the pod
stays in `ContainerCreating` and never reaches the eviction. So a
prepare whose device is gone writes the claim's file with
`device-absent` and the annotation, and returns success. The container
then fails to start on `device-absent`, the next pass taints the
device, and the pod is evicted.

The operator posts an `Event` on its `Machine` when it taints a device
and when the taint leaves, under root plan 78, with the claim's
namespace and name.

## What this does not repair

- A bare pod with no controller is evicted and nothing creates it
  again. The person who made it creates it again.
- A claim that selects a port with `address` follows the port, as it
  asks. When the device comes back to that port, the new pod receives
  it.
- Two units with the same vendor, product, and serial on one machine
  are told apart only by port, so a claim on one of them must select
  `address`.

## Choices

- **Eviction instead of a rename.** Milestone 83 named a USB device by
  its vendor, product, and serial, so that an allocation followed the
  unit to another port on the same machine with no pod restart. It
  needed a resolution of old port names during the upgrade, it broke
  every claim allocated after the upgrade on a rollback, it renamed a
  unit when a second identical unit arrived, and it did nothing for a
  move to another machine. The eviction costs one pod restart and has
  none of those failures.
- **A taint in the slice instead of a `DeviceTaintRule`.** A
  `DeviceTaintRule` is a cluster-scoped object that the operator would
  have to create and delete, and it would need its own RBAC. A taint in
  the operator's own slice is written by the pass that already writes
  the slice, and it leaves with the device.
- **No grace period in the operator.** A timer in `machine-operator`
  would hold state across passes. The consumer's `tolerationSeconds`
  is the upstream setting for the same delay, and it belongs to the
  workload that knows how long it can wait.

## Questions for the build

- What `firmware-operator`'s upload does when a chip enumerates again
  with its loaded product ID, if the upload pod holds a claim on the
  bare chip. The bare chip's device goes away while the claim holds it.
  The upload pod should finish before the taint evicts it.
- Whether a device's interfaces each carry the taint, or only the
  interface that the claim names. The claim names one device, so only
  that device needs it.

## The manual

The commit that builds this updates:

- the USB sections of the
  [device reference](../docs/content/docs/reference/devices.md), which
  now say that a returning device's nodes reach the next container
  start, with the eviction and the toleration;
- the `troubleshoot` skill and guide, with the `Event` and the taint;
- the devices guide and skill, with a claim that tolerates
  `liken.sh/unplugged`;
- `observatory-operator`'s `connect-usb-equipment` guide and skill,
  with the restart after a replug.

## Drills

On liken-1, with spare USB dongles, each drill starts with a
`Deployment` whose pod holds a named `ResourceClaim` that selects the
dongle by `serial`:

1. Move the dongle to another port. The pod is evicted, and the new
   pod runs with the dongle.
2. Move the dongle to another machine. The new pod runs on that
   machine.
3. Unplug the dongle and plug it into the same port. The pod is
   evicted once, and the new pod receives the current nodes.
4. Unplug the dongle and do not return it. The new pod stays
   `Pending`, and the `Machine` shows the `Event`.
5. Run a pod that holds a dongle through libusb. Nothing is tainted
   while it runs.
6. Reboot a machine whose claim's dongle was moved while it was off.
   The pod is evicted and allocated again.
