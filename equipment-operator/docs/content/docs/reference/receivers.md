---
title: Receiver
weight: 10
toc: true
---

<!-- Generated from deploy/receivers-crd.yaml by crdref. Do not edit. -->

One piece of A/V equipment on the far end of a machine's cable: the protocol that reaches it, the inputs liken machines feed, and the session a Player holds on it.

## spec

How to reach the receiver and how it is wired. The cluster owner writes every field but session, which the media operator applies.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--denon"></span>`denon` | [object](#specdenon) | no | The receiver speaks the Denon and Marantz control protocol on TCP port 23: plain commands such as MV50, and events in the same form. |
| <span id="spec--inputs"></span>`inputs` | [\[\]object](#specinputs) | no | The receiver's inputs that liken machines feed. Nothing can discover this wiring, so the cluster owner declares it. A receiver forwards one EDID on every input, so the monitor id alone cannot tell two machines apart, and every entry names the machine. |
| <span id="spec--session"></span>`session` | [object](#specsession) | no | The Player that holds the receiver now. The media operator applies this block under its own field manager while a Play stands, and lifts it after. A session powers the receiver on, selects the input once, and takes the level from the volume topic. Nothing here is re-asserted: a person at the receiver's own remote outranks the cluster. |

### spec.denon

The receiver speaks the Denon and Marantz control protocol on TCP port 23: plain commands such as MV50, and events in the same form.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenon--address"></span>`address` | string | yes | The host name or IP address the receiver answers on, with an optional port. The port is 23 when absent. |

### spec.inputs[]

The receiver's inputs that liken machines feed. Nothing can discover this wiring, so the cluster owner declares it. A receiver forwards one EDID on every input, so the monitor id alone cannot tell two machines apart, and every entry names the machine.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specinputs--name"></span>`name` | string | yes | The input's name on the receiver, as its protocol spells it: MPLAY, GAME, TV, CBL/SAT. |
| <span id="specinputs--machine"></span>`machine` | string | yes | The liken machine whose HDMI output lands on this input, by node name. |
| <span id="specinputs--monitor"></span>`monitor` | string | yes | The monitor id the display and audio operators publish for this cable, such as don-0070-denon-avr. It is the check that the wire is really there. |

### spec.session

The Player that holds the receiver now. The media operator applies this block under its own field manager while a Play stands, and lifts it after. A session powers the receiver on, selects the input once, and takes the level from the volume topic. Nothing here is re-asserted: a person at the receiver's own remote outranks the cluster.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsession--player"></span>`player` | string | yes | The Player, as namespace/name. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specsession--input"></span>`input` | string | yes | The input the Player plays through, by its name in spec.inputs. |
| <span id="specsession--volumetopic"></span>`volumeTopic` | string | yes | The Player's volume topic on the media bus. The operator follows it, owns the level while the session stands, and writes the receiver's own knob back to it. |

## status

What the receiver last said, in its own units. Only the operator writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--power"></span>`power` | string | no | The power state the receiver last reported: on or standby. |
| <span id="status--input"></span>`input` | string | no | The input the receiver last reported as selected, whether liken selected it or a person did. |
| <span id="status--volume"></span>`volume` | string | no | The master volume the receiver last reported, in its own scale. A Denon counts 0 to 98 in half steps. |
| <span id="status--volumemax"></span>`volumeMax` | string | no | The volume limit set in the receiver's own menu, in the same scale. A session's level of 100 maps onto it. |
| <span id="status--mute"></span>`mute` | boolean | no | Whether the receiver last reported itself muted. |
| <span id="status--soundmode"></span>`soundMode` | string | no | The sound mode the receiver last reported, such as MULTI CH IN or STEREO. |
| <span id="status--service"></span>`service` | string | no | The Service that stands in for the receiver on the cluster network, once the operator makes one. Empty until then. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Reachable is True only after a recent answered exchange with the receiver, never on an open socket alone. |

### status.conditions[]

Reachable is True only after a recent answered exchange with the receiver, never on an open socket alone.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The check this entry reports, in CamelCase. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The verdict. True is the good verdict, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation this condition judged. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition holds this verdict, meant for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same answer in a sentence a person reads. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the verdict last changed. It moves only when the status flips. |
