# 89. An unplugged device evicts its pod

Milestone 89. Proposed and built on 2026-10-28. The first drill on
liken-1 found that a named claim loops, so the taint reaches only a
claim that a pod owns, and
[a named claim keeps an unplugged device](../open-problems/a-named-claim-keeps-an-unplugged-device.md)
holds the rest. The other drills have not run. When someone unplugs a
USB device that a claim holds, the pod that holds the claim stays in
`CrashLoopBackOff` until a person deletes it, even after the device is
plugged into another port or another machine. This milestone taints
such a device with a `NoExecute` taint. Kubernetes then evicts the
pod, the claim's allocation clears, and the replacement pod receives a
new allocation by the claim's selector, wherever the device is now.
It replaces [milestone 83](../rejected/83-a-usb-device-is-named-for-its-unit.md),
which renamed USB devices for their unit and covered only a move on the
same machine.

## The failure

`machine-operator` names a USB device by its port (`deviceName` in
[`dra.go`](../../machine-operator/dra.go)), so a UPS on interface
`1-4:1.0` is `usb-1-4-1-0`. A claim selects it by its `serial`. The
scheduler writes the name into the claim's allocation once and does
not allocate the claim again while a pod reserves it
(`pkg/scheduler/framework/plugins/dynamicresources/dynamicresources.go`
in Kubernetes 1.37.1).

When someone moves the cable to another port, the slice publishes the
device as `usb-1-1-1-0`, and the allocation still names
`usb-1-4-1-0`. The refresh writes `/dev/liken.sh/device-absent` into
the claim's CDI spec (`refreshCDISpec` in
[`cdi.go`](../../machine-operator/cdi.go)), so the container fails to
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
   marks the claim's CDI spec, writes `device-absent` into it as it did
   before, and publishes the device's name with the taint.
2. The taint-eviction controller deletes the pod, and the garbage
   collector deletes the claim that the pod owns.
3. The pod's controller creates a new pod with a new claim. The
   scheduler allocates the claim to a device that matches its
   selector, on any machine.
4. The kubelet unprepares the old claim, and the unprepare wakes the
   pass that drops the taint from the slice.

A device that a pod uses through libusb, such as a UPS under Network
UPS Tools, is absent from the slice while the program holds it,
because its interface is bound to `usbfs` and delivers no node. It is
still present in sysfs, so discovery finds it and nothing is tainted.
The rule reads discovery, not the slice.

`bluetooth-operator` already repairs a disconnected controller this
way (`disconnectedTaint` in `bluetooth-operator/devices.go`).

### When a device has left

prepare writes, beside each allocated device's nodes in the claim's CDI
spec, the unit it resolved: its bus and sysfs address, its vendor,
product, and serial, and its bus and device numbers. Only a USB or a
PCI device gets the record, because only those have a directory under
`/sys/bus` that leaves with the hardware.

The refresh marks a device as unplugged when its directory is gone, or
when the device at that address reports another vendor, product,
serial, or device number. A new device number in the same port is a
new enumeration, and a container that kept running holds the nodes of
the old one. A value that the walk could not read counts as unchanged,
because a walk that misses one attribute must not evict a pod. A spec
that an older operator wrote has no record and is never marked; the
kubelet prepares it again at the next boot.

The mark stays until the kubelet unprepares the claim, even when the
device comes back to its port, because a container that kept running
still holds the old nodes. This is the restart that
[a replugged USB device](../../../observatory-operator/plans/open-problems/a-replugged-usb-device.md)
in `observatory-operator` asked for. The refresh still writes the
device's current nodes into the spec, exactly as it did before, so a
pod that tolerates the taint receives them at its next container
start.

### What the pass reads

The tainted entry of a device that is gone carries the device's name
and no attributes, so no `DeviceClass` selector and no new claim
matches it. A device that came back keeps its attributes and takes the
taint. `NoExecute` also keeps the scheduler from allocating either.

The record is in the CDI spec files, not in memory, so it survives a
restart of `machine-operator`. Each file is one claim that the kubelet
prepared on this machine (`cdi.go`), and the files are on tmpfs, so
they last one boot. The mark is the annotation `liken.sh/unplugged` on
the CDI device. The record needs no new watch and no new RBAC. The
refresh runs before the slice is published, so the pass that marks a
claim taints its device.

The operator posts one `DeviceUnplugged` `Event` on its `Machine` when
it marks a claim, under root plan 78, with the device's name and the
claim's namespace and name.

