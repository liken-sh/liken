---
title: Pairing
weight: 15
toc: true
---

<!-- Generated from deploy/crds.yaml by docs/crdref. Do not edit. -->

A `Pairing` is one bond: this controller holds link keys with this
adapter. The operator creates the object when a pairing succeeds,
and when it finds a bond `bluetoothd` already held. The keys are in
a `Secret` this object owns. Deleting a `Pairing` is the unpair:
the operator disconnects the controller, waits for the claim that
holds it to release, retires the device from the `ResourceSlice`,
and removes the bond from `bluetoothd`, and the `Secret` is
collected with the object. Edit `spec.alias` to rename a controller
and `spec.trusted` to say whether it may reconnect on its own.

One paired controller, named for its own address in the same form the ResourceSlice names it. The object owns the Secret that holds this bond's keys.

## spec

What the operator makes true about the device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name for this controller, written into BlueZ's Device1.Alias. bluetoothd stores the alias in the bond's own file, so the name is stored with the keys. Leave it empty to keep the name the controller reports for itself. |
| <span id="spec--trusted"></span>`trusted` | boolean | no | Whether the controller may reconnect on its own, written into BlueZ's Device1.Trusted. With this off, BlueZ asks an agent to authorize each service on every connection, and no agent is registered outside a pairing window, so the controller does not reconnect. Default: `true`. |

## status

What the operator observes about the bond.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The controller's Bluetooth address, in the uppercase form the label on the hardware shows. |
| <span id="status--devicename"></span>`deviceName` | string | no | The name the controller reports for itself. |
| <span id="status--adapter"></span>`adapter` | string | no | The address of the adapter this bond belongs to. |
| <span id="status--node"></span>`node` | string | no | The machine whose operator holds this bond now. The value changes when the adapter moves. |
| <span id="status--connected"></span>`connected` | boolean | no | Whether the controller holds a connection now. |
| <span id="status--bonded"></span>`bonded` | boolean | no | Whether bluetoothd still holds this bond. It goes false when the keys are gone from the daemon, which the operator reports and never acts on. |
| <span id="status--secret"></span>`secret` | string | no | The namespace and name of the Secret that holds this bond's keys. The Secret is owned by this object, so deleting the Pairing collects it. |
| <span id="status--pairedat"></span>`pairedAt` | string | no | When the operator first recorded this bond, which is the pairing for a bond it made and the adoption for one it discovered. |
| <span id="status--request"></span>`request` | string | no | The namespace and name of the PairingRequest that produced this bond. It is empty for a bond the operator adopted. A finished request is collected after its TTL, and this field outlasts it. |
