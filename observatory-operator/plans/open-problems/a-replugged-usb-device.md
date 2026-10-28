# A replugged USB device

Open problem. When a USB device is unplugged and plugged into the same
port, `liken` gives its claim the device's new node, but the device's
pod keeps the old one until its container restarts, and
`observatory-operator` does not restart it. The tty can also come back
under another name, such as `/dev/ttyUSB1` in place of `/dev/ttyUSB0`,
and the operator has no field for a driver's port.

## What `liken` does

[Milestone 77](../../../liken/plans/completed/77-the-machine-operator-hears-the-machine.md)
built the node side. `refreshCDISpecs` in `machine-operator/cdi.go`
writes a replugged device's new node into the CDI spec of each claim
the kubelet prepared. In its drill, after an unplug and a plug into the
same port, the spec named the new node within two seconds while the
pod ran, and the next pod on the claim received it. While the port is
empty, the spec names `/dev/liken.sh/device-absent`, so a container
that starts then fails instead of opening another device's node.

A container's device nodes are set when it starts, so the running
container does not receive the new node.

[Milestone 89](../../../liken/plans/completed/89-an-unplugged-device-evicts-its-pod.md)
taints an unplugged device so that Kubernetes evicts its pod, but only
for a claim that a pod owns. A device's claim here is the named claim
that `spec.claim` names, which outlives its pods, so the taint does not
reach it ([a named claim keeps an unplugged device](../../../liken/plans/open-problems/a-named-claim-keeps-an-unplugged-device.md)).

## What `observatory-operator` lacks

- **A restart after a replug.** The operator does not watch the
  `ResourceSlice` or the claim, so nothing restarts a device's pod when
  its device returns. Whether an INDI driver exits when it loses its
  port was not checked. A driver that exits restarts its container,
  and the container receives the new node. A driver that keeps running
  holds the old node until someone deletes the pod. A claim that the
  operator made for each pod would take the eviction of milestone 89.
- **A field for the driver's port.** The pod receives a tty at the
  path it has on the machine. A serial driver opens the port that its
  `DEVICE_PORT` property names. Many drivers search the other ports
  when that open fails, but a driver with no search does not find its
  device at the new path. The operator has no field that sets
  `DEVICE_PORT`, so today a person sets it from KStars, as the
  `connect-usb-equipment` skill describes.

This was not tested on hardware. Milestone 77 lists a replug of a USB
serial adapter under a running claim as verification it still needs.