### Who tolerates the taint

Three workloads in this repository hold a USB device through
`liken.sh`, and their programs follow the hardware in different ways:

- `audio-operator`'s pod claims every sound card of its node with
  `allocationMode: All`. Without a toleration, an unplugged USB card
  would restart PipeWire, and a film that plays over HDMI on the same
  machine would lose its sound for the restart. Its claim tolerates the
  taint. A `ResourceClaimTemplate`'s spec cannot change, so the
  template takes the new name `every-sound-card`, and the install guide
  says how to delete the old `sound-card`.
- `firmware-operator`'s upload makes the bare chip enumerate again with
  its loaded IDs, which is an unplug by the rule above. The upload pod
  is a bare pod that the operator does not run again, and an eviction
  would record a failure for an upload that worked. Its claim
  tolerates the taint.
- `bluetooth-operator` does not tolerate it. A radio that enumerates
  again has already dropped every connection, and the new pod restores
  the bonds from their `Secret`s, so the restart costs a few seconds.

`equipment-operator`'s CEC pod, `observatory-operator`'s device pods,
and the GPU claims of `display-operator`, `media-operator`, and
`library-operator` take the eviction. The GPU claims hold PCI devices,
which do not leave while the machine runs.

### A claim prepared after a reboot

The kubelet can prepare a claim before its USB device has enumerated,
so a device that is missing at prepare is not an unplug. Prepare fails
as it did before, the kubelet retries it, and the pod waits in
`ContainerCreating` until the device appears. A device that moved
while the machine was off leaves its pod waiting, and a person deletes
the pod.

## What this does not repair

- A named `ResourceClaim`, which outlives its pods
  ([a named claim keeps an unplugged device](../open-problems/a-named-claim-keeps-an-unplugged-device.md)).
- A device that moved while its machine was off, as above.
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

## Changes during the build

- The spec keeps writing the device's current nodes after a return,
  instead of `device-absent` until unprepare. A survey of the
  workloads found that a tolerating pod would otherwise never start a
  container again after a replug.
- A prepare whose device is missing fails as before, instead of
  writing a marked spec. A device that has not enumerated yet at boot
  is missing too, and a marked spec would evict its pod at every
  reboot.
- The refresh decides an unplug from the unit record and the sysfs
  directory, instead of from the walk alone. A walk that cannot read
  one device would otherwise evict its pod.
- One `Event` marks the unplug. The taint leaves with the unprepare,
  which is the kubelet's action and posts no `Event` of its own.
- Only a claim that a pod owns gets the unit record, so only its
  device is tainted. On liken-1, a named claim on a moved Z-Wave dongle
  looped: each pod that the `ReplicaSet` created reserved the claim
  before the evicted pod released it, so the allocation never cleared,
  and the taint posted 1,630 evictions in three minutes. The open
  problem holds the evidence.
- An unprepare wakes the reconcile pass. The drill showed the taint
  staying in the slice after the claim ended, because no event woke
  the pass that drops it.
- The entry of a device that is gone keeps no attributes. Kubernetes
  1.37.1 evaluates a class's selectors before a device's taints, so a
  class that reads an attribute with no `has()` guard errors on that
  entry during the moment between the end of the allocation and the
  unprepare, and the scheduler tries again at the next slice write. An
  entry with attributes would instead match a claim that tolerates the
  taint with `allocationMode: All`, and that claim's pod could not
  prepare a device that is gone.

## The manual

The commit that builds this updates:

- the USB sections of the
  [device reference](../../docs/content/docs/reference/devices.md), which
  now say that a returning device's nodes reach the next container
  start, with the eviction and the toleration;
- the `troubleshoot` skill and guide, with the `Event` and the taint;
- the devices guide and skill, with a claim that tolerates
  `liken.sh/unplugged`;
- `observatory-operator`'s `connect-usb-equipment` guide and skill,
  with the restart after a replug;
- `audio-operator`'s install guide and skill, with the toleration and
  the renamed template.

## Drills

On liken-1, with spare USB dongles, each drill starts with a
`Deployment` whose pod holds a claim from a `ResourceClaimTemplate`
that selects the dongle by `serial`:

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
6. Unplug a USB sound card on a machine that plays over HDMI. The
   `audio-operator` pod keeps running, and the HDMI sound does not
   stop.
7. Load a QHY camera's firmware with `firmware-operator`. The upload
   pod succeeds, and the `Firmware` records the load.
