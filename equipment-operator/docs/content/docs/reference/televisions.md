---
title: Television
weight: 16
toc: true
---

<!-- Generated from deploy/televisions-crd.yaml by crdref. Do not edit. -->

The TV at the root of one HDMI tree. A CEC bus has at most one TV, and the TV is always at physical address 0.0.0.0 and logical address 0, so spec.cec.bus identifies the TV and no address is needed. When a CECBus in Control finds a TV and no Television names that bus, the operator creates a Television with the bus's own name and the equipment.liken.sh/discovered label. The operator owns only that object: a labeled Television under another name is a person's. To adopt the discovered Television, apply your own Television under the bus's name, with the spec you want. The object keeps the label, and the operator keeps it for as long as no other Television names the same bus. A Television of another name that names the same bus also works: the operator then deletes the discovered one.

## spec

The protocol block and the power a person asks for. A person writes every field.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--cec"></span>`cec` | [object](#speccec) | no | The TV is on a CEC bus. The node workload reads the TV's power over that bus and sends the TV its power commands. |
| <span id="spec--power"></span>`power` | string | no | The power the operator drives the TV to. The node workload applies each generation of the spec once: each spec edit is a new metadata.generation, and it never asserts a generation again, so a person who turns the TV off with its own remote is not overruled. To ask for the same value again, edit the spec, such as by removing this field and adding it back. A new object applies its spec.power when it is created, so a Television deleted and created again, by a GitOps prune or a restore, applies its spec.power again. The node workload that sends the bus's commands reads the TV's power first, and sends nothing when the TV already reports the state, unless it sent the TV a command in the last 15 seconds: a TV answers its old state for a while after a command. It sends Image View On for On, or Standby to the TV alone for Standby, and reads the power every second until the TV reports the new state. A TV takes seconds to change state and reports ToOn or ToStandby while it does. When 10 seconds pass and no read showed the new state or the transition toward it, the node workload sends the command again. One generation gets at most three commands, and an application stops reading after 30 seconds. A new generation cancels an application of an older one. status.powerGeneration and the PowerApplied condition report the result. An absent field is no request, and the operator leaves the TV where it is. When two Televisions name one bus, the operator applies only the spec.power of the one in charge, which the InCharge condition names. When that one is deleted, the other takes over, and the operator applies its spec.power once then, as it does for a new object. Quote the value On in YAML, as power: "On": kubectl reads an unquoted On as the boolean true, and the API server refuses it. Image View On also asks the TV to show a source, so a TV can switch to the input of the adapter's Display when it wakes. One of: `On`, `Standby`. |

### spec.cec

The TV is on a CEC bus. The node workload reads the TV's power over that bus and sends the TV its power commands.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speccec--bus"></span>`bus` | string | yes | The CECBus the TV is on. The bus must be in Control for the operator to read the TV's power or send it a command, because Listen sends nothing. |

## status

