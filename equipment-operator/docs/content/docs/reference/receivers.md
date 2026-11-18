---
title: Receiver
weight: 10
toc: true
---

<!-- Generated from deploy/receivers-crd.yaml by crdref. Do not edit. -->

# `Receiver`

An A/V receiver that a `liken` machine plays through. The spec says which protocol `equipment-operator` controls it with, which of its inputs `liken` machines are connected to, and which settings to apply. The status has the state that the receiver last reported, and the Player session that uses it.

## spec

How to reach the receiver, how its inputs are wired, and which settings to apply. You write these fields, except `session`, which is for a media operator that does not write `status.session`. The operator also writes `spec.power` after it carries out a power request.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--denon"></span>`denon` | [object](#specdenon) | no | Settings for a receiver that speaks the Denon and Marantz control protocol, which is plain text over TCP port 23. A command such as `MV50` sets the volume, and the receiver sends its events in the same form. |
| <span id="spec--wiim"></span>`wiim` | [object](#specwiim) | no | Settings for a WiiM device, which the operator controls with the LinkPlay HTTP API: a `GET` to `/httpapi.asp?command=<name>` over HTTPS on port 443, with no authentication. The operator drives only a WiiM. Network discovery creates a `Receiver` only for a device whose UPnP description has a `modelName` that begins with `WiiM`, and the driver sends commands only to a device whose `getStatusEx` `project` begins with `WiiM`, in any letter case. To a device of another brand on the LinkPlay platform, the operator sends `getStatusEx` and nothing else. |
| <span id="spec--volume"></span>`volume` | [object](#specvolume) | no | The loudest level that a key press may set, how far one press moves the volume, and which component draws the volume indicator. Levels are in the receiver's own scale. `media-operator` reads `max` and `step` when it turns a press of a volume key into `status.session.volumeAsk`. A Denon requires `max`. |
| <span id="spec--inputs"></span>`inputs` | [\[\]object](#specinputs) | no | The receiver inputs that `liken` machines are connected to. The operator cannot discover this wiring, so you declare it. A receiver presents the same EDID on every input, so each machine connected to it publishes the same `Display` name, and each entry also names its machine. |
| <span id="spec--session"></span>`session` | [object](#specsession) | no | The Player that currently uses the receiver, for a media operator that does not write `status.session`. The operator uses this block only while `status.session` is absent, and applies its fields as `status.session` describes. A media operator applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward. |
| <span id="spec--power"></span>`power` | string | no | The power state that the operator sets on the receiver: `On`, `Standby`, or `Off`. A Denon has no `Off` separate from standby, so both values mean standby. A WiiM has no standby and always reports `On`. Quote the value in YAML, as in `power: "On"`, because kubectl's YAML parser reads an unquoted `On` or `Off` as a boolean, and the API server refuses it. The operator sends each new value once and does not enforce it, so a person who turns the receiver on or off by hand is not overruled, and a GitOps manifest that omits the field leaves the receiver as the operator last set it. When the value changes, including on a `Receiver` created while the operator runs, the operator waits until it has read the receiver's state, and sends the value only when the receiver reports another power. An edit of another field sends nothing. When the operator starts, it sends nothing for this field unless the value differs from `status.settledPower`, because a different value is an edit made while the operator was stopped. With no `status.settledPower`, it records the value it finds and sends nothing. The operator also writes this field after it carries out a `status.session.powerAsk`. One of: `On`, `Standby`, `Off`. |
| <span id="spec--zones"></span>`zones` | [object](#speczones) | no | The receiver's second and third zones, under the keys `zone2` and `zone3`. The main zone is not here, because `spec.power` and `spec.session` control it. The operator applies a zone's controls under the same rules as `spec.denon.settings`: it sends a control only when the zone reports another value, at most 3 times for one declared value, and sends a control that the zone does not report once each time `spec.zones` changes. |

### spec.denon

Settings for a receiver that speaks the Denon and Marantz control protocol, which is plain text over TCP port 23. A command such as `MV50` sets the volume, and the receiver sends its events in the same form.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenon--address"></span>`address` | string | yes | The host name or IP address of the receiver, with an optional port. The default port is 23. |
| <span id="specdenon--settings"></span>`settings` | [object](#specdenonsettings) | no | The receiver's settings in display units, in one block for each group. A key that you omit leaves the receiver unchanged. For each declared field, the operator compares the declared value with the value that the receiver reports, and sends the field only when they differ. So a restart sends nothing to a receiver that already has the declared values, and the operator sets back a change made at the receiver. When the receiver still reports another value after 3 sends of one declared value, the operator stops sending the field, and the `SettingsConfirmed` condition lists it. A new declared value, or a report of the declared value, resets the count. An edit of another field does not. The operator cannot compare a field that the receiver does not report, so it sends that field once each time the spec changes this block. After an operator restart, it sends such a field again only when the block differs from its digest in `status.settledSettings`. |

#### spec.denon.settings

The receiver's settings in display units, in one block for each group. A key that you omit leaves the receiver unchanged. For each declared field, the operator compares the declared value with the value that the receiver reports, and sends the field only when they differ. So a restart sends nothing to a receiver that already has the declared values, and the operator sets back a change made at the receiver. When the receiver still reports another value after 3 sends of one declared value, the operator stops sending the field, and the `SettingsConfirmed` condition lists it. A new declared value, or a report of the declared value, resets the count. An edit of another field does not. The operator cannot compare a field that the receiver does not report, so it sends that field once each time the spec changes this block. After an operator restart, it sends such a field again only when the block differs from its digest in `status.settledSettings`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettings--system"></span>`system` | [object](#specdenonsettingssystem) | no | Settings of the whole receiver. Power is not a setting here, because `spec.power` sets it. |
| <span id="specdenonsettings--tone"></span>`tone` | [object](#specdenonsettingstone) | no | The tone control and the bass and treble trims, in display units where 0dB is neutral. |
| <span id="specdenonsettings--audyssey"></span>`audyssey` | [object](#specdenonsettingsaudyssey) | no | The Audyssey room correction settings. |
| <span id="specdenonsettings--audio"></span>`audio` | [object](#specdenonsettingsaudio) | no | The audio processing settings. |
| <span id="specdenonsettings--hdmi"></span>`hdmi` | [object](#specdenonsettingshdmi) | no | The settings of the receiver's Video > HDMI Setup menu, including its HDMI-CEC switches. |
| <span id="specdenonsettings--channelvolumes"></span>`channelVolumes` | map[string]number | no | One trim per channel, keyed by the channel's name, in display units where 0dB is neutral. |

#### spec.denon.settings.system

Settings of the whole receiver. Power is not a setting here, because `spec.power` sets it.

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

The tone control and the bass and treble trims, in display units where 0dB is neutral.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingstone--control"></span>`control` | boolean | no | Whether the tone trims apply. False bypasses them. |
| <span id="specdenonsettingstone--bass"></span>`bass` | integer | no | The bass trim, from -12dB to 12dB. |
| <span id="specdenonsettingstone--treble"></span>`treble` | integer | no | The treble trim, from -12dB to 12dB. |

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
| <span id="specdenonsettingsaudio--lfe"></span>`lfe` | integer | no | The LFE level, from -10dB to 0dB. |
| <span id="specdenonsettingsaudio--effect"></span>`effect` | integer | no | The effect level. |
| <span id="specdenonsettingsaudio--delay"></span>`delay` | integer | no | The delay, 0 to 999 milliseconds. |
| <span id="specdenonsettingsaudio--audiodelay"></span>`audioDelay` | integer | no | The audio delay, 0 to 999 milliseconds. |
| <span id="specdenonsettingsaudio--subwoofer"></span>`subwoofer` | boolean | no | Whether the subwoofer is on. |
| <span id="specdenonsettingsaudio--restorer"></span>`restorer` | string | no | The Audio Restorer mode: off, low, medium, or high. |
| <span id="specdenonsettingsaudio--graphiceq"></span>`graphicEq` | string | no | The Graphic EQ: off or on. |
| <span id="specdenonsettingsaudio--headphoneeq"></span>`headphoneEq` | string | no | The Headphone EQ: off or on. |
| <span id="specdenonsettingsaudio--speakervirtualizer"></span>`speakerVirtualizer` | boolean | no | Whether the Speaker Virtualizer is on. |
| <span id="specdenonsettingsaudio--dialogenhancer"></span>`dialogEnhancer` | string | no | The Dialog Enhancer level: off, low, mid, or high. |

#### spec.denon.settings.hdmi

The settings of the receiver's Video > HDMI Setup menu, including its HDMI-CEC switches.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdenonsettingshdmi--audioout"></span>`audioOut` | string | no | Where HDMI audio plays: avr for the receiver's own amplifier, or tv. One of: `avr`, `tv`. |
| <span id="specdenonsettingshdmi--passthrough"></span>`passThrough` | boolean | no | Whether HDMI Pass Through sends a source to the TV while the receiver is in standby. |
| <span id="specdenonsettingshdmi--passthroughsource"></span>`passThroughSource` | string | no | The source that passes through in standby: last for the last source selected, or hdmi1 to hdmi7 for one HDMI input jack. The receiver offers only the jacks that have a source assigned. One of: `last`, `hdmi1`, `hdmi2`, `hdmi3`, `hdmi4`, `hdmi5`, `hdmi6`, `hdmi7`. |
| <span id="specdenonsettingshdmi--rcsourceselect"></span>`rcSourceSelect` | string | no | What a source button on the remote does while the receiver is in standby: powerOnAndSource turns the receiver on and selects the source, and sourceSelectOnly selects the source for pass through and leaves the receiver in standby. One of: `powerOnAndSource`, `sourceSelectOnly`. |
| <span id="specdenonsettingshdmi--control"></span>`control` | boolean | no | Whether HDMI Control, the receiver's HDMI-CEC switch, is on. |
| <span id="specdenonsettingshdmi--arc"></span>`arc` | boolean | no | Whether the Audio Return Channel is on. It needs HDMI Control. |
| <span id="specdenonsettingshdmi--tvaudioswitching"></span>`tvAudioSwitching` | boolean | no | Whether the receiver selects the TV input when the TV plays its own sound over HDMI Control. |
| <span id="specdenonsettingshdmi--poweroffcontrol"></span>`powerOffControl` | string | no | Which devices the TV's power off turns off over HDMI Control: all, video, or off. One of: `all`, `video`, `off`. |
| <span id="specdenonsettingshdmi--powersaving"></span>`powerSaving` | boolean | no | Whether HDMI Control's power saving is on. |

### spec.wiim

Settings for a WiiM device, which the operator controls with the LinkPlay HTTP API: a `GET` to `/httpapi.asp?command=<name>` over HTTPS on port 443, with no authentication. The operator drives only a WiiM. Network discovery creates a `Receiver` only for a device whose UPnP description has a `modelName` that begins with `WiiM`, and the driver sends commands only to a device whose `getStatusEx` `project` begins with `WiiM`, in any letter case. To a device of another brand on the LinkPlay platform, the operator sends `getStatusEx` and nothing else.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specwiim--uuid"></span>`uuid` | string | yes | The device's LinkPlay UUID, twelve bytes in hex. It is the `uuid` that `getStatusEx` returns, and the first twelve bytes of the UUID that mDNS and SSDP advertise. It does not change when the device reboots or gets a new DHCP lease. Before the operator sends any command, it reads the device's `uuid` and compares it with this field, so a command never goes to a different device that now has the old address. |
| <span id="specwiim--address"></span>`address` | string | no | The host name or IP address of the device, with an optional port. The default port is 443. While network discovery is on, the field is optional, because discovery finds the address from the `uuid`. When the operator's `EQUIPMENT_NETWORK_DISCOVERY` variable is `off`, you must set it. Without an address from either source, the `Receiver` is unreachable. The operator checks the `uuid` at whichever address it uses. |
| <span id="specwiim--settings"></span>`settings` | [object](#specwiimsettings) | no | The device's own settings, in one block for each group. The operator applies them under the same rules as `spec.denon.settings`: it sends a field only when the device reports another value, at most 3 times for one declared value, and sends a field that the device does not report once each time this block changes. A key that you omit leaves the device unchanged, and an empty block sends nothing. |

#### spec.wiim.settings

The device's own settings, in one block for each group. The operator applies them under the same rules as `spec.denon.settings`: it sends a field only when the device reports another value, at most 3 times for one declared value, and sends a field that the device does not report once each time this block changes. A key that you omit leaves the device unchanged, and an empty block sends nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specwiimsettings--audio"></span>`audio` | [object](#specwiimsettingsaudio) | no | The device's audio settings. |
| <span id="specwiimsettings--device"></span>`device` | [object](#specwiimsettingsdevice) | no | The device's own name and its front-panel controls. |

#### spec.wiim.settings.audio

The device's audio settings.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specwiimsettingsaudio--balance"></span>`balance` | number | no | The left-right balance, -1.0 fully left to 1.0 fully right. |

#### spec.wiim.settings.device

The device's own name and its front-panel controls.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specwiimsettingsdevice--name"></span>`name` | string | no | The device's name. The operator sets it over the API. The `uuid`, not the name, identifies the device. |
| <span id="specwiimsettingsdevice--led"></span>`led` | boolean | no | Whether the status light is on. |
| <span id="specwiimsettingsdevice--buttons"></span>`buttons` | boolean | no | Whether the touch controls are enabled. |

### spec.volume

The loudest level that a key press may set, how far one press moves the volume, and which component draws the volume indicator. Levels are in the receiver's own scale. `media-operator` reads `max` and `step` when it turns a press of a volume key into `status.session.volumeAsk`. A Denon requires `max`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specvolume--max"></span>`max` | number | no | The loudest level that a volume key can set on this receiver, in its own scale. A Denon counts 0 to 98, so `max` is at most 98. `media-operator` writes no `volumeAsk` above `max`, and draws the level on the Player's screen as the reported volume divided by `max`. The receiver's own remote can still go above it. |
| <span id="specvolume--step"></span>`step` | number | no | How far one press moves the volume, in the receiver's own scale. Half steps are allowed. When absent, one press moves one whole unit. |
| <span id="specvolume--indicator"></span>`indicator` | string | no | Which device shows the volume level while this receiver controls a Player's volume. `Player`, the default, means the Player's screens draw their volume bar. `Receiver` means the receiver shows its own overlay on the TV, so the Player's screens draw none. `media-operator` reads this field and sends the level to the screens in both cases. `equipment-operator` does not read it. The field applies to every protocol. One of: `Player`, `Receiver`. Default: `Player`. |

### spec.inputs[]

The receiver inputs that `liken` machines are connected to. The operator cannot discover this wiring, so you declare it. A receiver presents the same EDID on every input, so each machine connected to it publishes the same `Display` name, and each entry also names its machine.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specinputs--name"></span>`name` | string | yes | The input's name on the receiver, as its protocol spells it, such as `MPLAY`, `GAME`, `TV`, or `CBL/SAT`. |
| <span id="specinputs--machine"></span>`machine` | string | yes | The `liken` machine whose HDMI output is connected to this input, by node name. |
| <span id="specinputs--monitor"></span>`monitor` | string | yes | The name of the `Display` that `display-operator` publishes for this machine's output on this cable, such as `don-0070-denon-avr`. With `machine`, it tells the operator which `Display` this input shows, so the operator can find the TV that a session on this input wakes, and the CEC bus that the receiver is on. |
| <span id="specinputs--soundmode"></span>`soundMode` | string | no | An optional sound mode. When a session selects this input, the operator selects this sound mode with it. |

### spec.session

The Player that currently uses the receiver, for a media operator that does not write `status.session`. The operator uses this block only while `status.session` is absent, and applies its fields as `status.session` describes. A media operator applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsession--player"></span>`player` | string | yes | The Player, as `namespace/name`. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="specsession--input"></span>`input` | string | yes | The input that the Player plays through, by its name in `spec.inputs`. |
| <span id="specsession--active"></span>`active` | boolean | no | Same as `status.session.active`. |
| <span id="specsession--awake"></span>`awake` | boolean | no | Same as `status.session.awake`. |
| <span id="specsession--volumeask"></span>`volumeAsk` | [object](#specsessionvolumeask) | no | Same as `status.session.volumeAsk`. |
| <span id="specsession--powerask"></span>`powerAsk` | [object](#specsessionpowerask) | no | Same as `status.session.powerAsk`. |
| <span id="specsession--inputask"></span>`inputAsk` | [object](#specsessioninputask) | no | Same as `status.session.inputAsk`. |

#### spec.session.volumeAsk

Same as `status.session.volumeAsk`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsessionvolumeask--level"></span>`level` | number | yes | The volume in the receiver's own scale, the scale of `spec.volume.max`. A Denon counts 0 to 98 in half steps, and the driver sends a higher level as the top of the scale. |
| <span id="specsessionvolumeask--mute"></span>`mute` | boolean | no | Whether the receiver is muted. The default is not muted. |
| <span id="specsessionvolumeask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

#### spec.session.powerAsk

Same as `status.session.powerAsk`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsessionpowerask--action"></span>`action` | string | yes | `toggle`, `on`, or `off`. Quote `on` and `off` in YAML, as in `action: "on"`, because kubectl's YAML parser reads an unquoted `on` or `off` as a boolean. One of: `toggle`, `on`, `off`. |
| <span id="specsessionpowerask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

#### spec.session.inputAsk

Same as `status.session.inputAsk`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsessioninputask--action"></span>`action` | string | yes | `ensure` for any press, and `show` for a press of the home key. One of: `ensure`, `show`. |
| <span id="specsessioninputask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

### spec.zones

The receiver's second and third zones, under the keys `zone2` and `zone3`. The main zone is not here, because `spec.power` and `spec.session` control it. The operator applies a zone's controls under the same rules as `spec.denon.settings`: it sends a control only when the zone reports another value, at most 3 times for one declared value, and sends a control that the zone does not report once each time `spec.zones` changes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speczones--zone2"></span>`zone2` | [object](#speczoneszone2) | no | The receiver's second zone. Every field is optional, and a key with no fields sends nothing. |
| <span id="speczones--zone3"></span>`zone3` | [object](#speczoneszone3) | no | The receiver's third zone. Every field is optional, and a key with no fields sends nothing. |

#### spec.zones.zone2

The receiver's second zone. Every field is optional, and a key with no fields sends nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speczoneszone2--power"></span>`power` | string | no | The power state that the operator sets on this zone. Quote `On` and `Off` in YAML. One of: `On`, `Standby`, `Off`. |
| <span id="speczoneszone2--input"></span>`input` | string | no | The input that this zone selects, by its name in `spec.inputs`. |
| <span id="speczoneszone2--volume"></span>`volume` | number | no | The zone's volume in the receiver's display units. |
| <span id="speczoneszone2--mute"></span>`mute` | boolean | no | Whether this zone is muted. |
| <span id="speczoneszone2--sleep"></span>`sleep` | integer | no | Minutes until this zone sleeps. Zero means no sleep timer. |

#### spec.zones.zone3

The receiver's third zone. Every field is optional, and a key with no fields sends nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="speczoneszone3--power"></span>`power` | string | no | The power state that the operator sets on this zone. Quote `On` and `Off` in YAML. One of: `On`, `Standby`, `Off`. |
| <span id="speczoneszone3--input"></span>`input` | string | no | The input that this zone selects, by its name in `spec.inputs`. |
| <span id="speczoneszone3--volume"></span>`volume` | number | no | The zone's volume in the receiver's display units. |
| <span id="speczoneszone3--mute"></span>`mute` | boolean | no | Whether this zone is muted. |
| <span id="speczoneszone3--sleep"></span>`sleep` | integer | no | Minutes until this zone sleeps. Zero means no sleep timer. |

## status

The state that the receiver last reported, in its own units, and the protocol's own status. The operator writes it, except `session`, which `media-operator` writes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--address"></span>`address` | string | no | The address where the operator reaches the receiver. For a WiiM it is the declared address, or the address that discovery found when the spec has none. For a Denon it is the declared address, resolved. It is empty until the operator has created the driver. |
| <span id="status--model"></span>`model` | string | no | The receiver's model as its maker names it, such as `WiiM Amp` or `AVR-X1700H`. It tells you which device a `Receiver` controls when the `Receiver`'s name is a discovered UUID. For a WiiM, the operator reads `modelName` from the device's UPnP description on port 49152, after the device has identified itself as a WiiM. For a Denon, it reads `modelName` from the AIOS description on port 60006, which receivers of the AVR-X 2016 generation and later serve. The field is empty until the operator has read it, and stays empty for a receiver that serves no description. |
| <span id="status--manufacturer"></span>`manufacturer` | string | no | The receiver's maker from the same UPnP description, such as `Denon` or `Marantz`. A WiiM reports `Linkplay Technology Inc.`, the maker of its platform, so use `status.model` to tell whether a device is a WiiM. Empty whenever `status.model` is empty. |
| <span id="status--driver"></span>`driver` | string | no | The protocol driver that the operator created from the spec's protocol block: `denon` or `wiim`. Empty until the operator has created the driver. |
| <span id="status--zones"></span>`zones` | [map\[string\]object](#statuszones) | no | One entry for each zone that the receiver reported, keyed by the zone's name in the protocol. A receiver with one zone reports `main`. |
| <span id="status--denon"></span>`denon` | object | no | The Denon receiver's own settings as it reports them, in its units: the system settings, the tone trims, the Audyssey settings, the audio settings, the HDMI setup, and the channel volumes. The driver defines this shape, and `denon/AGENTS.md` documents it. |
| <span id="status--wiim"></span>`wiim` | object | no | The WiiM device's own status, in its units: identity, network, playback, now-playing, audio, equalizer, timers, Bluetooth, presets, and controls. The driver defines this shape, and `wiim/AGENTS.md` documents it. The device sends UPnP events for the volume, the mute state, and the transport state. The operator reads `getStatusEx` and `getPlayerStatus` every 10 seconds, so the identity, the network, and the input are at most 10 seconds old. It reads the track again when the player reports a new one, and reads the sleep timer every 10 seconds while the timer runs. The other groups have no events, so the operator reads them when it starts, after it sends a setting in the group, when it opens a UPnP subscription again, when the device responds again after it was unreachable or after it refused to renew a subscription, and every 30 minutes. So a change made in the WiiM Home app to one of those groups appears here within 30 minutes. The block leaves out values that change on their own between reads: the device's clock, the track position, and the seconds left on the sleep timer, which the zone's `sleep` reports in whole minutes. The temperatures, the Wi-Fi signal, noise, signal-to-noise ratio, and rate, and the remote's battery and signal are in the `equipment_wiim_*` metrics. |
| <span id="status--session"></span>`session` | [object](#statussession) | no | The Player that currently uses the receiver. `media-operator` applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward. The block is in status, so a change of `active` or `awake` does not change `metadata.generation`. The operator reads this block first, uses `spec.session` only while this block is absent, and never writes this block itself. The operator changes the volume only for a `volumeAsk`, and applies each `powerAsk` and `inputAsk` request once. Each time `active` or `awake` changes to `true`, the operator waits until it has read the receiver's state, and then sends the power, the input, and the input's sound mode, each only when the receiver reports another value. A Denon reports a sound mode in other words than the command that selects it, so the operator compares the declared and the reported mode by command family, for the families that the driver knows. A session that appears while the operator runs gets these commands for the flags it starts with. A session that the operator finds in its first pass after a start sends nothing for its flags, because the previous operator already acted on them. The operator does not repeat these commands, so a person can still change the power, the input, or the sound mode with the receiver's remote. |
| <span id="status--cec"></span>`cec` | [object](#statuscec) | no | The receiver's identity on HDMI-CEC and its System Audio Mode, from the device at logical address 5, the audio system, in the `CECBus`'s `status.devices`. The operator writes this block only on a `Receiver` that is the `via` of a `Display` in a `Television`'s `status.displays`. A `Receiver` is a `via` when the bus has an audio system above the `Display` in the HDMI tree, and an input of the `Receiver` names the `Display`'s machine and monitor. The block is absent on any other `Receiver`, such as one that a `Display` reaches only by an optical cable, which carries no CEC. Power and volume are not here, because `status.zones` reports them. While `systemAudioMode` is `On`, the TV sends its remote's volume keys to the receiver over CEC, so `status.zones` can show volume changes that `liken` did not make. Each change of `systemAudioMode` posts a Normal `Event`, `SystemAudioModeOn` or `SystemAudioModeOff`, because the mode also changes without `liken`: the receiver turns it off before standby, and a person turns it off by choosing the TV's speakers. |
| <span id="status--settledpower"></span>`settledPower` | string | no | The `spec.power` value that the operator last settled: it sent the value, found the receiver already at it, or recorded the value it found when it started. A restart that finds `spec.power` at this value sends nothing. One of: `On`, `Standby`, `Off`. |
| <span id="status--settledsettings"></span>`settledSettings` | map[string]string | no | A digest of each declared block that the operator has sent, keyed by the block's path in the spec: `spec.denon.settings`, `spec.wiim.settings`, or `spec.zones`. After a restart, the operator sends a block's unreported fields again only when the block's digest differs from the one here. So a restart sends nothing for a block that nobody changed, and sends the unreported fields once for a new `Receiver` or for a block edited while the operator was stopped. |
| <span id="status--powergeneration"></span>`powerGeneration` | integer | no | A field that an earlier version of the operator wrote: the `metadata.generation` whose `spec.power` it had settled. The operator does not read or write it, and its first status apply removes it. |
| <span id="status--settingsgeneration"></span>`settingsGeneration` | integer | no | A field that an earlier version of the operator wrote: the `metadata.generation` whose declared settings it had sent. When the operator starts and finds this field with no `settledSettings`, it records each declared block as sent and sends nothing for it. The operator does not write this field, and its first status apply removes it. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Reachable` is `True` only when the receiver has recently answered a request. An open socket alone does not count. `SettingsConfirmed` is `False` with reason `NotConfirmed` while a declared setting or zone control has been sent 3 times at its declared value and the receiver still reports another value. Its message lists each such field by its path in the spec. The condition is absent otherwise. `InputSelected` is present while a session exists. It is `True` with reason `SessionInput` while the receiver reports the session's input, `False` with reason `OtherInput` while it reports another input, and `Unknown` with reason `Unreachable` or `NoInputReported` while the operator cannot reach the receiver or the receiver has reported no input. |

### status.zones.*

One entry for each zone that the receiver reported, keyed by the zone's name in the protocol. A receiver with one zone reports `main`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuszones--power"></span>`power` | string | no | The power state that the zone last reported: `On`, `Standby`, or `Off`. A WiiM has no standby, so it always reports `On`. |
| <span id="statuszones--input"></span>`input` | string | no | The input that the zone last reported, whether `liken` or a person selected it. |
| <span id="statuszones--soundmode"></span>`soundMode` | string | no | The sound mode that the zone last reported, such as `STEREO` or `MULTI CH IN`. |
| <span id="statuszones--mute"></span>`mute` | boolean | no | Whether the zone last reported itself muted. |
| <span id="statuszones--volume"></span>`volume` | string | no | The zone's volume in the receiver's own scale. A Denon counts 0 to 98 in half steps. |
| <span id="statuszones--volumemax"></span>`volumeMax` | string | no | The volume limit that the receiver last reported for this zone, in the same scale. The operator does not act on it, because a Denon's limit changes with the volume. |
| <span id="statuszones--sleep"></span>`sleep` | integer | no | Minutes until the zone sleeps, and zero when no sleep timer is set. |

### status.session

The Player that currently uses the receiver. `media-operator` applies this block with its own field manager while the Player has a screen on this receiver, and removes it afterward. The block is in status, so a change of `active` or `awake` does not change `metadata.generation`. The operator reads this block first, uses `spec.session` only while this block is absent, and never writes this block itself. The operator changes the volume only for a `volumeAsk`, and applies each `powerAsk` and `inputAsk` request once. Each time `active` or `awake` changes to `true`, the operator waits until it has read the receiver's state, and then sends the power, the input, and the input's sound mode, each only when the receiver reports another value. A Denon reports a sound mode in other words than the command that selects it, so the operator compares the declared and the reported mode by command family, for the families that the driver knows. A session that appears while the operator runs gets these commands for the flags it starts with. A session that the operator finds in its first pass after a start sends nothing for its flags, because the previous operator already acted on them. The operator does not repeat these commands, so a person can still change the power, the input, or the sound mode with the receiver's remote.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussession--player"></span>`player` | string | yes | The Player, as `namespace/name`. Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?/[a-z0-9]([-a-z0-9]*[a-z0-9])?$`. |
| <span id="statussession--input"></span>`input` | string | yes | The input that the Player plays through, by its name in `spec.inputs`. |
| <span id="statussession--active"></span>`active` | boolean | no | Whether a Play is present on the Player. The default is `false`. When it changes to `true`, the operator turns the receiver on and selects the input, once, each only when the receiver reports another value. A change to `false` sends no power or input command, and `volumeAsk` still applies. `awake` triggers the same commands on its own. |
| <span id="statussession--awake"></span>`awake` | boolean | no | Whether the Player's screen is on. `media-operator` sets it to `false` while the Player's `spec.power` is `Off`. The default is `false`. A change to `true` triggers the same one-time power and input commands as `active`, whatever the value of `active`. A change to `false` sends no command to the receiver. |
| <span id="statussession--volumeask"></span>`volumeAsk` | [object](#statussessionvolumeask) | no | A request for an absolute volume and mute state, which `media-operator` writes when someone presses a volume key. The operator applies each request once, when `at` changes. It sends `level` as the volume and `mute` as the mute state, each only when the receiver reports another value. When requests arrive faster than the receiver reports the volume it was last sent, the operator sends only the newest, as soon as the receiver reports the last volume or 1 second after it sent that volume, whichever comes first. A request that the operator finds in its first pass after a start sends nothing, because someone may have turned the receiver's knob since the request was made. The operator does not enforce the request, so the receiver's remote can still change the volume. The operator does not check `level` against `spec.volume.max`. `media-operator` keeps its requests at or below that ceiling. |
| <span id="statussession--powerask"></span>`powerAsk` | [object](#statussessionpowerask) | no | A request to turn the Player's receiver and TV on or off. `media-operator` writes `off` when its Player's `spec.power` changes to `Off`. The operator applies each request once, when `at` changes, and a request that it finds in its first pass after a start sends nothing. `toggle` turns the equipment off when it is on, and on when it is off. `on` and `off` act only when the equipment is not in that state already, because HDMI-CEC 1.3a, section CEC 13.13.3, defines each to leave the device in its state when it is pressed again. To decide whether the equipment is on, the operator uses the TV's power when the session's input is a `Display` that a `Television` lists: `On` or `ToOn` is on, and any other power is off. Nothing reads the TV's power on a timer. So when an adapter in `Control` mode finds the `Television`'s TV, and its `Reachable` condition is `True`, or `False` with the reason `NoPower`, the operator first requests a read through the `Television`'s `status.session.powerReadAt`. It waits up to 3 seconds for `status.powerRead`, and uses the last `status.power` when no answer arrives. When there is no such `Television`, or the TV does not answer and has reported no power, the operator uses the receiver's power. To turn off, the operator requests standby through the `Television`'s `status.session.standbyAt` and puts the receiver in standby. A receiver with no standby command, such as a WiiM, stays on, and the operator logs that. To turn on, the operator wakes the `Television`, selects the session's input, and turns the receiver on. When the operator cannot reach the receiver, it sends the receiver nothing and logs that. The TV still turns on or off if it has reported its power, and the request is dropped if it has not. After the receiver changes, the operator sets `spec.power` to match. |
| <span id="statussession--inputask"></span>`inputAsk` | [object](#statussessioninputask) | no | A request to select the session's input, which `media-operator` writes when someone presses a controller key. The operator applies each request once, when `at` changes, and a request that it finds in its first pass after a start sends nothing. `ensure` sends the session's input and that input's sound mode, only when the receiver is on and reports another input. `show` does the same, and also asks the `Television` that shows the session's input to show the session's `Display`, through the `Television`'s `status.session.showAt`. The TV receives Image View On and Active Source only when it reports `On` and the Player's screen is on. Neither action turns the receiver on. `media-operator` reads the `InputSelected` condition to decide whether a press needs an `ensure`. |

#### status.session.volumeAsk

A request for an absolute volume and mute state, which `media-operator` writes when someone presses a volume key. The operator applies each request once, when `at` changes. It sends `level` as the volume and `mute` as the mute state, each only when the receiver reports another value. When requests arrive faster than the receiver reports the volume it was last sent, the operator sends only the newest, as soon as the receiver reports the last volume or 1 second after it sent that volume, whichever comes first. A request that the operator finds in its first pass after a start sends nothing, because someone may have turned the receiver's knob since the request was made. The operator does not enforce the request, so the receiver's remote can still change the volume. The operator does not check `level` against `spec.volume.max`. `media-operator` keeps its requests at or below that ceiling.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionvolumeask--level"></span>`level` | number | yes | The volume in the receiver's own scale, the scale of `spec.volume.max`. A Denon counts 0 to 98 in half steps, and the driver sends a higher level as the top of the scale. |
| <span id="statussessionvolumeask--mute"></span>`mute` | boolean | no | Whether the receiver is muted. The default is not muted. |
| <span id="statussessionvolumeask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

#### status.session.powerAsk

A request to turn the Player's receiver and TV on or off. `media-operator` writes `off` when its Player's `spec.power` changes to `Off`. The operator applies each request once, when `at` changes, and a request that it finds in its first pass after a start sends nothing. `toggle` turns the equipment off when it is on, and on when it is off. `on` and `off` act only when the equipment is not in that state already, because HDMI-CEC 1.3a, section CEC 13.13.3, defines each to leave the device in its state when it is pressed again. To decide whether the equipment is on, the operator uses the TV's power when the session's input is a `Display` that a `Television` lists: `On` or `ToOn` is on, and any other power is off. Nothing reads the TV's power on a timer. So when an adapter in `Control` mode finds the `Television`'s TV, and its `Reachable` condition is `True`, or `False` with the reason `NoPower`, the operator first requests a read through the `Television`'s `status.session.powerReadAt`. It waits up to 3 seconds for `status.powerRead`, and uses the last `status.power` when no answer arrives. When there is no such `Television`, or the TV does not answer and has reported no power, the operator uses the receiver's power. To turn off, the operator requests standby through the `Television`'s `status.session.standbyAt` and puts the receiver in standby. A receiver with no standby command, such as a WiiM, stays on, and the operator logs that. To turn on, the operator wakes the `Television`, selects the session's input, and turns the receiver on. When the operator cannot reach the receiver, it sends the receiver nothing and logs that. The TV still turns on or off if it has reported its power, and the request is dropped if it has not. After the receiver changes, the operator sets `spec.power` to match.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionpowerask--action"></span>`action` | string | yes | `toggle`, `on`, or `off`. Quote `on` and `off` in YAML, as in `action: "on"`, because kubectl's YAML parser reads an unquoted `on` or `off` as a boolean. One of: `toggle`, `on`, `off`. |
| <span id="statussessionpowerask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

#### status.session.inputAsk

A request to select the session's input, which `media-operator` writes when someone presses a controller key. The operator applies each request once, when `at` changes, and a request that it finds in its first pass after a start sends nothing. `ensure` sends the session's input and that input's sound mode, only when the receiver is on and reports another input. `show` does the same, and also asks the `Television` that shows the session's input to show the session's `Display`, through the `Television`'s `status.session.showAt`. The TV receives Image View On and Active Source only when it reports `On` and the Player's screen is on. Neither action turns the receiver on. `media-operator` reads the `InputSelected` condition to decide whether a press needs an `ensure`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessioninputask--action"></span>`action` | string | yes | `ensure` for any press, and `show` for a press of the home key. One of: `ensure`, `show`. |
| <span id="statussessioninputask--at"></span>`at` | string | yes | When the request was made, as an RFC 3339 time with milliseconds. A new value is a new request. |

### status.cec

The receiver's identity on HDMI-CEC and its System Audio Mode, from the device at logical address 5, the audio system, in the `CECBus`'s `status.devices`. The operator writes this block only on a `Receiver` that is the `via` of a `Display` in a `Television`'s `status.displays`. A `Receiver` is a `via` when the bus has an audio system above the `Display` in the HDMI tree, and an input of the `Receiver` names the `Display`'s machine and monitor. The block is absent on any other `Receiver`, such as one that a `Display` reaches only by an optical cable, which carries no CEC. Power and volume are not here, because `status.zones` reports them. While `systemAudioMode` is `On`, the TV sends its remote's volume keys to the receiver over CEC, so `status.zones` can show volume changes that `liken` did not make. Each change of `systemAudioMode` posts a Normal `Event`, `SystemAudioModeOn` or `SystemAudioModeOff`, because the mode also changes without `liken`: the receiver turns it off before standby, and a person turns it off by choosing the TV's speakers.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscec--bus"></span>`bus` | string | yes | The `CECBus` that the receiver is the audio system of. |
| <span id="statuscec--logicaladdress"></span>`logicalAddress` | integer | yes | The receiver's logical address, which is 5 for an audio system. |
| <span id="statuscec--physicaladdress"></span>`physicalAddress` | string | no | The receiver's position in the HDMI tree, in dotted form such as `1.0.0.0`. |
| <span id="statuscec--osdname"></span>`osdName` | string | no | The name that the receiver reports on the bus, at most 14 characters. |
| <span id="statuscec--vendor"></span>`vendor` | string | no | The receiver's IEEE OUI on the bus, as six hex digits. A Device Vendor ID of `000000` means no vendor, so it never replaces a vendor that the device reported earlier, and the field is absent until the device reports a real one. |
| <span id="statuscec--cecversion"></span>`cecVersion` | string | no | The CEC version that the receiver reports: `1.3a`, `1.4`, or `2.0`. |
| <span id="statuscec--systemaudiomode"></span>`systemAudioMode` | string | no | The System Audio Mode that the receiver last reported. While it is `On`, the TV mutes its own speakers and sends its remote's volume keys to the receiver (HDMI-CEC 1.3a, section CEC 13.15.2). When the operator wakes the TV through this receiver, it requests `On`, and the receiver broadcasts `Off` before standby. Absent until an adapter has read or heard the mode. One of: `On`, `Off`. |
| <span id="statuscec--systemaudiomodeat"></span>`systemAudioModeAt` | string | no | When an adapter of the bus last read or heard `systemAudioMode`. |

### status.conditions[]

`Reachable` is `True` only when the receiver has recently answered a request. An open socket alone does not count. `SettingsConfirmed` is `False` with reason `NotConfirmed` while a declared setting or zone control has been sent 3 times at its declared value and the receiver still reports another value. Its message lists each such field by its path in the spec. The condition is absent otherwise. `InputSelected` is present while a session exists. It is `True` with reason `SessionInput` while the receiver reports the session's input, `False` with reason `OtherInput` while it reports another input, and `Unknown` with reason `Unreachable` or `NoInputReported` while the operator cannot reach the receiver or the receiver has reported no input.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's type, in CamelCase, such as Reachable. It is the key of this list. Pattern: `^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The condition's status. True is the healthy status, and Unknown means the operator cannot tell yet. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The metadata.generation that this condition reflects. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One CamelCase word for why the condition has this status, for a program to match on. Pattern: `^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`. |
| <span id="statusconditions--message"></span>`message` | string | no | The same reason, as a sentence for a person to read. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes | When the status last changed. A change of the reason or the message alone does not move it. |
