---
title: bluetooth-operator
---

# `bluetooth-operator`

`bluetooth-operator` lets you pair a Bluetooth game controller or
remote with `kubectl`, and then give it to exactly one pod on a
[`liken`](https://liken.sh/docs/) cluster. The pod claims the
controller by its MAC address and gets that controller's input
devices, and no other input device on the machine.

What you can do:

* **Pair a DualSense with `kubectl`.** Create a `PairingRequest`, put
  the controller in pairing mode, and approve the address that the
  radio reports. Pairing is an API, so RBAC controls who can pair, and
  nobody needs a shell on a node or in a pod.
* **Give the controller to one pod.** A game or emulator pod claims
  the controller by its address and gets its `/dev/input/event*`
  nodes. No other pod gets that input, and the pod gets no other input
  device.
* **Claim a controller that is switched off.** The operator
  publishes a paired controller while it is off. A pod that claims it
  waits until you turn the controller on, and then starts.
* **Keep the pairing across restarts.** The operator stores the
  pairing keys in `Secrets`, so the controller can reconnect after a
  pod restart, an upgrade, or a reboot.

Start here:

* [Install the operator](/docs/guides/install/).
* [Pair a controller and give it to a pod](/docs/guides/pair-a-controller/).
* [Devices](/docs/reference/devices/): the devices, their attributes,
  and the claims that select them.

## How it works

`liken` publishes the machine's Bluetooth radio as a device. The
operator's pod claims the radio and runs `bluetoothd` itself, so the
`liken` system image contains no BlueZ and no D-Bus. The operator
publishes each paired controller as a device under the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
driver `bluetooth.liken.sh`.

It also publishes the radio's
[media bus](/docs/reference/devices/#the-media-bus), a D-Bus
connection to `bluetoothd`, which
[`audio-operator`](https://liken.sh/audio/) claims to play sound
through Bluetooth speakers.

`bluetooth-operator` is one of the extension operators for
[claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/).
You install it only if your cluster needs Bluetooth.

* [The source](https://github.com/liken-sh/liken/tree/main/bluetooth-operator)
* [The `liken` manual](https://liken.sh/docs/)
