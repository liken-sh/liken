---
title: Sinks
weight: 20
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `Sink`

A `Sink` is one playback endpoint as a Kubernetes resource: an
analog jack, an HDMI or DisplayPort output, the playback side of a USB
card, or a Bluetooth speaker. The operator creates a `Sink` for each
endpoint it publishes. A `Sink` is cluster-scoped, like a `Node`, and
has the same name as the device in the `ResourceSlice`. You never
create one, and you delete one only after its card has gone. The
operator writes `status`, which has the endpoint's location, the
controls that the card declares, and the values that the operator last
read. media-operator writes `status.session`, which has the last
volume request from a remote's volume key. You write `spec`, the
settings you want for the endpoint.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Sink
metadata:
  name: node-1-usb-0573-1573-a34004801402-usb-audio
spec:
  volume:
    level: 80
    max: 100
    step: 5
  controls:
    PCM Playback Volume: "120"
status:
  node: node-1
  location: "1-6"
  connectionType: usb
  card:
    number: 1
    id: HID
    driver: USB-Audio
    name: USB Audio and HID
  pcm:
    device: 0
    id: USB Audio
  nodeName: liken.audio.card1-pcm0
  layoutSource: ChannelMap
  capabilities:
    PCM Playback Volume:
      type: integer
      min: 0
      max: 127
      minDecibels: "-63.50"
      maxDecibels: "0.00"
      channels: 2
    PCM Playback Switch:
      type: boolean
      channels: 1
  observed:
    volume: 80
    mute: false
    controls:
      PCM Playback Volume: "120"
      PCM Playback Switch: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Ready
      status: "True"
    - type: LayoutApplied
      status: "True"
