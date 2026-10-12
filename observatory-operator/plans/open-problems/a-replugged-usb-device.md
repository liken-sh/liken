# A replugged USB device

Open problem. When a USB device is unplugged and plugged into the same
port, `liken` gives its claim the device's new node, but the device's
pod keeps the old one until its container restarts, and
`observatory-operator` does not restart it.

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

A device moved to a different port is a separate failure: the claim's
allocation stays on the old port.
[Milestone 83](../../../liken/plans/83-a-usb-device-is-named-for-its-unit.md)
proposes to name a USB device for its unit instead, and is not built.

## What `observatory-operator` lacks

- **A restart after a replug.** The operator does not watch the
  `ResourceSlice` or the claim, so nothing restarts a device's pod when
  its device returns. Whether an INDI driver exits when it loses its
  port was not checked. A driver that exits restarts its container,
  and the container receives the new node. A driver that keeps running
  holds the old node until someone deletes the pod.
- **A field for the driver's port.** The pod receives a tty at the
  path it has on the machine, such as `/dev/ttyUSB1` in place of
  `/dev/ttyUSB0` after a replug. A serial driver opens the port that
  its `DEVICE_PORT` property names. Many drivers search the other ports
  when that open fails, but a driver with no search does not find its
  device at the new path. The operator has no field that sets
  `DEVICE_PORT`, so today a person sets it from KStars, as the
  `connect-usb-equipment` skill describes.

Neither part was tested on hardware. Milestone 77 lists a replug of a
USB serial adapter under a running claim as verification it still
needs.
