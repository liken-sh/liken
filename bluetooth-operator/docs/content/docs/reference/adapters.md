---
title: Adapter
weight: 10
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `Adapter`

An `Adapter` is one Bluetooth radio. The operator creates it for the
adapter that its pod claimed, and names it for the radio's address in
lowercase with dashes. The `Adapter` owns every
[Peripheral](/docs/reference/peripherals/) bonded with the radio, so
deleting an `Adapter` also deletes its `Peripherals` and their bond
`Secrets`. While the radio is present, the operator keeps its
finalizer on a deleted `Adapter` and writes the reason to
`status.deletionRefused`. The deletion completes after you unplug the
radio. The ownership does not depend on a machine, so a dongle that
you move to another machine keeps its `Peripherals` and their stored
keys.
[Pair a controller](/docs/guides/pair-a-controller/) starts by
reading this object's name.

`spec.privacy` turns on Low Energy privacy for the radio.
[Privacy](/docs/concepts/privacy/) explains what it hides and what it
costs. `bluetoothd` reads the setting only when it starts. When
`spec.privacy` differs from the value that the running `bluetoothd`
started with, the operator stores the bonds, posts a `PrivacyChanged`
`Event` on the `Adapter` that gives both values, and deletes its own
pod. The `DaemonSet` creates a new pod, which starts `bluetoothd` with
the new value. Every connected controller disconnects, and reconnects
as it does after any restart of the pod. An empty field means `off`.

`status.privacy` and the `PRIVACY` column of `kubectl get adapters`
show the value that the running `bluetoothd` started with:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   PRIVACY   BTMON   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      device    false   1m

With privacy on, the operator stores the radio's identity resolving
key in the `Secret` `bluetooth-identity-<adapter>` in the operator's
namespace. The `Adapter` owns the `Secret`, so deleting the `Adapter`
deletes it. The operator does not delete the `Secret` when you turn
privacy off.

`spec.btmon: true` turns on a trace of the radio in the pod's `btmon`
container. The container logs each packet on the radio's HCI link
and management channel, so the trace of a stalled controller is still
there after the pod restarts. The trace is off by default, because
`btmon` prints the link keys, the long term keys, and the radio's
identity key in plain text, and anyone who can read the pod's logs
can read them. Turn the trace on while you diagnose a fault, and turn
it off afterward:

    kubectl patch adapter 04-4a-69-66-92-27 --type merge -p '{"spec":{"btmon":true}}'

When `spec.btmon` differs from the value that the `btmon` container
reads, the operator writes the new value for the container and posts
a `BtmonChanged` `Event` on the `Adapter`. The container starts or
stops `btmon` within seconds. The pod does not restart, so no
controller disconnects. An empty field means `false`. `status.btmon`
and the `BTMON` column show the value that the container reads.

One Bluetooth radio. The object is named for the adapter's address, so an adapter that you move to another machine keeps its `Peripherals` and their stored bonds.

## spec

The settings that the operator applies to the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name that the radio broadcasts, which the operator writes to BlueZ's `Adapter1.Alias`. A device that scans for adapters while a pairing window is open sees this name. When the field is empty, the radio keeps the name that bluetoothd chose. |
| <span id="spec--privacy"></span>`privacy` | string | no | Low Energy privacy for this radio, which the operator writes to the `Privacy` key of bluetoothd's configuration. With privacy on, the radio advertises and connects from a private address that changes periodically, and only a device that is paired with the radio can resolve that address. A classic Bluetooth link always uses the radio's public address. The value `off` uses the fixed address, and an empty field means `off`. The value `network` accepts advertising only from a peer that uses a private address, and some older devices do not. The value `device` also accepts advertising from a peer's identity address. The values `limited-network` and `limited-device` advertise in Limited Discoverable Mode, which shows the identity address while the radio is discoverable, and they scan as `network` and `device` do. bluetoothd reads the value only when it starts, so the operator deletes its own pod to apply a change. One of: `off`, `network`, `device`, `limited-network`, `limited-device`. |
| <span id="spec--btmon"></span>`btmon` | boolean | no | Whether the btmon container writes a trace of this radio's HCI link and management channel to its log. The trace prints key material in plain text, such as link keys, long term keys, and the radio's identity key, so anyone who can read the operator pod's logs can read those keys. Turn it on while you diagnose a fault, and turn it off afterward. An empty field means `false`. A change starts or stops the trace within seconds, and the pod does not restart. |

## status

What the operator observes about the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The adapter's Bluetooth address, in uppercase with colons. |
| <span id="status--node"></span>`node` | string | no | The node whose operator runs this radio. The value changes when you move the adapter to another node. |
| <span id="status--powered"></span>`powered` | boolean | no | Whether the radio is powered on. |
| <span id="status--privacy"></span>`privacy` | string | no | The privacy value that the running bluetoothd started with. This is the value that bluetoothd was configured with, which can differ from the setting that the kernel accepted. To read the kernel's setting, run `btmgmt info` in the bluetoothd container. |
| <span id="status--btmon"></span>`btmon` | boolean | no | The trace setting that the btmon container reads. The operator writes it from `spec.btmon`, and the container runs the trace while it is `true`. |
| <span id="status--deletionrefused"></span>`deletionRefused` | string | no | Why the operator keeps its finalizer on this deleting Adapter. Deleting an Adapter also deletes every `Peripheral` under it, which unpairs each device, so the operator does not let the deletion finish while the radio is plugged in. Kubernetes cannot cancel a deletion, so the Adapter stays until you unplug the radio. Then the operator removes the finalizer. The field is empty until the operator refuses a deletion. |