```

A pod that plays sound still gets the output through a claim, as the
[claim guide](/docs/guides/claim/) shows, and it keeps its own stream
volume. The `Sink` covers everything else about the output: its
default level, its mute, and the card's own controls. Because a `Sink`
is an ordinary Kubernetes resource, anything with the right RBAC can
change it. A pod that only needs to mute one speaker needs no claim,
and a rule that lowers every speaker at night is one patch for each
`Sink`. [Set endpoint volume and controls](/docs/guides/rest/) walks
through it.

A `Sink` is one playback endpoint: an analog jack, an HDMI or DisplayPort output, the playback side of a USB card, or a Bluetooth speaker. `spec` has the settings you want for the endpoint, and `status` has the hardware facts and the values that the operator last read. The operator builds the name from the machine's name and the card's identity, as the section "The name" of the `Sink` reference describes. When a USB card's name would be longer than 63 characters, the name has the first eight hex digits of the SHA-256 of the serial in place of the serial.

## spec

The settings you want for the endpoint. Every field is optional. The operator writes `volume.level` and `mute` to the endpoint only when you change them and when the endpoint appears, for example when a speaker reconnects or PipeWire creates the node again. Between those times, a level set at the device stays, and the operator reports it in `status.observed`. The operator writes a declared control or codec to the hardware whenever the hardware differs from it. It never writes a setting that `spec` leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--volume"></span>`volume` | [object](#specvolume) | no | The endpoint's volume, as a percent of unity gain, where 100 is unity. The operator writes `level` to the endpoint. `max` and `step` control the volume requests that media-operator writes to `status.session.volumeAsk`, and this operator does not read them. |
| <span id="spec--mute"></span>`mute` | boolean | no | `true` silences the endpoint, and `false` unmutes it. The operator writes it at the same times as `volume.level`. |
| <span id="spec--controls"></span>`controls` | map[string]string | no | The card's own controls, keyed by the kernel's control name as `status.capabilities` lists it, for example `Master Playback Volume`. An integer control takes a number within its range, a boolean control takes `on` or `off`, and an enumerated control takes one of its values. The operator writes a control only when `spec` lists it. When two endpoints share one control, the last write wins, because the hardware has one register. |
| <span id="spec--layout"></span>`layout` | []string | no | The channel position of each PCM slot of an ALSA sink, in slot order, in PipeWire's channel names such as `FL`, `FR`, `FC`, `LFE`, `RL`, `RR`, `SL`, and `SR`. `NA` marks a slot that plays nothing. For a 7.1 receiver on HDMI, write `[FL, FR, RL, RR, FC, LFE, SL, SR]`. When this field is absent, the operator reads the layout from the monitor's ELD on HDMI and DisplayPort and from the device's channel map on USB. On the analog jack it declares no positions, so streams there play in stereo. The number of positions must be a channel count that the device accepts, or PipeWire ignores the layout. A Bluetooth speaker ignores this field. |
| <span id="spec--headroom"></span>`headroom` | [object](#specheadroom) | no | Extra audio that PipeWire keeps queued on an ALSA sink's card, so that the card does not run out of samples when PipeWire's estimate of the card's position is off. Set it on a sink whose `pipewire` container logs `XRun!` lines for it. Every stream on the sink then plays later by the headroom, so leave it absent on a sink that has no underruns. When this field is absent, PipeWire keeps no headroom. A change waits until no stream plays on the machine, restarts PipeWire, and `LayoutApplied` reports it as it reports a layout change. A Bluetooth speaker ignores this field. |
| <span id="spec--sharedclaims"></span>`sharedClaims` | integer | no | How many claims can hold the endpoint at the same time, at least 2. When this field is absent, one claim holds the endpoint, and a second claim stays `Pending` until the first ends. When it is set, the operator publishes the device in the `ResourceSlice` with multiple allocations allowed and a capacity named `claims` of this value. A claim whose request asks for `claims: 1` shares the endpoint with the other claims that ask for one, up to this number, and PipeWire mixes their streams. A claim that asks for no `claims` capacity gets all of it and holds the endpoint alone. A change applies to the next allocation, and the claims that already hold the endpoint keep it. Neither the hardware nor PipeWire limits how many streams one endpoint mixes, so the number is yours to choose. A shared claim cannot set a codec, because a codec switch replaces the speaker's node under every claim that plays through it. |
| <span id="spec--codec"></span>`codec` | string | no | The A2DP codec that a Bluetooth speaker uses when no claim holds it. It must be one of `status.bluetooth.codecs`. While a claim holds the speaker, the claim's `codec` parameter takes precedence. A change waits until the claim ends, because switching codecs replaces the speaker's node and interrupts playback. The operator ignores this field on an ALSA endpoint. |

### spec.volume

The endpoint's volume, as a percent of unity gain, where 100 is unity. The operator writes `level` to the endpoint. `max` and `step` control the volume requests that media-operator writes to `status.session.volumeAsk`, and this operator does not read them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specvolume--level"></span>`level` | integer | no | The default level, as a percent of unity, applied to every channel alike. On an ALSA endpoint it is a software gain in PipeWire. On a Bluetooth speaker it is the speaker's own volume, sent over AVRCP when the speaker supports absolute volume, and a software gain when it does not. The operator writes it when it changes and when the endpoint appears, whether or not a claim holds the endpoint. A pod's own stream volume applies on top of this level. When the level later changes at the device or by a volume request, the operator does not write this value back, and `status.observed.volume` reports the new level. When this field is absent, a node that PipeWire creates while the operator runs starts at unity. |
| <span id="specvolume--max"></span>`max` | integer | no | The highest level, as a percent of unity, that a volume request can set. The default is 100. media-operator keeps its requests at or below this value. Someone at the device can still set a higher level. Default: `100`. |
| <span id="specvolume--step"></span>`step` | integer | no | How many percentage points one press of a remote's volume key raises or lowers the level that media-operator requests. The default is 5. Default: `5`. |

### spec.headroom

Extra audio that PipeWire keeps queued on an ALSA sink's card, so that the card does not run out of samples when PipeWire's estimate of the card's position is off. Set it on a sink whose `pipewire` container logs `XRun!` lines for it. Every stream on the sink then plays later by the headroom, so leave it absent on a sink that has no underruns. When this field is absent, PipeWire keeps no headroom. A change waits until no stream plays on the machine, restarts PipeWire, and `LayoutApplied` reports it as it reports a layout change. A Bluetooth speaker ignores this field.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specheadroom--samples"></span>`samples` | integer | yes | The number of samples, at the rate the sink runs, that PipeWire keeps queued on the card beyond what it needs. 4096 samples is 21 ms of a bitstream at 192 kHz and 85 ms of PCM at 48 kHz. `0` is the same as no headroom. |

## status

The hardware facts and the values that the operator last read. The operator writes every field here except `session`, which media-operator writes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--session"></span>`session` | [object](#statussession) | no | The `Player` that uses the `Sink`, and the last volume request from a remote's key. media-operator writes this block by server-side apply under its own field manager. This operator writes the rest of `status` with a merge patch that never includes this block, so neither writer removes the other's fields. The block is in `status` because no person declares it, and a status write does not change `metadata.generation`. |
| <span id="status--node"></span>`node` | string | no | The machine that has the endpoint now. For a Bluetooth speaker, the value changes when the speaker moves to another machine. The name of every other `Sink` starts with this machine's name, so a card that moves to another machine gets a new `Sink`. The operator on each machine lists and watches only the `Sink`s whose `status.node` is that machine. |
| <span id="status--location"></span>`location` | string | no | Where the card is on the machine, in the kernel's notation: a PCI address such as `0000:00:1f.3`, or a USB port path such as `1-6`. Absent on a Bluetooth speaker. |
| <span id="status--connectiontype"></span>`connectionType` | string | no | The way sound leaves the machine. One of: `analog`, `hdmi`, `displayport`, `usb`, `bluetooth`. |
| <span id="status--card"></span>`card` | [object](#statuscard) | no | The ALSA card that the endpoint is on. The kernel assigns the number and the id at each boot, and adding a second card can change both, so the endpoint's name does not use them. |
| <span id="status--pcm"></span>`pcm` | [object](#statuspcm) | no | The PCM device that the endpoint plays through. |
| <span id="status--monitor"></span>`monitor` | [object](#statusmonitor) | no | The monitor that an HDMI or DisplayPort output feeds, from the ELD that the graphics driver wrote into the card. Absent while the output has no ELD, and on every other connection type. An Intel HDMI codec binds a pin to the first free output when a monitor appears, so on a card with two monitors the monitors can swap outputs between plug events. This object shows which monitor the output feeds now. |
| <span id="status--bluetooth"></span>`bluetooth` | [object](#statusbluetooth) | no | The speaker behind a Bluetooth endpoint. Absent on every other connection type. |
| <span id="status--nodename"></span>`nodeName` | string | no | The name of the PipeWire node that a pod's playback streams target. A prepared claim gives the pod the same value in `PIPEWIRE_NODE`. Absent while PipeWire has no node for the endpoint. |
| <span id="status--format"></span>`format` | [object](#statusformat) | no | The format that the node runs at, from PipeWire's `Format` parameter. Absent while the node is not running. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The card's own controls for this endpoint, keyed by the kernel's control name. A `Playback` control applies to the card's analog and USB sinks, and so does a control with no direction, such as `Auto-Mute Mode`. An `IEC958 Playback Switch` applies to the HDMI output with the same number, and an HDMI output lists no other control, because an HDMI PCM has no volume element. Jack controls are left out, because they are read-only and the operator reads them for the `Connected` condition. A Bluetooth speaker lists no card controls. |
| <span id="status--layout"></span>`layout` | []string | no | The channel positions that the operator declares the sink's node with, in PCM slot order. Absent when `layoutSource` is `ChannelMap`, because PipeWire reads the positions from the device, and absent when it is `None`. While `LayoutApplied` is `False` with the reason `Restarting`, this is the layout that PipeWire loads when it restarts. |
| <span id="status--layoutsource"></span>`layoutSource` | string | no | Where the layout comes from. `Spec` is `spec.layout`. `ELD` is the monitor's speaker allocation, limited to the largest LPCM channel count that the monitor accepts. `ChannelMap` is the channel map that a USB device describes. `None` means no positions, and a multichannel stream then plays in stereo. An HDMI sink keeps its ELD layout while its monitor is off. Absent on a Bluetooth speaker. One of: `Spec`, `ELD`, `ChannelMap`, `None`. |
| <span id="status--passthrough"></span>`passthrough` | []string | no | The encoded formats that the sink can send to its monitor without decoding them, in PipeWire's names. `AC3` is Dolby Digital, `EAC3` is Dolby Digital Plus, and `TrueHD` and `DTS-HD` are the lossless formats. Dolby Atmos is inside `EAC3` or `TrueHD`, and DTS:X is inside `DTS-HD`. The operator declares the sink with the formats that the monitor's ELD lists, so a player can send a track in one of these formats as a bitstream, and must decode every other track to PCM. Absent when the monitor lists none, on an output of a card whose driver is not `HDA-Intel`, and on every analog, USB, and Bluetooth sink. An HDMI sink keeps its formats while its monitor is off. A change of the formats is a layout change, and `LayoutApplied` reports it in the same way. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value that the operator read for each setting. The operator reads the card's control device on each control event, and PipeWire's graph on each change that PipeWire reports, so a change at a knob or by a client appears here without waiting for a timer. |
| <span id="status--applied"></span>`applied` | [object](#statusapplied) | no | The node that the operator last wrote the declared volume and mute to, and the values that it wrote. When the operator starts, a node whose PipeWire cookie and node id match this record kept running through the restart. The operator keeps that node's level, and writes the declaration only when it differs from the one recorded here. Any other node is one that PipeWire created since, for example after the machine boots, so the operator writes the declaration to it, or unity when the sink declares no level. Absent while the endpoint has no node. |
| <span id="status--claims"></span>`claims` | [\[\]object](#statusclaims) | no | The claims that hold the endpoint now, sorted by namespace and name, so you can see which workloads use the speaker. The list has one claim, or up to `spec.sharedClaims` claims on a shared endpoint, and it is absent when no claim holds the endpoint. The operator records a claim when the kubelet prepares it on this machine, and keeps that record only in memory. So after the operator restarts, a running pod's claim is missing here until the kubelet prepares it again. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The conditions of the endpoint. `Connected` is `True` when the endpoint can play now: a monitor is on the HDMI output, a plug is in the analog jack, the speaker is connected, or the endpoint is USB, which is always connected. The operator treats a monitor as gone only after its ELD has been empty for 5 seconds, so a shorter drop of hot plug detect, such as an A/V receiver in standby causes, changes neither condition. `Ready` is `True` when PipeWire has a node for the endpoint. `Connected` and `Ready` show a person what the device's taints in the `ResourceSlice` tell the scheduler. `LayoutApplied`, on a sink of a sound card, is `True` when PipeWire runs the sink with the layout and the encoded formats that the operator selected. It is `False` with the reason `AwaitingIdle` while a new layout waits for every stream on the machine to end, and with the reason `Restarting` while PipeWire restarts to load it. |

### status.session

The `Player` that uses the `Sink`, and the last volume request from a remote's key. media-operator writes this block by server-side apply under its own field manager. This operator writes the rest of `status` with a merge patch that never includes this block, so neither writer removes the other's fields. The block is in `status` because no person declares it, and a status write does not change `metadata.generation`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussession--player"></span>`player` | string | no | The `Player` that uses the `Sink`, as `namespace/name`. |
| <span id="statussession--volumeask"></span>`volumeAsk` | [object](#statussessionvolumeask) | no | The last volume request, from a press of a remote's volume key. The operator applies each new request once, with the same write as `spec.volume.level`. When several requests arrive before the operator applies one, it applies only the newest. It does not apply the request that it finds the first time it reads the `Sink`, for example in its first pass after a start, and it drops a request for an endpoint that has no node. It never writes a request's level again, so a later press of the speaker's own button stays, and `status.observed.volume` reports the level that the endpoint plays at. |

#### status.session.volumeAsk

The last volume request, from a press of a remote's volume key. The operator applies each new request once, with the same write as `spec.volume.level`. When several requests arrive before the operator applies one, it applies only the newest. It does not apply the request that it finds the first time it reads the `Sink`, for example in its first pass after a start, and it drops a request for an endpoint that has no node. It never writes a request's level again, so a later press of the speaker's own button stays, and `status.observed.volume` reports the level that the endpoint plays at.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussessionvolumeask--level"></span>`level` | integer | yes | The requested level, as a percent of unity. |
| <span id="statussessionvolumeask--mute"></span>`mute` | boolean | no | Whether the request mutes the endpoint. Absent means not muted. |
| <span id="statussessionvolumeask--at"></span>`at` | string | yes | When the key was pressed, with millisecond precision. Each new time is a new request. |

### status.card

The ALSA card that the endpoint is on. The kernel assigns the number and the id at each boot, and adding a second card can change both, so the endpoint's name does not use them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscard--number"></span>`number` | integer | no | The card number that the kernel assigned in this boot. |
| <span id="statuscard--id"></span>`id` | string | no | The card's short id, such as `PCH`, with the suffix that the kernel appends when two cards have the same id, such as `PCH_1`. |
| <span id="statuscard--driver"></span>`driver` | string | no | The kernel driver of the card, such as `HDA-Intel`. |
| <span id="statuscard--name"></span>`name` | string | no | The card's name as its driver reports it, such as `HDA Intel PCH`. |

### status.pcm

The PCM device that the endpoint plays through.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspcm--device"></span>`device` | integer | no | The PCM device number on the card, in this boot. |
| <span id="statuspcm--id"></span>`id` | string | no | The driver's name for the PCM, such as `HDMI 0` or `USB Audio`. The endpoint's name uses this id, because it stays the same when the card number changes. |

### status.monitor

The monitor that an HDMI or DisplayPort output feeds, from the ELD that the graphics driver wrote into the card. Absent while the output has no ELD, and on every other connection type. An Intel HDMI codec binds a pin to the first free output when a monitor appears, so on a card with two monitors the monitors can swap outputs between plug events. This object shows which monitor the output feeds now.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmonitor--display"></span>`display` | string | no | The name of the `Display` that display-operator publishes for the same monitor. The name is the monitor's pairing identity, `monitor.liken.sh/id`. |
| <span id="statusmonitor--manufacturer"></span>`manufacturer` | string | no | The monitor's three-letter PNP id, such as `GSM`. |
| <span id="statusmonitor--product"></span>`product` | string | no | The monitor's product code, as four lowercase hexadecimal digits. |
| <span id="statusmonitor--name"></span>`name` | string | no | The monitor's name, from the EDID descriptor that the `Display` reports as `model`. |

### status.bluetooth

The speaker behind a Bluetooth endpoint. Absent on every other connection type.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbluetooth--address"></span>`address` | string | no | The speaker's address, in uppercase with colons. |
| <span id="statusbluetooth--name"></span>`name` | string | no | The name that the speaker reports for itself. |
| <span id="statusbluetooth--peripheral"></span>`peripheral` | string | no | The name of the `Peripheral` that bluetooth-operator publishes for this speaker, which is the address in lowercase with dashes. |
| <span id="statusbluetooth--codec"></span>`codec` | string | no | The A2DP codec that the transport negotiated. Present while the speaker is connected. |
| <span id="statusbluetooth--codecs"></span>`codecs` | []string | no | Every codec that both the speaker and the operator's image support, with the codec that is playing first. |

### status.format

The format that the node runs at, from PipeWire's `Format` parameter. Absent while the node is not running.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusformat--rate"></span>`rate` | integer | no | The sample rate in hertz. |
| <span id="statusformat--channels"></span>`channels` | integer | no | The number of channels. |
| <span id="statusformat--positions"></span>`positions` | []string | no | The channel positions in order, in PipeWire's names, such as `FL`, `FR`, `FC`, `LFE`, `RL`, `RR`. |

### status.capabilities.*

The card's own controls for this endpoint, keyed by the kernel's control name. A `Playback` control applies to the card's analog and USB sinks, and so does a control with no direction, such as `Auto-Mute Mode`. An `IEC958 Playback Switch` applies to the HDMI output with the same number, and an HDMI output lists no other control, because an HDMI PCM has no volume element. Jack controls are left out, because they are read-only and the operator reads them for the `Connected` condition. A Bluetooth speaker lists no card controls.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--type"></span>`type` | string | no | The kind of value that the control takes. One of: `integer`, `boolean`, `enumerated`. |
| <span id="statuscapabilities--min"></span>`min` | integer | no | The smallest number that an integer control accepts. |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number that an integer control accepts. |
| <span id="statuscapabilities--step"></span>`step` | integer | no | The difference between neighboring numbers that an integer control accepts. Absent when every number in the range is accepted. |
| <span id="statuscapabilities--mindecibels"></span>`minDecibels` | string | no | The level at `min`, in decibels, as a decimal string such as `-65.25`, when the control declares one. A string keeps the value exact, and a float would not. |
| <span id="statuscapabilities--maxdecibels"></span>`maxDecibels` | string | no | The level at `max`, in decibels, when the control declares one. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value that an enumerated control accepts. |
| <span id="statuscapabilities--channels"></span>`channels` | integer | no | The number of channels that the control has. A write from `spec` sets every channel to the same value. |

### status.observed

The last value that the operator read for each setting. The operator reads the card's control device on each control event, and PipeWire's graph on each change that PipeWire reports, so a change at a knob or by a client appears here without waiting for a timer.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--volume"></span>`volume` | integer | no | The level that the endpoint plays at, as a percent of unity, from the node's gain or the Bluetooth speaker's own volume. A suspended node does not report the level that it will run at, because PipeWire applies a write to a suspended node without announcing the change. So for a suspended endpoint, this field has the level that the operator last wrote, from `spec` or a volume request. When the operator has written no level since the node last ran, this field has what the node reports. |
| <span id="statusobserved--mute"></span>`mute` | boolean | no | Whether the endpoint is silent, reported in the same way as `volume`. |
| <span id="statusobserved--codec"></span>`codec` | string | no | The codec that a Bluetooth speaker plays with now. |
| <span id="statusobserved--controls"></span>`controls` | map[string]string | no | The value of every control in `capabilities`, in the notation that `spec.controls` takes. For a control with several channels, the value of the first channel. |

### status.applied

The node that the operator last wrote the declared volume and mute to, and the values that it wrote. When the operator starts, a node whose PipeWire cookie and node id match this record kept running through the restart. The operator keeps that node's level, and writes the declaration only when it differs from the one recorded here. Any other node is one that PipeWire created since, for example after the machine boots, so the operator writes the declaration to it, or unity when the sink declares no level. Absent while the endpoint has no node.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusapplied--pipewirecookie"></span>`pipewireCookie` | integer | no | The random number that the PipeWire daemon chose when it started, from its `Core` object. PipeWire reuses node ids after a restart, so an id identifies a node only together with this number. |
| <span id="statusapplied--node"></span>`node` | integer | no | The PipeWire object id of the node. |
| <span id="statusapplied--volume"></span>`volume` | integer | no | The declared level that the node was compared with, as a percent of unity. Absent when `spec` declared no level. |
| <span id="statusapplied--mute"></span>`mute` | boolean | no | The declared mute that the node was compared with. Absent when `spec` declared no mute. |

### status.claims[]

The claims that hold the endpoint now, sorted by namespace and name, so you can see which workloads use the speaker. The list has one claim, or up to `spec.sharedClaims` claims on a shared endpoint, and it is absent when no claim holds the endpoint. The operator records a claim when the kubelet prepares it on this machine, and keeps that record only in memory. So after the operator restarts, a running pod's claim is missing here until the kubelet prepares it again.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusclaims--namespace"></span>`namespace` | string | no |  |
| <span id="statusclaims--name"></span>`name` | string | no |  |

### status.conditions[]

The conditions of the endpoint. `Connected` is `True` when the endpoint can play now: a monitor is on the HDMI output, a plug is in the analog jack, the speaker is connected, or the endpoint is USB, which is always connected. The operator treats a monitor as gone only after its ELD has been empty for 5 seconds, so a shorter drop of hot plug detect, such as an A/V receiver in standby causes, changes neither condition. `Ready` is `True` when PipeWire has a node for the endpoint. `Connected` and `Ready` show a person what the device's taints in the `ResourceSlice` tell the scheduler. `LayoutApplied`, on a sink of a sound card, is `True` when PipeWire runs the sink with the layout and the encoded formats that the operator selected. It is `False` with the reason `AwaitingIdle` while a new layout waits for every stream on the machine to end, and with the reason `Restarting` while PipeWire restarts to load it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The name

The operator builds the name from the hardware's identity, so the name
stays the same after a reboot and when a second card is added:

| Endpoint | Form | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `node-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | node, vendor, product, serial, PCM id | `node-1-usb-0573-1573-a34004801402-usb-audio` |
| USB card with a serial, past 63 characters | node, vendor, product, serial hash, PCM id | `studio-screen-12-usb-046d-0a44-5fe3de05-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `node-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45` |

The PCM id is the driver's name for the endpoint, such as `HDMI 0` or
`USB Audio`, in lowercase with dashes. Every ALSA name starts with the
node name, because a USB serial is not unique across machines: dongles
of one model can report the same serial. The serial tells apart two
identical dongles on one machine. A card that moves to another machine
becomes a new `Sink`, and so does a card with no serial that moves to
another port. The old `Sink` stays with its `spec`, and the operator
on the old machine sets its `Connected` condition to `False`. Delete
it when you no longer need its `spec`. A card that plays and
records through one PCM gives its `Source` the same name with
`-capture` on the end.

A name is a DNS label, so it can have at most 63 characters. A USB
serial is often 20 characters or more, so on a machine with a long
name the serial form can exceed that limit. The name then contains the
first eight hex digits of the serial's SHA-256 in place of the serial,
and keeps its other parts. The hash is stable and differs for each
serial, so two identical dongles on one machine still get two names.
The operator checks each name separately, and `-capture` adds eight
characters. So a dongle whose `Sink` name is 56 to 63 characters keeps
the serial in the `Sink` name and uses the hash in the `Source` name.
To compute the hash by hand:

```sh
printf %s ABCDEF0123456789ABCDEF0 | sha256sum | cut -c1-8
```

The operator refuses any other name that is longer than 63 characters,
and its log names the endpoint and the length.

On an Intel HDMI codec, `hdmi-0` is the card's first HDMI output, not
a physical port. The codec binds a pin to the first free output when a
monitor appears, so on a card with two monitors, the output that each
monitor uses can change between plug events. `status.monitor` shows
which monitor the output feeds now. On a card with one HDMI monitor,
the output does not change.

## How the operator applies the `spec`

The operator writes a declared `volume.level` and `mute` in two cases:
when the declaration changes, and when the endpoint appears, for
example when a speaker reconnects or PipeWire creates the nodes again.
At other times the operator leaves the level alone. When someone
presses a button on the speaker, a client changes the graph, or a
volume request arrives, the level changes, and the operator reports
the new level in `status.observed` without writing the declaration
back.

A declared control and a declared `codec` work differently. The
operator compares each one with the value it last read, and writes to
the hardware only when the two differ. It checks each declared control
against `status.capabilities`. When the card does not declare the
control name, or the value is outside the control's range, the
operator does not write it and logs the reason. An empty `spec` writes
nothing. The operator writes no value that you did not declare, with
one exception: it sets every sink to unity gain when PipeWire creates
the node, so that no software gain below unity lowers the resolution
of the samples before they reach the card. It writes unity only to a node that PipeWire created since
the operator last applied the declaration.

The operator records in `status.applied` the node that it applied the
declaration to, by the node's id and the cookie of the PipeWire that
created it, and the `volume.level` and `mute` that it applied. After a
restart, the operator uses this record to tell which nodes kept
running. A node that `status.applied` names keeps its current level,
because that level can come from a volume request or a press of the
speaker's button, and the declaration does not include those. The
operator writes the declaration to such a node only when it differs
from the declaration in `status.applied`, for example when you changed
it while the operator was down. Every other node is new, so it takes
the declaration, or unity on a sink with no declared level. When the
machine boots, PipeWire starts with a new cookie and creates every
node again, unmuted at its configured level, so each node takes the
declaration on the operator's first pass.

`volume.max` and `volume.step` control the volume requests that the
next section describes, and the operator never writes them to the
hardware. `volume.max` defaults to 100 percent and `volume.step` to 5
percentage points. The API server fills in the defaults when
`spec.volume` is present.

`volume.level`, `mute`, and `controls` apply at once, whether or not a
claim holds the endpoint. `codec` waits until the claim ends, because
a codec switch replaces the speaker's node and interrupts playback.
While a claim holds the speaker, the claim's own `codec` parameter
takes precedence. On a shared speaker, `codec` waits until every claim ends.

`sharedClaims` changes the device that the operator publishes in the
`ResourceSlice`, not the hardware. The operator writes the slice
again, and the new count applies to the next allocation.
[Shared endpoints](/docs/reference/devices/#shared-endpoints) lists
the fields that the slice publishes.

## Volume requests

A remote's volume key reaches a `Sink` through media-operator.
media-operator turns each key press into an absolute level, at most `volume.max`, and
writes the level with the time of the press to
`status.session.volumeAsk`. It writes that block by server-side apply
under its own field manager. This operator writes the rest of `status`
as a merge patch that never names the block, so neither writer removes
the other's fields.

```yaml
status:
  session:
    player: media/den
    volumeAsk:
      level: 45
      mute: false
      at: "2026-10-04T12:15:25.164Z"
