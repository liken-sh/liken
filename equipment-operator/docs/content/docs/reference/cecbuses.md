---
title: CECBus
weight: 15
toc: true
---

<!-- Generated from deploy/cecbuses-crd.yaml by crdref. Do not edit. -->

One HDMI tree, which means one CEC wire, and the USB CEC adapters on the cluster's machines that are connected to it. CEC gives a bus no name, so a person names it. A node workload that holds an adapter no CECBus names creates a CECBus in Listen, named after its machine, with the equipment.liken.sh/discovered label. A person's CECBus that names the same machine takes over, and the node workload deletes the one it created. To adopt a discovered CECBus, create one under a new name that names the same machine, and delete the discovered one; an apply under the discovered name conflicts with the node workload, which owns that object's spec.

## spec

The mode and the adapters. A person writes every field.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--mode"></span>`mode` | string | yes | The consent a person gives the adapters, because a device on a CEC bus can wake the TV and switch its input. Listen claims no logical address and sends nothing: each adapter opens the bus as a monitor, which needs CAP_NET_ADMIN, and reports the devices it hears. A full device list needs polls, and a poll is a transmission, so the device list in Listen is partial. Control claims a playback logical address with the machine's name as the OSD name, announces the Display's physical address, answers the TV, and scans the bus every minute. One of: `Listen`, `Control`. |
| <span id="spec--adapters"></span>`adapters` | [\[\]object](#specadapters) | yes | The adapters connected to this wire, one for each machine. |

### spec.adapters[]

The adapters connected to this wire, one for each machine.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specadapters--machine"></span>`machine` | string | yes | The Machine that carries the adapter. liken publishes the adapter as the machine's -cec device, and the node workload on that machine holds it. |
| <span id="specadapters--display"></span>`display` | string | no | The Display whose physical address the adapter announces in Control. display-operator reads the address from the EDID and publishes it at the Display's status.physicalAddress. The adapter announces this address whether it is connected to a spare input and carries no video, or sits inline in the machine's video path, because either way the address is the place in the tree where this Display's picture enters. An Active Source from the adapter then switches the TV, and any receiver between, to this Display's input. |

## status

What the adapters report and what the operator derives from their reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--adapters"></span>`adapters` | [\[\]object](#statusadapters) | no | One entry for each adapter, written by the node workload on that adapter's machine under its own field manager. Only the entries of machines the spec names count. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | Every device on the wire, merged from the adapters' reports by physical address, in tree order: the TV first, then each device after the device it is connected to. When two adapters report one fact differently, the adapter first in spec.adapters wins. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | AddressKnown: every adapter in Control announces its Display's physical address. Joined: every adapter in Control holds a logical address. Coherent: the adapters see each other on the bus, and an adapter that does not is on a different wire than the spec states. Scanned: the device list is complete, which only Control achieves; a bus where an adapter finds no device says that the HDMI cable at the adapter's output may not carry the CEC wire. An adapter whose entry is stale makes each condition Unknown with the reason Stale, and an adapter whose node workload stopped makes each condition False with the reason Stopped. Coherent compares only adapters that finished a scan, by their OSD names. |

### status.adapters[]

One entry for each adapter, written by the node workload on that adapter's machine under its own field manager. Only the entries of machines the spec names count.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusadapters--machine"></span>`machine` | string | yes | The Machine that carries the adapter. |
| <span id="statusadapters--mode"></span>`mode` | string | no | The mode the adapter runs. |
| <span id="statusadapters--state"></span>`state` | string | no | Where the adapter is in its work. Listening: the adapter is a monitor. Joining: the adapter is in Control and holds no logical address. Joined: it holds a logical address and has not finished a scan. Scanned: it finished at least one scan. Refused: a call the adapter needs failed, and message gives the kernel's or the API server's text. Stopped: the node workload stopped, cleared the adapter's logical addresses, and left the bus, and message says why. |
| <span id="statusadapters--driver"></span>`driver` | string | no | The kernel driver of the adapter, such as pulse8-cec. |
| <span id="statusadapters--physicaladdress"></span>`physicalAddress` | string | no | The physical address the adapter announces, in the dotted form 1.3.0.0. |
| <span id="statusadapters--logicaladdress"></span>`logicalAddress` | integer | no | The logical address the adapter holds. It is absent in Listen and while the adapter holds none. |
| <span id="statusadapters--osdname"></span>`osdName` | string | no | The OSD name the adapter announces: its machine's name, cut to 14 characters. |
| <span id="statusadapters--message"></span>`message` | string | no | Why the adapter is not where its mode asks, in the words of the call that failed. |
| <span id="statusadapters--reportedat"></span>`reportedAt` | string | no | When the node workload last wrote this entry. It writes the entry every 30 seconds even when nothing changed, and the conditions treat an entry older than 90 seconds as stale, because its pod stopped without a word. |
| <span id="statusadapters--devices"></span>`devices` | [\[\]object](#statusadaptersdevices) | no | The devices this adapter found. In Control a scan polls every logical address and asks each device that answers for its facts. In Listen the adapter reports the devices it hears. |

#### status.adapters[].devices[]

The devices this adapter found. In Control a scan polls every logical address and asks each device that answers for its facts. In Listen the adapter reports the devices it hears.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusadaptersdevices--physicaladdress"></span>`physicalAddress` | string | no | The device's place in the HDMI tree, in the dotted form 1.0.0.0. It is absent until the device states it. |
| <span id="statusadaptersdevices--logicaladdress"></span>`logicalAddress` | integer | yes | The logical address the device holds. 0 is the TV and 5 is the audio system. |
| <span id="statusadaptersdevices--type"></span>`type` | string | no | The device type the device reports, or the one its logical address implies: TV, Recording, Tuner, Playback, AudioSystem, Switch, Processor, Backup, or Specific. |
| <span id="statusadaptersdevices--osdname"></span>`osdName` | string | no | The name the device reports, at most 14 characters. |
| <span id="statusadaptersdevices--vendor"></span>`vendor` | string | no | The device's IEEE OUI, as six hex digits. |
| <span id="statusadaptersdevices--cecversion"></span>`cecVersion` | string | no | The CEC version the device reports: 1.3a, 1.4, or 2.0. |
| <span id="statusadaptersdevices--power"></span>`power` | string | no | The power status the device last reported: On, Standby, ToOn, or ToStandby. |

### status.devices[]

Every device on the wire, merged from the adapters' reports by physical address, in tree order: the TV first, then each device after the device it is connected to. When two adapters report one fact differently, the adapter first in spec.adapters wins.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--physicaladdress"></span>`physicalAddress` | string | no | The device's place in the HDMI tree, in the dotted form 1.0.0.0. It is absent until the device states it. |
| <span id="statusdevices--logicaladdress"></span>`logicalAddress` | integer | yes | The logical address the device holds. 0 is the TV and 5 is the audio system. |
| <span id="statusdevices--type"></span>`type` | string | no | The device type the device reports, or the one its logical address implies: TV, Recording, Tuner, Playback, AudioSystem, Switch, Processor, Backup, or Specific. |
| <span id="statusdevices--osdname"></span>`osdName` | string | no | The name the device reports, at most 14 characters. |
| <span id="statusdevices--vendor"></span>`vendor` | string | no | The device's IEEE OUI, as six hex digits. |
| <span id="statusdevices--cecversion"></span>`cecVersion` | string | no | The CEC version the device reports: 1.3a, 1.4, or 2.0. |
| <span id="statusdevices--power"></span>`power` | string | no | The power status the device last reported: On, Standby, ToOn, or ToStandby. |

### status.conditions[]

AddressKnown: every adapter in Control announces its Display's physical address. Joined: every adapter in Control holds a logical address. Coherent: the adapters see each other on the bus, and an adapter that does not is on a different wire than the spec states. Scanned: the device list is complete, which only Control achieves; a bus where an adapter finds no device says that the HDMI cable at the adapter's output may not carry the CEC wire. An adapter whose entry is stale makes each condition Unknown with the reason Stale, and an adapter whose node workload stopped makes each condition False with the reason Stopped. Coherent compares only adapters that finished a scan, by their OSD names.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no |  |
| <span id="statusconditions--reason"></span>`reason` | string | no |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |
