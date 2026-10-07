---
title: Adapter
weight: 10
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `Adapter`

An `Adapter` is one Bluetooth radio. The operator creates the
object for the adapter its pod claimed and names it for the radio's
address, lowercase with dashes. It is the root of the pairing
records: every [Peripheral](/docs/reference/peripherals/) bonded
with the radio belongs to it, so deleting an `Adapter` collects
every bond and every bond `Secret` with it. The operator refuses
that deletion while the radio is present; unplug the radio to let
it through. None of these objects names a machine, so a dongle
moved to another machine keeps its `Peripherals` and their stored
keys.
[Pair a controller](/docs/guides/pair-a-controller/) starts by
reading this object's name.

`spec.privacy` turns on Low Energy privacy for the radio.
[Privacy](/docs/concepts/privacy/) explains what it hides and what it
costs. `bluetoothd` reads the setting only when it starts, so when
`spec.privacy` differs from the value the running `bluetoothd` started
with, the operator stores the bonds, posts a `PrivacyChanged` `Event`
on the `Adapter` that names both values, and deletes its own pod. The
`DaemonSet` creates a new pod, which starts `bluetoothd` with the new
value. Every connected controller drops for the few seconds the new pod
takes to start, and reconnects as it does after any restart of the
pod. An empty field is `off`.

`status.privacy` and the `PRIVACY` column of `kubectl get adapters`
show the value the running `bluetoothd` started with:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   PRIVACY   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      device    1m

With privacy on, the operator keeps the radio's identity resolving key
in the `Secret` `bluetooth-identity-<adapter>` in the operator's
namespace. The `Adapter` owns it, so deleting the `Adapter` collects
it. The operator keeps it when privacy goes off again.

One Bluetooth adapter, named for its own address. The object follows the radio, so an adapter that moves to another machine keeps its Peripherals and its stored bonds.

## spec

What the operator makes true about the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name the radio broadcasts about itself, written into BlueZ's Adapter1.Alias. A discoverable window announces the adapter under this name. Leave it empty to keep the name bluetoothd chose. |
| <span id="spec--privacy"></span>`privacy` | string | no | Low Energy privacy for this radio, written into bluetoothd's Privacy key. With privacy on, the radio advertises and connects from a private address that it changes every few minutes, and only a device that paired with it can resolve that address back to the radio. A classic link always uses the radio's public address. off uses the fixed address, and is the value of an empty field. network accepts advertising only from a peer that uses a private address, which some older devices do not. device also accepts advertising from a peer's identity address. limited-network and limited-device advertise in Limited Discoverable Mode, which shows the identity address while the radio is discoverable, and scan as network and device do. bluetoothd reads the value only when it starts, so the operator deletes its own pod to apply a change. One of: `off`, `network`, `device`, `limited-network`, `limited-device`. |

## status

What the operator observes about the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The adapter's Bluetooth address, in the uppercase form the label on the hardware shows. |
| <span id="status--node"></span>`node` | string | no | The machine whose operator holds the radio now. The value changes when the adapter moves. |
| <span id="status--powered"></span>`powered` | boolean | no | Whether bluetoothd has the radio powered on. |
| <span id="status--privacy"></span>`privacy` | string | no | The privacy value that the running bluetoothd started with. It is what bluetoothd was told, not what the kernel accepted. btmgmt info in the bluetoothd container shows the kernel's setting. |
| <span id="status--deletionrefused"></span>`deletionRefused` | string | no | Why the operator kept its finalizer on an Adapter somebody deleted. Deleting an Adapter cascades to every Peripheral under it, so the operator refuses while the radio is present. Unplug the radio to let the deletion through. It is empty at every other time. |
