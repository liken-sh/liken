# A replugged USB device

Open problem. When a USB serial device is unplugged and plugged in
again, its tty can come back under another name, such as
`/dev/ttyUSB1` in place of `/dev/ttyUSB0`. An INDI driver that opens
only the port its `DEVICE_PORT` property names does not find its
device, and `observatory-operator` has no field that sets that
property.

## What `liken` does

[Milestone 77](../../../liken/plans/completed/77-the-machine-operator-hears-the-machine.md)
built the node side. `refreshCDISpecs` in `machine-operator/cdi.go`
writes a replugged device's new node into the CDI spec of each claim
the kubelet prepared. In its drill, after an unplug and a plug into the
same port, the spec named the new node within two seconds while the
pod ran, and the next pod on the claim received it. While the port is
empty, the spec names `/dev/liken.sh/device-absent`, so a container
that starts then fails instead of opening another device's node.

[Milestone 89](../../../liken/plans/completed/89-an-unplugged-device-evicts-its-pod.md)
gives the pod its restart. While a prepared claim's device is
unplugged, `machine-operator` taints the device `NoExecute`, so
Kubernetes evicts the device's pod, `observatory-operator` creates it
again, and the new pod's claim is allocated the device in whichever
port or machine it is in now. A driver that kept running with the old
node no longer holds it.

## What `observatory-operator` lacks

**A field for the driver's port.** The pod receives a tty at the path
it has on the machine. A serial driver opens the port that its
`DEVICE_PORT` property names. Many drivers search the other ports when
that open fails, but a driver with no search does not find its device
at the new path. The operator has no field that sets `DEVICE_PORT`, so
today a person sets it from KStars, as the `connect-usb-equipment`
skill describes.

This was not tested on hardware. Milestone 77 lists a replug of a USB
serial adapter under a running claim as verification it still needs.
