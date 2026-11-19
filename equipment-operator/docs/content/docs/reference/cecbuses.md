---
title: CECBus
weight: 15
toc: true
---

<!-- Generated from deploy/cecbuses-crd.yaml by crdref. Do not edit. -->

# `CECBus`

One HDMI tree, which has one CEC wire, and the USB CEC adapters on the cluster's machines that connect to it. CEC gives a bus no name, so you choose the name of the `CECBus`. The TV never shows that name. It lists each adapter by `spec.osdName`. When the node workload finds an adapter on a machine that no `CECBus` lists, it creates a `CECBus` in `Listen`, named after the machine and labeled `equipment.liken.sh/discovered`. To take over a discovered `CECBus`, create your own under a different name that lists the same machine. The node workload then deletes the discovered one. Do not apply your spec under the discovered name. The node workload created that spec under its own field manager, so a server-side apply conflicts, and the object keeps the label, so the node workload deletes it as soon as another `CECBus` lists the machine.

## spec

The mode, the OSD name, and the adapters of the bus.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--mode"></span>`mode` | string | yes | Whether the adapters may send on the bus. A message on a CEC bus can wake the TV and switch its input, so the adapters send nothing until you set `Control`. In `Listen`, each adapter opens the bus as a monitor, which needs CAP_NET_ADMIN, claims no logical address, and lists the devices it hears. Only a poll finds a device that stays silent, and a poll is a transmission, so the list in `Listen` can miss devices. In `Control`, each adapter claims a playback logical address, announces `spec.osdName` and the physical address of its `Display`, replies to the TV, and scans the bus once when it joins. After the scan it sends nothing on a timer. It sends the replies that CEC requires, the commands that a person's request causes, one query to each device that announces itself, and a read of the TV's power status when it needs one: when the TV announces itself with an unknown power status, around a command, and when the remote's power button is pressed. One of: `Listen`, `Control`. |
| <span id="spec--osdname"></span>`osdName` | string | no | The name that every adapter of the bus announces in Control. The TV lists it as the name of the source in its input menu. It is 1 to 14 printable ASCII characters, from space to tilde, because that is the limit of the operand of Set OSD Name in HDMI-CEC 1.3a (CEC Table 26). The default is `liken`. The kernel sets the name only when an adapter claims a logical address, so when you change the name, each adapter releases its address and claims it again, and the source leaves the TV's input menu and comes back. Default: `liken`. Pattern: `^[ -~]+$`. |
| <span id="spec--adapters"></span>`adapters` | [\[\]object](#specadapters) | yes | The adapters connected to this wire, one entry for each machine, from 1 to 8 entries. |

### spec.adapters[]

The adapters connected to this wire, one entry for each machine, from 1 to 8 entries.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specadapters--machine"></span>`machine` | string | yes | The `Machine` that the adapter is plugged into. `liken` publishes the adapter as the machine's `-cec` device, and the node workload on that machine opens it. |
| <span id="specadapters--display"></span>`display` | string | no | The `Display` whose physical address the adapter announces in Control. Required in Control. `display-operator` reads the address from the monitor's EDID and publishes it at `status.physicalAddress` of the `Display`. The adapter announces this address even when it is plugged into a spare input and passes no video, because the address is where the picture of this `Display` enters the HDMI tree. So an Active Source message from the adapter switches the TV, and any receiver between them, to the input that shows this `Display`. |

## status

What the adapters report, and the device list and conditions that the operator derives from those reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--adapters"></span>`adapters` | [\[\]object](#statusadapters) | no | One entry for each adapter. The node workload on the adapter's machine writes the entry under its own field manager. The operator ignores the entry of a machine that `spec.adapters` does not list. |
| <span id="status--devices"></span>`devices` | [\[\]object](#statusdevices) | no | Every device on the wire, merged from the reports of the adapters by physical address, in tree order: the TV first, then each device after the device it is connected to. When two adapters report one fact differently, the adapter that comes first in `spec.adapters` wins. The exception is the System Audio Mode, where the report with the later `systemAudioModeAt` wins. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `AddressKnown`: every adapter in `Control` announces the physical address of its Display. `Joined`: every adapter in `Control` holds a logical address. `Coherent`: each adapter sees every other adapter on the bus at the physical address that the other announces. When one does not, the two adapters are probably on different wires. `Coherent` compares only adapters that finished a scan, and matches them by physical address, because every adapter of a bus announces the same OSD name. `Scanned`: every adapter finished the scan it runs when it joins and found at least one device. It is never `True` in `Listen`. An adapter that finds no device may be on an HDMI cable that does not carry the CEC wire. In `Listen`, `AddressKnown` and `Joined` are `False` and `Coherent` is `Unknown`, with the reason `Listening`. In `Control`, when an adapter's entry is stale, each condition is `Unknown` with the reason `Stale`, and when an adapter's node workload stopped, each condition is `False` with the reason `Stopped`. |

### status.adapters[]

One entry for each adapter. The node workload on the adapter's machine writes the entry under its own field manager. The operator ignores the entry of a machine that `spec.adapters` does not list.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusadapters--machine"></span>`machine` | string | yes | The `Machine` that the adapter is plugged into. |
| <span id="statusadapters--mode"></span>`mode` | string | no | The mode that the adapter runs in. |
| <span id="statusadapters--state"></span>`state` | string | no | How far the adapter is through its start-up. `Listening`: the adapter is a monitor. `Joining`: the adapter is in Control and has no logical address yet. `Joined`: it has a logical address and has not finished its scan. `Scanned`: it finished the scan it runs when it joins. `Refused`: a kernel or API server call that the adapter needs failed, and `message` has the error. `Stopped`: the node workload stopped, cleared the adapter's logical addresses, and left the bus, and `message` says why. |
| <span id="statusadapters--driver"></span>`driver` | string | no | The kernel driver of the adapter, such as `pulse8-cec`. |
| <span id="statusadapters--physicaladdress"></span>`physicalAddress` | string | no | The physical address the adapter announces, in the dotted form 1.3.0.0. |
| <span id="statusadapters--logicaladdress"></span>`logicalAddress` | integer | no | The logical address that the adapter claimed. It is absent in Listen and until the adapter claims one. |
| <span id="statusadapters--osdname"></span>`osdName` | string | no | The OSD name that the adapter announces in Control. It is `spec.osdName` of the bus. |
| <span id="statusadapters--activesource"></span>`activeSource` | string | no | The physical address in the last Active Source message that the adapter heard or sent, in the dotted form 1.3.0.0. Active Source is a broadcast, so an adapter hears it in `Listen` and in `Control`. The kernel does not return an adapter's own messages to it, so the node workload records the Active Source messages that it sends itself. |
| <span id="statusadapters--message"></span>`message` | string | no | Why the adapter is not in the state that its mode needs, as the error text of the call that failed. |
| <span id="statusadapters--reportedat"></span>`reportedAt` | string | no | When the node workload last wrote this entry. It writes the entry only when the entry changes, so an old time does not mean that the entry is stale. The operator treats an entry that is not `Stopped` as stale when no pod of the `equipment-operator-cec` DaemonSet runs on the entry's machine, when the pod there stopped being Ready after the entry was written, or when a new pod there is still not Ready 60 seconds after the entry was written. |
| <span id="statusadapters--devices"></span>`devices` | [\[\]object](#statusadaptersdevices) | no | The devices that this adapter found. In `Control` the adapter polls every logical address when it joins, and queries each device that answers. After that it updates the list from the messages that devices send. When a new device broadcasts Report Physical Address, the adapter adds it and queries it once for the facts that message does not include. The adapter removes a device only when the device does not acknowledge a message, so a device that leaves the bus silently stays in the list until the adapter joins again. In `Listen` the adapter lists the devices it hears. |

#### status.adapters[].devices[]

The devices that this adapter found. In `Control` the adapter polls every logical address when it joins, and queries each device that answers. After that it updates the list from the messages that devices send. When a new device broadcasts Report Physical Address, the adapter adds it and queries it once for the facts that message does not include. The adapter removes a device only when the device does not acknowledge a message, so a device that leaves the bus silently stays in the list until the adapter joins again. In `Listen` the adapter lists the devices it hears.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusadaptersdevices--physicaladdress"></span>`physicalAddress` | string | no | The position of the device in the HDMI tree, in the dotted form 1.0.0.0. It is absent until the device reports it. |
| <span id="statusadaptersdevices--logicaladdress"></span>`logicalAddress` | integer | yes | The logical address of the device. 0 is the TV and 5 is the audio system. |
| <span id="statusadaptersdevices--type"></span>`type` | string | no | The device type that the device reports, or the type that its logical address implies. One of `TV`, `Recording`, `Tuner`, `Playback`, `AudioSystem`, `Switch`, `Processor`, `Backup`, or `Specific`. |
| <span id="statusadaptersdevices--osdname"></span>`osdName` | string | no | The OSD name that the device reports, at most 14 characters. |
| <span id="statusadaptersdevices--vendor"></span>`vendor` | string | no | The IEEE OUI of the device, as six hex digits. A Device Vendor ID of 000000 means no vendor, so it never replaces a vendor that the device reported before. The field is absent until the device reports a real one. |
| <span id="statusadaptersdevices--cecversion"></span>`cecVersion` | string | no | The CEC version that the device reports, from `1.1` to `2.0`. |
| <span id="statusadaptersdevices--power"></span>`power` | string | no | The power status that the device last reported: `On`, `Standby`, `ToOn`, or `ToStandby`. |
| <span id="statusadaptersdevices--systemaudiomode"></span>`systemAudioMode` | string | no | The System Audio Mode that the audio system, the device at logical address 5, last reported. While it is `On`, the TV mutes its own speakers and sends its remote's volume keys to the audio system (HDMI-CEC 1.3a, CEC 13.15.2). In `Control` the adapter reads the mode in its scan. After that, a broadcast Set System Audio Mode, such as the `Off` that an audio system broadcasts before standby, updates it, and so does a System Audio Mode Status. When a wake sends a System Audio Mode Request and no broadcast `On` follows, the adapter reads the mode once more. A Set System Audio Mode sent to one device, such as the TV, does not update it, because the audio system sends its first `On` to the TV alone and goes no further when the TV refuses it. Absent until the audio system reports it. One of: `On`, `Off`. |
| <span id="statusadaptersdevices--systemaudiomodeat"></span>`systemAudioModeAt` | string | no | When the adapter last read or heard `systemAudioMode`. |

### status.devices[]

Every device on the wire, merged from the reports of the adapters by physical address, in tree order: the TV first, then each device after the device it is connected to. When two adapters report one fact differently, the adapter that comes first in `spec.adapters` wins. The exception is the System Audio Mode, where the report with the later `systemAudioModeAt` wins.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdevices--physicaladdress"></span>`physicalAddress` | string | no | The position of the device in the HDMI tree, in the dotted form 1.0.0.0. It is absent until the device reports it. |
| <span id="statusdevices--logicaladdress"></span>`logicalAddress` | integer | yes | The logical address of the device. 0 is the TV and 5 is the audio system. |
| <span id="statusdevices--type"></span>`type` | string | no | The device type that the device reports, or the type that its logical address implies. One of `TV`, `Recording`, `Tuner`, `Playback`, `AudioSystem`, `Switch`, `Processor`, `Backup`, or `Specific`. |
| <span id="statusdevices--osdname"></span>`osdName` | string | no | The OSD name that the device reports, at most 14 characters. |
| <span id="statusdevices--vendor"></span>`vendor` | string | no | The IEEE OUI of the device, as six hex digits. A Device Vendor ID of 000000 means no vendor, so it never replaces a vendor that the device reported before. The field is absent until the device reports a real one. |
| <span id="statusdevices--cecversion"></span>`cecVersion` | string | no | The CEC version that the device reports, from `1.1` to `2.0`. |
| <span id="statusdevices--power"></span>`power` | string | no | The power status that the device last reported: `On`, `Standby`, `ToOn`, or `ToStandby`. |
| <span id="statusdevices--systemaudiomode"></span>`systemAudioMode` | string | no | The System Audio Mode that the audio system, the device at logical address 5, last reported. While it is `On`, the TV mutes its own speakers and sends its remote's volume keys to the audio system (HDMI-CEC 1.3a, CEC 13.15.2). In `Control` the adapter reads the mode in its scan. After that, a broadcast Set System Audio Mode, such as the `Off` that an audio system broadcasts before standby, updates it, and so does a System Audio Mode Status. When a wake sends a System Audio Mode Request and no broadcast `On` follows, the adapter reads the mode once more. A Set System Audio Mode sent to one device, such as the TV, does not update it, because the audio system sends its first `On` to the TV alone and goes no further when the TV refuses it. Absent until the audio system reports it. One of: `On`, `Off`. |
| <span id="statusdevices--systemaudiomodeat"></span>`systemAudioModeAt` | string | no | When an adapter last read or heard `systemAudioMode`. |

### status.conditions[]

`AddressKnown`: every adapter in `Control` announces the physical address of its Display. `Joined`: every adapter in `Control` holds a logical address. `Coherent`: each adapter sees every other adapter on the bus at the physical address that the other announces. When one does not, the two adapters are probably on different wires. `Coherent` compares only adapters that finished a scan, and matches them by physical address, because every adapter of a bus announces the same OSD name. `Scanned`: every adapter finished the scan it runs when it joins and found at least one device. It is never `True` in `Listen`. An adapter that finds no device may be on an HDMI cable that does not carry the CEC wire. In `Listen`, `AddressKnown` and `Joined` are `False` and `Coherent` is `Unknown`, with the reason `Listening`. In `Control`, when an adapter's entry is stale, each condition is `Unknown` with the reason `Stale`, and when an adapter's node workload stopped, each condition is `False` with the reason `Stopped`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no |  |
| <span id="statusconditions--reason"></span>`reason` | string | no |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |
