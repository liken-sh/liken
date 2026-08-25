---
title: Adapter
weight: 10
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

An `Adapter` is one Bluetooth radio. The operator creates the
object for the adapter its pod claimed and names it for the radio's
address, lowercase with dashes. It is the root of the pairing
records: every [Pairing](/docs/reference/pairings/) made with the
radio belongs to it, so deleting an `Adapter` collects every bond
and every bond `Secret` with it. The operator refuses that deletion
while the radio is present; unplug the radio to let it through.
None of these objects names a machine, so a dongle moved to another
machine keeps its `Pairings` and their stored keys.
[Pair a controller](/docs/guides/pair-a-controller/) starts by
reading this object's name.

One Bluetooth adapter, named for its own address. The object follows the radio, so an adapter that moves to another machine keeps its Pairings and its stored bonds.

## spec

What the operator makes true about the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name the radio broadcasts about itself, written into BlueZ's Adapter1.Alias. A discoverable window announces the adapter under this name. Leave it empty to keep the name bluetoothd chose. |

## status

What the operator observes about the radio.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The adapter's Bluetooth address, in the uppercase form the label on the hardware shows. |
| <span id="status--node"></span>`node` | string | no | The machine whose operator holds the radio now. The value changes when the adapter moves. |
| <span id="status--powered"></span>`powered` | boolean | no | Whether bluetoothd has the radio powered on. |
| <span id="status--deletionrefused"></span>`deletionRefused` | string | no | Why the operator kept its finalizer on an Adapter somebody deleted. Deleting an Adapter cascades to every Pairing under it, so the operator refuses while the radio is present. Unplug the radio to let the deletion through. It is empty at every other time. |
