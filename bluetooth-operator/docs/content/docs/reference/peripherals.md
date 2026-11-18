---
title: Peripheral
weight: 15
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `Peripheral`

A `Peripheral` is one controller or speaker that has paired with an
adapter and shares link keys with it. The operator creates the
`Peripheral` when pairing succeeds, and when it finds a bond that
`bluetoothd` already stores. The keys are in a `Secret` that the
`Peripheral` owns. Read `status.conditions` for the link,
`status.battery` for the charge the device reports, and `status.bond`
for the keys. Edit `spec.alias` to rename a device, and
`spec.trusted` to set whether it may reconnect on its own.

To unpair a device, delete its `Peripheral`. The operator disconnects
the device, waits until no claim holds it, removes the device from
the `ResourceSlice`, and deletes the bond from `bluetoothd`.
Kubernetes deletes the `Secret` with the `Peripheral`.

One bonded Bluetooth device. The object is named for the device's address in the same form that the ResourceSlice uses, and it owns the `Secret` that stores the bond's keys.

## spec

The settings that the operator applies to the device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--alias"></span>`alias` | string | no | The name for this device, which the operator writes to BlueZ's `Device1.Alias`. bluetoothd stores the alias in the bond's own file, so the name is stored with the keys. When the field is empty, the device keeps the name that it reports. |
| <span id="spec--trusted"></span>`trusted` | boolean | no | Whether the device may connect without an agent's approval. The operator writes it to BlueZ's `Device1.Trusted`. When it is `false`, BlueZ asks an agent to authorize each service on every connection. No agent is registered outside a pairing window, so the device does not connect. A trusted controller connects when you press one of its buttons. For a trusted audio device, such as a speaker, the operator calls `Device1.Connect` while the device is not connected, and waits longer after each failed attempt, from 10 seconds up to 2 minutes. So the device connects soon after it is switched on and in range. Default: `true`. |

## status

