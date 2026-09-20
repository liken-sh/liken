---
title: Receiver
weight: 10
toc: true
---

<!-- Generated from deploy/receivers-crd.yaml by crdref. Do not edit. -->

One piece of A/V equipment on the far end of a machine's cable. The resource declares the protocol that reaches it, the inputs liken machines feed, and the session that currently uses it.

## spec

How to reach the receiver and how its inputs are wired. The cluster owner writes every field except session. The media operator applies session.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--denon"></span>`denon` | [object](#specdenon) | no | The receiver accepts the Denon and Marantz control protocol on TCP port 23. Commands such as MV50 and receiver events use the same plain-text form. |
| <span id="spec--volume"></span>`volume` | [object](#specvolume) | no | The loudest level a press may set and the distance one press moves, both in the receiver's own scale. A Denon requires max. |
| <span id="spec--inputs"></span>`inputs` | [\[\]object](#specinputs) | no | The receiver inputs that liken machines feed. The cluster owner declares this wiring because the operator cannot discover it. A receiver forwards one EDID on every input, so the monitor ID alone cannot distinguish two machines. Each entry therefore names its machine. |
| <span id="spec--session"></span>`session` | [object](#specsession) | no | The Player that currently uses the receiver. The media operator applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward. The session owns the level from the volume topic and marks itself owner on that topic plus /owner. The operator sends power and input commands once each time active or awake changes to true. It does not re-assert those commands. A person using the receiver's remote can therefore change them. |
| <span id="spec--power"></span>`power` | string | no | The power state the operator drives the receiver to. The operator owns this field: it applies it once per change and never re-asserts it, so a GitOps manifest that omits it leaves the receiver wherever the operator last put it. A toggle from the session's power topic rewrites it. The Denon cannot tell standby from off, so both mean standby. One of: `on`, `off`, `standby`. |
| <span id="spec--settingstopic"></span>`settingsTopic` | string | no | The topic the operator subscribes to for the life of the Receiver. A message on it is a settings toggle write, {"setting":"tone.bass","value":3}, with the value in display units. An absent topic subscribes to nothing. |
| <span id="spec--commandstopic"></span>`commandsTopic` | string | no | The topic the operator subscribes to for the life of the Receiver. A message on it is a one-shot command, {"command":"quick.3"}, an action that is not a setting. An absent topic subscribes to nothing. |
| <span id="spec--zones"></span>`zones` | [map\[string\]object](#speczones) | no | The receiver's second and third zones, keyed by the zone's name as the receiver reports it. The main zone has no entry here: spec.power and spec.session drive it. |

### spec.denon

The receiver accepts the Denon and Marantz control protocol on TCP port 23. Commands such as MV50 and receiver events use the same plain-text form.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenon--address"></span>`address` | string | yes | The host name or IP address the receiver answers on, with an optional port. The port is 23 when absent. |
| <span id="specdenon--settings"></span>`settings` | [object](#specdenonsettings) | no | The receiver's settings, one family per block, in display units. The operator applies a declared setting once on change and never re-asserts it, so an omitted key leaves the receiver where it sits. A key written from the settings topic returns here at the leaf, so a declared key and a bus write never own the same field. |

#### spec.denon.settings

The receiver's settings, one family per block, in display units. The operator applies a declared setting once on change and never re-asserts it, so an omitted key leaves the receiver where it sits. A key written from the settings topic returns here at the leaf, so a declared key and a bus write never own the same field.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettings--system"></span>`system` | [object](#specdenonsettingssystem) | no | The unit-wide settings. Power is reported by the driver but not declarable here, because the controller owns it through spec.power. |
| <span id="specdenonsettings--tone"></span>`tone` | [object](#specdenonsettingstone) | no | The tone control and the two trims, in display units where 0dB is neutral. |
| <span id="specdenonsettings--audyssey"></span>`audyssey` | [object](#specdenonsettingsaudyssey) | no | The Audyssey room correction settings. |
| <span id="specdenonsettings--audio"></span>`audio` | [object](#specdenonsettingsaudio) | no | The audio processing settings. |
| <span id="specdenonsettings--channelvolumes"></span>`channelVolumes` | map[string]number | no | One trim per channel, keyed by the channel's name, in display units where 0dB is neutral. |

#### spec.denon.settings.system

The unit-wide settings. Power is reported by the driver but not declarable here, because the controller owns it through spec.power.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingssystem--eco"></span>`eco` | string | no | The Eco mode the receiver uses: auto, on, or off. |
| <span id="specdenonsettingssystem--dimmer"></span>`dimmer` | string | no | The display dimmer: off, dim, dark, or bright. |
| <span id="specdenonsettingssystem--autostandby"></span>`autoStandby` | string | no | The auto standby timer: off, or 15m, 30m, 60m, 2h, 4h, or 8h. |
| <span id="specdenonsettingssystem--speakerpreset"></span>`speakerPreset` | integer | no | The speaker preset, 1 through 4. |
| <span id="specdenonsettingssystem--audioinputmode"></span>`audioInputMode` | string | no | The audio input mode: auto, hdmi, digital, or analog. |
| <span id="specdenonsettingssystem--videoselect"></span>`videoSelect` | string | no | The video source the receiver selects, a name it already knows, or off. |
| <span id="specdenonsettingssystem--bluetoothtransmitter"></span>`bluetoothTransmitter` | string | no | The Bluetooth transmitter: on or off. |
| <span id="specdenonsettingssystem--bluetoothoutput"></span>`bluetoothOutput` | string | no | The Bluetooth output: speakers or bluetooth. |

#### spec.denon.settings.tone

The tone control and the two trims, in display units where 0dB is neutral.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingstone--control"></span>`control` | boolean | no | Whether the tone trims apply. False bypasses them. |
| <span id="specdenonsettingstone--bass"></span>`bass` | integer | no | The bass trim, 12dB on each side of neutral. |
| <span id="specdenonsettingstone--treble"></span>`treble` | integer | no | The treble trim, 12dB on each side of neutral. |

#### spec.denon.settings.audyssey

The Audyssey room correction settings.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingsaudyssey--multeq"></span>`multeq` | string | no | The MultEQ mode: reference, l/r bypass, flat, manual, or off. |
| <span id="specdenonsettingsaudyssey--dynamiceq"></span>`dynamicEq` | boolean | no | Whether Dynamic EQ is on. |
| <span id="specdenonsettingsaudyssey--referenceleveloffset"></span>`referenceLevelOffset` | integer | no | The reference level offset, in dB, one of 0, 5, 10, or 15. One of: `0`, `5`, `10`, `15`. |
| <span id="specdenonsettingsaudyssey--dynamicvolume"></span>`dynamicVolume` | string | no | The Dynamic Volume mode, such as off, light, medium, or heavy. |
| <span id="specdenonsettingsaudyssey--loudnessmanagement"></span>`loudnessManagement` | boolean | no | Whether Loudness Management is on. |

#### spec.denon.settings.audio

The audio processing settings.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingsaudio--drc"></span>`drc` | string | no | The Dynamic Range mode: off, auto, low, mid, or hi. |
| <span id="specdenonsettingsaudio--lfe"></span>`lfe` | integer | no | The LFE level, a cut from 0dB down to 10dB. |
| <span id="specdenonsettingsaudio--effect"></span>`effect` | integer | no | The effect level. |
| <span id="specdenonsettingsaudio--delay"></span>`delay` | integer | no | The delay, 0 to 999 milliseconds. |
| <span id="specdenonsettingsaudio--audiodelay"></span>`audioDelay` | integer | no | The audio delay, 0 to 999 milliseconds. |
| <span id="specdenonsettingsaudio--subwoofer"></span>`subwoofer` | boolean | no | Whether the subwoofer is on. |
| <span id="specdenonsettingsaudio--restorer"></span>`restorer` | string | no | The Audio Restorer mode: off, low, medium, or high. |
| <span id="specdenonsettingsaudio--graphiceq"></span>`graphicEq` | string | no | The Graphic EQ: off or on. |
| <span id="specdenonsettingsaudio--headphoneeq"></span>`headphoneEq` | string | no | The Headphone EQ: off or on. |
| <span id="specdenonsettingsaudio--speakervirtualizer"></span>`speakerVirtualizer` | boolean | no | Whether the Speaker Virtualizer is on. |
| <span id="specdenonsettingsaudio--dialogenhancer"></span>`dialogEnhancer` | string | no | The Dialog Enhancer level: off, low, mid, or high. |

### spec.volume

The loudest level a press may set and the distance one press moves, both in the receiver's own scale. A Denon requires max.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specvolume--max"></span>`max` | number | no | The loudest a press may drive this receiver, in its own scale. A Denon counts 0 to 98 and this is capped there. 100 on the bus means this value. A hand on the receiver's own remote can still go past it. |
| <span id="specvolume--step"></span>`step` | number | no | How far one press moves the receiver, in its own scale. Half steps are allowed. Absent means one whole unit. |

### spec.inputs[]

The receiver inputs that liken machines feed. The cluster owner declares this wiring because the operator cannot discover it. A receiver forwards one EDID on every input, so the monitor ID alone cannot distinguish two machines. Each entry therefore names its machine.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specinputs--name"></span>`name` | string | yes | The input's name on the receiver, as its protocol spells it: MPLAY, GAME, TV, CBL/SAT. |
| <span id="specinputs--machine"></span>`machine` | string | yes | The liken machine whose HDMI output lands on this input, by node name. |
| <span id="specinputs--monitor"></span>`monitor` | string | yes | The monitor id the display and audio operators publish for this cable, such as don-0070-denon-avr. It is the check that the wire is really there. |
| <span id="specinputs--soundmode"></span>`soundMode` | string | no | The sound mode the receiver selects with this input, when present. The session selects it in the same one-shot that selects the input, so a Play brings the picture and the mode together. |

### spec.session

The Player that currently uses the receiver. The media operator applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward. The session owns the level from the volume topic and marks itself owner on that topic plus /owner. The operator sends power and input commands once each time active or awake changes to true. It does not re-assert those commands. A person using the receiver's remote can therefore change them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsession--player"></span>`player` | string | yes | The Player, as namespace/name. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specsession--input"></span>`input` | string | yes | The input the Player plays through, by its name in spec.inputs. |
| <span id="specsession--volumetopic"></span>`volumeTopic` | string | yes | The Player's volume topic on the media bus, in full. The operator subscribes to it, publishes a retained owner mark on the topic plus /owner while the session exists, and writes the receiver's true level back to it. |
| <span id="specsession--powertopic"></span>`powerTopic` | string | no | The topic on which the remote's power button publishes a toggle, in full. When present, the operator subscribes to it and flips the receiver's power on a {"action":"toggle"} message, updating spec.power to match. An absent topic subscribes the session to nothing. |
| <span id="specsession--active"></span>`active` | boolean | no | Whether a Play is present on the Player. When this changes to true, the receiver is powered on and its input is selected once. Changing this to false does not send a power or input command. The session still owns the level. The awake field can independently trigger those commands. Absent means false. |
| <span id="specsession--awake"></span>`awake` | boolean | no | Whether the room's screen is awake. A change to true triggers the same one-shot power and input commands as active, whatever active says. A change to false sends no command to the receiver. Absent means asleep. |

### spec.zones.*

One zone beyond main, keyed by the zone's name. Every field is optional, so a bare key just claims the zone.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speczones--power"></span>`power` | string | no | The power state the operator drives this zone to. One of: `on`, `off`, `standby`. |
| <span id="speczones--input"></span>`input` | string | no | The input this zone selects, by its name in spec.inputs. |
| <span id="speczones--volume"></span>`volume` | number | no | The zone's volume in the receiver's display units. |
| <span id="speczones--mute"></span>`mute` | boolean | no | Whether this zone is muted. |
| <span id="speczones--sleep"></span>`sleep` | integer | no | Minutes until this zone sleeps, and zero means no sleep timer. |

## status

What the receiver last reported, in its own units, plus the protocol's own settings. Only the operator writes it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--zones"></span>`zones` | [map\[string\]object](#statuszones) | no | One entry per zone the receiver reported, keyed by the zone's protocol name. A single-zone receiver reports main. |
| <span id="status--denon"></span>`denon` | object | no | The Denon protocol's own settings, in the receiver's units: the system settings, the tone trims, the Audyssey settings, the audio settings, and the channel volumes. The driver owns this shape, and denon/AGENTS.md documents it. |
| <span id="status--service"></span>`service` | string | no | The Service that represents the receiver on the cluster network after the operator creates it. Empty until then. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Reachable is True only after a recent answered exchange with the receiver, never on an open socket alone. |

### status.zones.*

One entry per zone the receiver reported, keyed by the zone's protocol name. A single-zone receiver reports main.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuszones--power"></span>`power` | string | no | The power state the zone last reported: on, standby, or off. |
| <span id="statuszones--input"></span>`input` | string | no | The input the zone last reported as selected, whether liken selected it or a person did. |
| <span id="statuszones--soundmode"></span>`soundMode` | string | no | The sound mode the zone last reported, such as STEREO or MULTI CH IN. |
| <span id="statuszones--mute"></span>`mute` | boolean | no | Whether the zone last reported itself muted. |
| <span id="statuszones--volume"></span>`volume` | string | no | The zone's volume in the receiver's own scale. A Denon counts 0 to 98 in half steps. |
| <span id="statuszones--volumemax"></span>`volumeMax` | string | no | The last volume limit the receiver sent for this zone, in the same scale. It is what the receiver said and nothing the operator acts on: on a Denon the number moves with the volume. |
| <span id="statuszones--sleep"></span>`sleep` | integer | no | Minutes until the zone sleeps, and zero when no sleep timer stands. |

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
