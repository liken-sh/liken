---
title: Peripheral
weight: 15
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

A `Peripheral` is one bonded device: this controller or speaker
holds link keys with this adapter, and the object reports what the
radio observes about it. The operator creates the object when a
pairing succeeds, and when it finds a bond `bluetoothd` already
held. The keys are in a `Secret` this object owns. Read
`status.conditions` for the link, `status.battery` for the charge
the device reports, and `status.bond` for the keys. Deleting a
`Peripheral` is the unpair: the operator disconnects the device,
waits for the claim that holds it to release, retires the device
from the `ResourceSlice`, and removes the bond from `bluetoothd`,
and the `Secret` is collected with the object. Edit `spec.alias` to
rename a device and `spec.trusted` to say whether it may reconnect
on its own.

One bonded Bluetooth device, named for its own address in the same form the ResourceSlice names it. The object owns the Secret that holds this bond's keys.

## spec

What the operator makes true about the device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name for this device, written into BlueZ's Device1.Alias. bluetoothd stores the alias in the bond's own file, so the name is stored with the keys. Leave it empty to keep the name the device reports for itself. |
| <span id="spec--trusted"></span>`trusted` | boolean | no | Whether the device may connect with no agent, written into BlueZ's Device1.Trusted. With this off, BlueZ asks an agent to authorize each service on every connection, and no agent is registered outside a pairing window, so the device does not connect. A trusted controller connects when its own button is pressed. A trusted speaker is connected by the operator whenever it is powered on and in range. Default: `true`. |

## status

What the operator observes about the device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The device's Bluetooth address, in the uppercase form the label on the hardware shows. |
| <span id="status--name"></span>`name` | string | no | The name the device reports for itself. It is not spec.alias, so a person who renames a device still reads the name the hardware states. |
| <span id="status--icon"></span>`icon` | string | no | The freedesktop icon name BlueZ derives for this device, such as input-gaming or audio-headset. It is empty when BlueZ states none. |
| <span id="status--adapter"></span>`adapter` | string | no | The address of the adapter this bond belongs to. |
| <span id="status--node"></span>`node` | string | no | The machine whose operator holds this bond now. The value changes when the adapter moves. |
| <span id="status--bond"></span>`bond` | [object](#statusbond) | no | What the operator observes about the bond. |
| <span id="status--battery"></span>`battery` | [object](#statusbattery) | no | The charge the device reports, read from BlueZ's org.bluez.Battery1 interface. The block is absent when the device reports no level, which covers every device with no battery and every battery device that is not connected. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Connected reports whether the device holds a link now. Its reason is LinkUp when the link is up, Asleep for a Low Energy device that drops its link between presses and pages the radio again on the next one, NotConnected for a device that is switched off or out of range, and NotBonded when bluetoothd holds no object for the device, which means the bond was removed by another route. |

### status.bond

What the operator observes about the bond.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbond--held"></span>`held` | boolean | no | Whether bluetoothd still holds this bond. It goes false when the keys are gone from the daemon, which the operator reports and never acts on. |
| <span id="statusbond--secret"></span>`secret` | string | no | The namespace and name of the Secret that holds this bond's keys. The Secret is owned by this object, so deleting the Peripheral collects it. |
| <span id="statusbond--pairedat"></span>`pairedAt` | string | no | When the operator first recorded this bond, which is the pairing for a bond it made and the adoption for one it discovered. |
| <span id="statusbond--request"></span>`request` | string | no | The namespace and name of the PairingRequest that produced this bond. It is empty for a bond the operator adopted. A finished request is collected after its TTL, and this field outlasts it. |

### status.battery

The charge the device reports, read from BlueZ's org.bluez.Battery1 interface. The block is absent when the device reports no level, which covers every device with no battery and every battery device that is not connected.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbattery--percentage"></span>`percentage` | integer | no | The charge left, from 0 to 100. |
| <span id="statusbattery--source"></span>`source` | string | no | Where BlueZ read the level, such as HID or a GATT service. It is empty when BlueZ states none. |

### status.conditions[]

Connected reports whether the device holds a link now. Its reason is LinkUp when the link is up, Asleep for a Low Energy device that drops its link between presses and pages the radio again on the next one, NotConnected for a device that is switched off or out of range, and NotBonded when bluetoothd holds no object for the device, which means the bond was removed by another route.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The state this condition reports on. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the state holds. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One word for why the condition has this status. |
| <span id="statusconditions--message"></span>`message` | string | no | What a person can do about it, when the reason alone does not say. It is empty at every other time. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed, which is not when the operator last wrote the object. A reason that changes under the same status does not move it. |