What the operator observes about the device.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The device's Bluetooth address, in uppercase with colons. |
| <span id="status--name"></span>`name` | string | no | The name that the device reports. It is separate from `spec.alias`, so the name that the hardware reports stays visible after you rename the device. |
| <span id="status--icon"></span>`icon` | string | no | The freedesktop icon name that BlueZ gives this device, such as `input-gaming` or `audio-headset`. It is empty when BlueZ reports no icon. |
| <span id="status--adapter"></span>`adapter` | string | no | The address of the adapter that is paired with this device. |
| <span id="status--node"></span>`node` | string | no | The node whose operator has this bond. The value changes when you move the adapter to another node. |
| <span id="status--bond"></span>`bond` | [object](#statusbond) | no | What the operator observes about the bond. |
| <span id="status--battery"></span>`battery` | [object](#statusbattery) | no | The battery charge that the device reports. The operator reads the kernel's power supply class first, and reads BlueZ's `org.bluez.Battery1` interface for a device that has no kernel power supply. The block is absent when neither reports a level, which is the case for a device with no battery and for a device that is not connected. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The `Connected` condition is `True` while the device has a link with the radio, with the reason `LinkUp`. When it is `False`, the reason comes from the reason that BlueZ gave when the link ended. `Asleep`: the link timed out on a device that disconnects when idle, such as a T6 remote. When the device connects again, the operator writes one `KEY_UNKNOWN` press into its virtual input device, so the press that woke it is not lost. `LinkLost`: the link timed out on a device that stays connected while it is on, so it went out of range or its battery ran out. `ClosedByDevice`: the device ended the link, for example because it was switched off. `ClosedByRadio`: the operator ended the link, to unpair the device or to reconnect a controller whose input device did not appear. `AuthenticationFailed`: the keys did not match, so you must delete the `Peripheral` and pair the device again. `NotConnected`: the operator has no reason, because the link ended while the operator was not running or BlueZ gave none. `NotBonded`: bluetoothd has no object for the device, because another tool removed the bond. |

### status.bond

What the operator observes about the bond.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbond--held"></span>`held` | boolean | no | Whether bluetoothd still has this bond. It is `false` when bluetoothd no longer has the keys. When it changes to `false`, the operator posts a `BondLost` warning `Event` and keeps the Peripheral, because only a person deletes one. |
| <span id="statusbond--paired"></span>`paired` | boolean | no | Whether BlueZ reports this device as paired, from `Device1.Paired`. The operator reads `held` from the same property. `paired` is listed with `bonded`, `trusted`, and `connected`, the BlueZ properties that you compare when a device does not pair or reconnect. |
| <span id="statusbond--bonded"></span>`bonded` | boolean | no | Whether BlueZ reports a stored link key for this device, from `Device1.Bonded`. A device can be paired without a stored key. |
| <span id="statusbond--trusted"></span>`trusted` | boolean | no | Whether BlueZ reports this device as trusted, from `Device1.Trusted`. The operator writes `spec.trusted` to this property. |
| <span id="statusbond--connected"></span>`connected` | boolean | no | Whether BlueZ reports this device as connected, from `Device1.Connected`. |
| <span id="statusbond--secret"></span>`secret` | string | no | The namespace and name of the `Secret` that stores this bond's keys. This object owns the `Secret`, so deleting the Peripheral deletes it. |
| <span id="statusbond--pairedat"></span>`pairedAt` | string | no | When the operator first recorded this bond. For a bond that the operator created, this is the time of the pairing. For a bond that bluetoothd already had, it is the time that the operator found it. |
| <span id="statusbond--request"></span>`request` | string | no | The namespace and name of the `PairingRequest` that created this bond. It is empty for a bond that bluetoothd already had. The operator deletes a finished request after its `ttlSecondsAfterFinished`, and this field stays after that. |

### status.battery

The battery charge that the device reports. The operator reads the kernel's power supply class first, and reads BlueZ's `org.bluez.Battery1` interface for a device that has no kernel power supply. The block is absent when neither reports a level, which is the case for a device with no battery and for a device that is not connected.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbattery--percentage"></span>`percentage` | integer | no | The charge left, from 0 to 100 percent. |
| <span id="statusbattery--source"></span>`source` | string | no | Where the level was read. For a reading from the kernel, this is the name of the power supply, such as `ps-controller-battery-7c:66:ef:22:e7:80`. For a reading from BlueZ, it is the source that BlueZ reports, such as `HID` or a GATT service. |
| <span id="statusbattery--charging"></span>`charging` | boolean | no | Whether the device is charging. It is absent when BlueZ is the source, because BlueZ reports a level and not a charging state. It is also absent when the kernel reports the status `Unknown`. |

### status.conditions[]

The `Connected` condition is `True` while the device has a link with the radio, with the reason `LinkUp`. When it is `False`, the reason comes from the reason that BlueZ gave when the link ended. `Asleep`: the link timed out on a device that disconnects when idle, such as a T6 remote. When the device connects again, the operator writes one `KEY_UNKNOWN` press into its virtual input device, so the press that woke it is not lost. `LinkLost`: the link timed out on a device that stays connected while it is on, so it went out of range or its battery ran out. `ClosedByDevice`: the device ended the link, for example because it was switched off. `ClosedByRadio`: the operator ended the link, to unpair the device or to reconnect a controller whose input device did not appear. `AuthenticationFailed`: the keys did not match, so you must delete the `Peripheral` and pair the device again. `NotConnected`: the operator has no reason, because the link ended while the operator was not running or BlueZ gave none. `NotBonded`: bluetoothd has no object for the device, because another tool removed the bond.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition type, which is `Connected`. |
| <span id="statusconditions--status"></span>`status` | string | yes | Whether the reported state is true, false, or unknown. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | The reason for the condition's status, in CamelCase, such as `LinkUp`. |
| <span id="statusconditions--message"></span>`message` | string | no | One sentence that explains the reason and, when you can act on it, tells you what to do. The `Event` that the operator posts for each transition has the same sentence. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed. It does not change when the operator rewrites the object, or when the reason changes and the status stays the same. |
