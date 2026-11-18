# bluetooth-operator

`bluetooth-operator` pairs a Bluetooth game controller with
`kubectl` and gives it to exactly one pod. It is a Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver for [`liken`](https://github.com/liken-sh/liken) clusters. It
publishes each paired controller as a device under the driver name
`bluetooth.liken.sh`. A pod claims one controller by its MAC address
and gets that controller's evdev nodes, and no other input device on
the machine.

You pair through the Kubernetes API. You create a `PairingRequest`,
put the controller in pairing mode, and approve the address that the
radio reports. Your game or emulator pod then claims that address,
and only that pod can read the controller, because only one claim can
hold it. The operator stores the bond's keys in a `Secret`, so after
a pod restart or a reboot the controller reconnects when you press a
button. You need no SSH access, no host configuration, and no shell
in any pod.

The operator also publishes the media bus of each radio. A claim on
the bus lets a pod connect a sound server to the radio's
`bluetoothd`, and `audio-operator` claims it to play sound through
Bluetooth speakers.
[The media bus](https://liken.sh/bluetooth/docs/reference/devices/#the-media-bus)
in the manual describes its attributes and how a pod receives it.

## What it needs from `liken`

`liken`'s own DRA driver publishes the machine's Bluetooth adapter
as a `btusb` device. This operator claims that adapter through DRA
and uses no private interface of `liken`. Its claim, its
`ResourceSlice`, and the CDI files it writes for the container
runtime are the interfaces that any DRA driver uses.

The operator is one of `liken`'s optional
[extension operators for claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/),
and it installs as an ordinary workload. The operator's pod runs
`bluetoothd`, so the `liken` system image contains no BlueZ and no
D-Bus.

## Install

Apply the base:

    kubectl apply -k deploy/

The base contains the `DeviceClass` `bluetooth-adapter`, because the
operator's own claim names it and the pod cannot start without it.
You create the class that your workloads claim through.
[Install the operator](docs/content/docs/guides/install.md) gives
the YAML for `bluetooth-input`, the class to start with, and shows
how to reference the base from your own GitOps.

## The manual

The manual publishes at **<https://liken.sh/bluetooth/>**. It includes:

* [Install the operator](docs/content/docs/guides/install.md)
* [Pair a controller and give it to a pod](docs/content/docs/guides/pair-a-controller.md)
* [Devices](docs/content/docs/reference/devices.md): the published devices,
  their attributes and taints, and the claims that select them

The comments in the Go files and the manifests explain how the
operator works and why it is built that way. [`plans/`](plans/) holds
the design documents and the open problems.

## The build

    go build ./...
    go test ./...
    docker build -t bluetooth-operator .
    docker build -f bluetoothd/Dockerfile -t bluetoothd .
    docker build -f bondfetch/Dockerfile -t bluetooth-bondfetch .

The images run in one pod, so a release builds and tags each of them
from the same commit. The Kubernetes libraries and the Go version
match the ones `liken` builds with, because both drivers register
with the same kubelet on a node.

## License

MIT. See [LICENSE](LICENSE).
