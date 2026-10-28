# A replugged USB device

Open problem. When a USB serial device is unplugged and plugged in
again, its tty can come back under another name, such as
`/dev/ttyUSB1` in place of `/dev/ttyUSB0`, and the operator has no
field for a driver's port. A driver that opens only the port that its
`DEVICE_PORT` property names does not find its device.

## What `liken` and the operator do

[Milestone 77](../../../liken/plans/completed/77-the-machine-operator-hears-the-machine.md)
built the node side. `refreshCDISpecs` in `machine-operator/cdi.go`
writes a replugged device's new node into the CDI spec of each claim
the kubelet prepared. A container's device nodes are set when it
starts, so a running container does not receive the new node.

[Milestone 89](../../../liken/plans/completed/89-an-unplugged-device-evicts-its-pod.md)
taints an unplugged device `liken.sh/unplugged`, and Kubernetes evicts
the pods of each claim that a pod owns. Each device's pod makes its
claim from a `ResourceClaimTemplate` (`claimName` in `pods.go`), so
the eviction reaches it. While the reservation is `Ready`, the
operator creates the pod again (`steady.go`), and the new pod's claim
is allocated wherever the device is now: the same port, another port,
or another machine. So the restart that a replug needs is built.

## What `observatory-operator` lacks

**A field for the driver's port.** The pod receives a tty at the path
it has on the machine. A serial driver opens the port that its
`DEVICE_PORT` property names. Many drivers search the other ports when
that open fails, but a driver with no search does not find its device
at the new path. The operator has no field that sets `DEVICE_PORT`, so
today a person sets it from KStars, as the `connect-usb-equipment`
skill describes. A stable path, such as the `/dev/serial/by-id` name
of the adapter, would serve a driver across a replug if the claim gave
the pod that path.

No replug of a USB serial adapter under a running reservation has been
tried on hardware.
