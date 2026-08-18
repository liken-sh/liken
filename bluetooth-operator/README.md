# bluetooth-operator

Pair a Bluetooth game controller with `kubectl`, then hand it to
exactly one pod. `bluetooth-operator` is a Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver for [`liken`](https://github.com/liken-sh/liken) clusters. It
publishes each paired controller as a device under the driver name
`bluetooth.liken.sh`. A pod claims one controller by its MAC address
and receives that controller's evdev nodes, and no other input device
on the machine.

Everything runs through the Kubernetes API. You create a
`PairingRequest`, hold the controller's pairing buttons, and approve
the address the radio reported. Your game or emulator pod claims that
address, and only that pod reads the controller. The bond's keys are
in a `Secret`, so after a pod restart or a reboot the controller
reconnects with one button. This needs no SSH, no host configuration,
and no shell in any pod.

## What it needs from `liken`

`liken`'s own DRA driver publishes the raw hardware: the Bluetooth
adapter itself, as a `btusb` device. This operator claims that adapter
and publishes the higher grain, one device for each paired controller.
It uses no private interface into `liken`. The claim, the
`ResourceSlices` it writes, and the CDI files it leaves for the
runtime are the public contracts any DRA driver gets.

The operator is one of `liken`'s optional
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/),
and it installs as an ordinary workload. A cluster that never deploys
it behaves as it does now. Its pod runs bluetoothd, so the `liken`
system image ships no BlueZ and no D-Bus.

## Install

Create the two `DeviceClasses` first. A class is cluster policy,
yours to name and curate, so the base ships none;
[Install the operator](docs/content/docs/guides/install.md) gives
their YAML. Then apply the base:

    kubectl apply -k deploy/

The base is a `kustomize` directory you can also reference from your
own GitOps. The guide gives the steps, the verification, and the
privilege the pod takes.

## The manual

The manual publishes at **<https://bluetooth.liken.sh>**. It includes:

* [Install the operator](docs/content/docs/guides/install.md)
* [Pair a controller and give it to a pod](docs/content/docs/guides/pair-a-controller.md)
* [Devices](docs/content/docs/reference/devices.md): the published devices,
  their attributes and taints, and the claims that select them

The source files hold the rest. This repository is written to be
read, like `liken` itself: the comments in the Go files and the
manifests explain how the operator works and why it is built this way.
[`plans/`](plans/) holds the design documents and the open problems.

## The build

    go build ./...
    go test ./...
    docker build -t bluetooth-operator .
    docker build -f bluetoothd/Dockerfile -t bluetoothd .
    docker build -f bondfetch/Dockerfile -t bluetooth-bondfetch .

The three images are three parts of one pod, so a release builds and
tags all three from one commit. The Kubernetes libraries and the Go
version are pinned to what `liken` builds against, because the two
drivers serve the same kubelet on the same node.

## License

MIT. See [LICENSE](LICENSE).
