---
title: bluetooth.liken.sh
---

# `bluetooth.liken.sh`

Pair a Bluetooth game controller with `kubectl`, then hand it to
exactly one pod. `bluetooth-operator` is a Kubernetes
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver for [`liken`](https://liken.sh/docs/) clusters. It
publishes each paired controller as a device under the driver name
`bluetooth.liken.sh`. A pod claims one controller by its MAC address
and receives that controller's evdev nodes, and no other input device
on the machine.

With it you can:

* **Pair a DualSense from your desk.** Create a `PairingRequest`, hold
  the controller's pairing buttons, and approve the address the radio
  reported. Pairing is an API, so RBAC controls who may pair, and
  nobody needs a shell on a node or in a pod.
* **Give the controller to one pod.** A game or emulator pod claims
  the controller by address and receives its `/dev/input/event*`
  nodes. No other pod receives that input, and the claiming pod
  receives no other input device.
* **Wait for a controller that is switched off.** A paired controller
  stays published while it is off. A claim on it parks the pod
  `Unschedulable`, and the pod starts when somebody turns the
  controller on.
* **Keep the pairing across restarts.** The bond's keys are in
  `Secrets`, so one button reconnects the controller after a pod
  restart, an upgrade, or a reboot.

The operator is one of `liken`'s optional
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/),
and it installs as an ordinary workload. A cluster that never deploys
it behaves as it does now. `liken`'s own DRA driver publishes the raw
adapter. This operator claims that adapter, and its pod runs
bluetoothd, so the `liken` system image ships no BlueZ and no D-Bus.
The sibling operators publish
[monitor outputs](https://display.liken.sh) and
[audio outputs](https://audio.liken.sh).

Start with [Install the operator](/docs/guides/install/), then
[Pair a controller and give it to a pod](/docs/guides/pair-a-controller/).
[Devices](/docs/reference/devices/) describes the published devices, their
attributes, and the claims that select them. The source is
[liken-sh/bluetooth-operator](https://github.com/liken-sh/bluetooth-operator),
and it is written to be read: the comments in the Go files and the
manifests explain how the operator works.