```

Each new `at` value is one volume request. The operator applies it
once, with the same write as `volume.level`: the node's gain, or a
Bluetooth speaker's own volume over AVRCP. When several requests arrive before the operator
applies one, it applies only the newest. After the write succeeds, the
operator writes the requested level and mute to `status.observed`
immediately, and the next pass replaces them with what PipeWire
reports. The operator does not apply a request that it finds the first
time it reads the `Sink`, as in its first pass after a start, because
either the previous operator applied that request or the device's
level is newer. It drops a request for an endpoint with no node, and
the endpoint takes `volume.level` when its node appears.

## Observation

The operator updates `status.observed` from two event sources. The
card's control device reports every control write from any process,
every jack change, every monitor change, and a knob turned on a USB
DAC. PipeWire's graph reports every node and device change. So a
change that someone makes with a knob, a remote, or a speaker's own
buttons appears in `observed` on the pass that the event starts. On
that pass the operator writes a declared control or codec back to the
hardware, and does not write a declared level back. The operator also
runs a pass every 60 seconds, for a card whose control events it could
not subscribe to and for a status write that failed.

## The channel layout

The operator declares every sink of a sound card to PipeWire with a
channel layout: the position of each PCM slot, in PipeWire's channel
names. WirePlumber links each channel of a stream to the sink channel
with the same name. It links every stream to a sink with no positions
as two channels, `FL` and `FR`, so a 5.1 or 7.1 stream is mixed down
to them.

The operator reads the layout from the first source in this table that
has one, and `status.layoutSource` names it:

| Source | Where the positions come from |
| --- | --- |
| `Spec` | `spec.layout` |
| `ELD` | the HDMI or DisplayPort monitor's speaker allocation, limited to the largest LPCM channel count that the monitor accepts |
| `ChannelMap` | the channel map that a USB device describes in its descriptors. PipeWire reads it when it opens the device, so `status.layout` is absent |
| `None` | nothing. The analog jack reports no speakers, and neither does an HDMI output whose monitor was off when the pod started |

From the ELD, the operator selects one of the HDMI layouts that
PipeWire's own card profiles use:

| The monitor advertises | `status.layout` |
| --- | --- |
| 8-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR`, `RLC/RRC` | `FL, FR, RL, RR, FC, LFE, SL, SR` |
| 6-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR` | `FL, FR, RL, RR, FC, LFE` |
| anything else | `FL, FR` |

Most televisions accept 8-channel LPCM and have two speakers. Such a
set gets `FL, FR`, so PipeWire mixes the center channel, which
carries the dialog, into the front pair. An HDMI sink keeps its layout
from the ELD while its monitor is off, so a receiver that turns off
and on again changes nothing.

PipeWire reads the layout once, when it starts. When a sink's layout
changes, because `spec.layout` changed or a monitor with a different
layout is connected, the operator writes the new declaration. The
first process of the PipeWire container then restarts PipeWire inside
the container, so the restart never waits in the kubelet's crash
backoff. The
restart ends every stream on the machine, so the operator waits until
no stream is playing. While it waits, `LayoutApplied` is `False` with
the reason `AwaitingIdle`. It stays `False` with the reason
`Restarting` until the new PipeWire starts. Each change posts one
`LayoutChanged` `Event` on the `Sink`, which names the old layout, the
new layout, and the source.

The operator does not write the kernel's channel map. On HDMI the
kernel routes each slot by its standard allocation for the channel
count, so a height position such as `TFL` in `spec.layout` plays from
the speaker that the standard allocation gives that slot. The kernel can route
front heights (CEA allocation 0x2f), but only when a program writes
the PCM's channel map while the device is open and stopped.

## Encoded formats

A receiver plays Dolby Atmos or DTS:X on its height speakers only when
it decodes the track itself. So a sink can send a film's Dolby
or DTS track to its monitor as the encoded bitstream, and
`status.passthrough` lists the formats that it sends this way:

    kubectl get sink <name> -o jsonpath='{.status.passthrough}'

| Name | Format | Also carries |
| --- | --- | --- |
| `AC3` | Dolby Digital | |
| `EAC3` | Dolby Digital Plus | Dolby Atmos |
| `TrueHD` | Dolby TrueHD | Dolby Atmos |
| `DTS` | DTS | |
| `DTS-HD` | DTS-HD High Resolution and Master Audio | DTS:X |

The list contains the formats that the monitor's ELD lists, and the
device attribute `encodedFormats` has the same list for a claim to
select on. The operator declares the sink to PipeWire with these
formats, and with alsa-lib's `hdmi` device for the output in place of
the `hw` device, because PipeWire's ALSA node sends a bitstream only
through a device with that name. An output of a card whose driver is
not `HDA-Intel` sends PCM only, and so does every analog, USB, and
Bluetooth sink.

The sink does not choose what a stream sends. A player whose stream is
a bitstream in one of these formats is linked as it is, and every
other stream plays as PCM. A player has to send only the formats
in the list: PipeWire refuses a bitstream in any other format, and the
stream then plays nothing.
[media-operator](https://liken.sh/media/)
reads this list for a `Player` that sets `passthrough: On`.

While a bitstream plays through a sink:

* The sink's level and mute have no effect, because PipeWire cannot
  scale a bitstream. Only the receiver's own volume changes the
  level.
* An underrun, a moment when the card runs out of samples, breaks the
  bitstream's framing, and the receiver mutes until it finds the
  stream again. With PCM the same underrun is a gap of a few
  milliseconds. PipeWire logs each one, as the
  [install guide](/docs/guides/install/) shows.
* The bitstream has the sink to itself. PipeWire refuses a second
  stream to the sink, and a tap of the sink through `audio-api`, until
  the bitstream ends. When a stream or a tap is already using the
  sink, PipeWire refuses the bitstream instead.

A change of the formats is handled like a layout change. When a monitor
reports a different list, the operator writes the declaration again,
and PipeWire restarts when no stream is playing. `LayoutApplied`
reports the wait, and the `LayoutChanged` `Event` names the formats. A
sink keeps its formats while its monitor is off.

## Events

The operator posts a Kubernetes `Event` on a `Sink` for each change of
a condition and for each action it takes. A `Sink` is cluster-scoped,
so its `Event`s are in the `default` namespace. `kubectl describe
sink` shows them. `kubectl events --for` shows them only with
`-n default` or `-A`:

    kubectl events -n default --for sink/<name>

The API server deletes an `Event` one hour after its last write, so
the conditions and the operator's log keep the facts for longer. When
an `Event` repeats within 10 minutes, the operator patches the count
of the existing `Event` in place of posting a new one.

Each condition change posts one `Event` with the condition's own
reason and message, such as `NoMonitor`, `JackEmpty`, or
`AwaitingIdle`. The first status write posts one for each condition. A
change of `Ready` to `False` is a `Warning` while `Connected` is
`True`, because the endpoint is connected but PipeWire has no node to
send sound through. Every other condition change is `Normal`: a
television that turns off and a plug pulled from a jack are ordinary
actions by a person.

The operator posts these reasons for the actions and faults that
change no condition:

| Reason | Type | When |
|---|---|---|
| `LayoutChanged` | `Normal` | The operator wrote a new channel layout, and PipeWire restarts in its container to apply it. |
| `LayoutWriteFailed` | `Warning` | The operator could not write the declaration that has the new layout. The sink keeps its layout, and each pass tries the write again. |
| `SpecRefused` | `Warning` | The `spec` sets a value that the endpoint does not accept, such as a codec that the speaker does not offer. The message names each refused value. |
| `PipeWireLost` | `Warning` | A read of PipeWire's graph failed. After 3 failed reads in a row, the operator taints every output and restarts. |
| `PipeWireRecovered` | `Normal` | A read of PipeWire's graph succeeded again, after one or more failed reads. |
| `BluetoothUnavailable` | `Warning` | On a speaker's `Sink`: a read of the paired speakers from `bluetoothd` failed. The speaker is published with a taint until a read succeeds. |
| `BluetoothAvailable` | `Normal` | On a speaker's `Sink`: a read from `bluetoothd` succeeds again. |
| `Captured` | `Normal` | The capture API returned the audio of the `Sink` to a caller. The message names the caller and the format. |

The operator posts an `Event` for a fault when it first meets the
fault, and does not post it again on each later pass that meets the
same fault.