What the bus reports about the TV, and what the operator applied.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--cec"></span>`cec` | [object](#statuscec) | no | The TV as the adapters of its CECBus found it. The operator copies each fact from the TV's entry in the CECBus's status.devices. A fact the TV has not stated is absent: one TV answers its power status in standby and does not answer Give OSD Name. |
| <span id="status--power"></span>`power` | string | no | The power status the TV last reported: On, Standby, ToOn, or ToStandby. The adapters in Control ask the TV every 10 seconds with Give Device Power Status, and the adapters also read every Report Power Status the TV sends them. A TV in a deep standby or an eco mode can stop answering. After two questions in a row without an answer, this field is absent and Reachable is False; one missed answer keeps the last power, because a TV that wakes can miss one. The operator does not report Standby for a TV that did not answer. |
| <span id="status--powergeneration"></span>`powerGeneration` | integer | no | The metadata.generation whose spec.power the node workload applied. The node workload applies a generation when it differs from this field, so a restart of the node workload applies no generation twice. |
| <span id="status--displays"></span>`displays` | [\[\]object](#statusdisplays) | no | The Displays whose pictures reach this TV: the Display that each adapter of the CECBus names in spec.adapters[].display, while the bus is in Control and the Display has a physical address from its EDID. The operator derives the list. Each such adapter announces its Display's physical address on this bus, so that Display's picture enters this tree. A Display that no adapter names is not listed, even on a machine the bus names: that machine's other HDMI output can go to another TV, and a physical address does not name its tree. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Reachable: the TV answers its power status on its CECBus. It is Unknown while the bus is in Listen or has not finished a scan, Unknown with the reason Stale when an adapter's entry is stale, and False when the bus does not exist, when no adapter finds a TV, or when the TV acknowledges its address and does not answer. InCharge: whether this Television speaks for the TV of its bus; of two Televisions on one bus, the one not in charge names the one that is. PowerApplied: whether the TV reported the state that spec.power asked for, with the command the node workload sent, the number of commands, and the power the TV last reported. |

### status.cec

The TV as the adapters of its CECBus found it. The operator copies each fact from the TV's entry in the CECBus's status.devices. A fact the TV has not stated is absent: one TV answers its power status in standby and does not answer Give OSD Name.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscec--physicaladdress"></span>`physicalAddress` | string | no | The TV's physical address, 0.0.0.0. |
| <span id="statuscec--logicaladdress"></span>`logicalAddress` | integer | yes | The TV's logical address, 0. |
| <span id="statuscec--osdname"></span>`osdName` | string | no | The name the TV reports, at most 14 characters. |
| <span id="statuscec--vendor"></span>`vendor` | string | no | The TV's IEEE OUI, as six hex digits. |
| <span id="statuscec--cecversion"></span>`cecVersion` | string | no | The CEC version the TV reports: 1.3a, 1.4, or 2.0. |

### status.displays[]

The Displays whose pictures reach this TV: the Display that each adapter of the CECBus names in spec.adapters[].display, while the bus is in Control and the Display has a physical address from its EDID. The operator derives the list. Each such adapter announces its Display's physical address on this bus, so that Display's picture enters this tree. A Display that no adapter names is not listed, even on a machine the bus names: that machine's other HDMI output can go to another TV, and a physical address does not name its tree.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplays--name"></span>`name` | string | yes | The Display. |
| <span id="statusdisplays--physicaladdress"></span>`physicalAddress` | string | yes | The Display's physical address, from display-operator, in the dotted form 1.3.0.0. |
| <span id="statusdisplays--via"></span>`via` | [object](#statusdisplaysvia) | no | The Receiver the picture passes through: a Receiver with an input that names this Display's machine and this Display as its monitor, when the CECBus has an audio system above the Display in the tree. The physical address shows the path: a receiver at 1.0.0.0 is above a Display at 1.3.0.0 and not above one at 2.0.0.0. It is absent when no Receiver is on the path, such as for a Receiver whose optical input names a Display that is connected straight to the TV. |

#### status.displays[].via

The Receiver the picture passes through: a Receiver with an input that names this Display's machine and this Display as its monitor, when the CECBus has an audio system above the Display in the tree. The physical address shows the path: a receiver at 1.0.0.0 is above a Display at 1.3.0.0 and not above one at 2.0.0.0. It is absent when no Receiver is on the path, such as for a Receiver whose optical input names a Display that is connected straight to the TV.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusdisplaysvia--kind"></span>`kind` | string | yes | The kind of the object, Receiver. |
| <span id="statusdisplaysvia--name"></span>`name` | string | yes | The object's name. |

### status.conditions[]

Reachable: the TV answers its power status on its CECBus. It is Unknown while the bus is in Listen or has not finished a scan, Unknown with the reason Stale when an adapter's entry is stale, and False when the bus does not exist, when no adapter finds a TV, or when the TV acknowledges its address and does not answer. InCharge: whether this Television speaks for the TV of its bus; of two Televisions on one bus, the one not in charge names the one that is. PowerApplied: whether the TV reported the state that spec.power asked for, with the command the node workload sent, the number of commands, and the power the TV last reported.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no |  |
| <span id="statusconditions--reason"></span>`reason` | string | no |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |
