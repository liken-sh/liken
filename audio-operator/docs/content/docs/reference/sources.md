---
title: Sources
weight: 30
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

# `Source`

A `Source` is one capture endpoint as a Kubernetes resource: an
analog input, or the capture side of a USB card. The operator creates
a `Source` for each capture endpoint it publishes. A `Source` is
cluster-scoped, like a `Node`, and is named in the same way as a
`Sink`. You never create one, and you delete one only after its card
has gone. The operator writes all of `status`, and you write `spec`,
the settings you want for the endpoint.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Source
metadata:
  name: node-1-usb-0573-1573-a34004801402-usb-audio-capture
spec:
  mute: true
status:
  node: node-1
  location: "1-6"
  connectionType: usb
  pcm:
    device: 0
    id: USB Audio
  nodeName: liken.audio.card1-pcm0c
  capabilities:
    Mic Capture Volume:
      type: integer
      min: 0
      max: 16
      channels: 1
    Mic Capture Switch:
      type: boolean
      channels: 1
  observed:
    volume: 100
    mute: true
    controls:
      Mic Capture Volume: "8"
      Mic Capture Switch: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Ready
      status: "True"
```

For a microphone, `mute` is the most useful field. Setting
`spec.mute: true` on a `Source` closes that microphone for every
user. A rule that closes every microphone is one patch for each
`Source`, and `kubectl get sources` shows which microphones are open.
A pod that records still gets the microphone through a claim, and
its capture stream targets the PipeWire node that `status.nodeName`
names.

An HDA card serves several input jacks through one capture PCM and
selects the active jack with its `Input Source` control. So a
`Source` is the PCM, and you choose the jack by setting
`Input Source` in `spec.controls`. A Bluetooth headset's microphone is not published
yet, because it needs the headset profiles, which the pod does not
run.

A `Source` is one capture endpoint: an analog input, or the capture side of a USB card. A Bluetooth headset's microphone is not published yet. An HDA card serves several input jacks through one capture PCM and selects the active jack with its `Input Source` control, so a `Source` is the PCM, and you choose the jack with that control. The operator builds the name from the machine's name and the card's identity, as the section "The name" of the `Sink` reference describes. When a USB card's name would be longer than 63 characters, the name has the first eight hex digits of the SHA-256 of the serial in place of the serial.

## spec

The settings you want for the endpoint. Every field is optional. The operator writes `volume` and `mute` to the endpoint only when you change them and when the endpoint appears. Between those times, it reports the endpoint's level in `status.observed`. It writes a declared control to the hardware whenever the hardware differs from it. It never writes a setting that `spec` leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--volume"></span>`volume` | integer | no | The gain that PipeWire applies to the captured sound, as a percent of unity, the same for every channel. A change applies at once, whether or not a claim holds the endpoint. |
| <span id="spec--mute"></span>`mute` | boolean | no | `true` makes the endpoint capture silence, and a change applies at once. Set it to `true` to close a microphone. The `Mute` column of `kubectl get sources` shows which microphones are closed. |
| <span id="spec--controls"></span>`controls` | map[string]string | no | The card's own controls, keyed by the kernel's control name as `status.capabilities` lists it, for example `Capture Volume` or `Input Source`. An integer control takes a number within its range, a boolean control takes `on` or `off`, and an enumerated control takes one of its values. The operator writes a control only when `spec` lists it. |
| <span id="spec--sharedclaims"></span>`sharedClaims` | integer | no | How many claims can hold the endpoint at the same time, at least 2. When this field is absent, one claim holds the endpoint, and a second claim stays `Pending` until the first ends. When it is set, the operator publishes the device in the `ResourceSlice` with multiple allocations allowed and a capacity named `claims` of this value. A claim whose request asks for `claims: 1` shares the endpoint with the other claims that ask for one, up to this number, and each of them records the same input. A claim that asks for no `claims` capacity gets all of it and holds the endpoint alone. A change applies to the next allocation, and the claims that already hold the endpoint keep it. Neither the hardware nor PipeWire limits how many streams record from one endpoint, so the number is yours to choose. |

## status

The hardware facts and the values that the operator last read. The operator writes every field here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--node"></span>`node` | string | no | The machine that has the endpoint. The name of every `Source` starts with this machine's name, so a card that moves to another machine gets a new `Source`. The operator on each machine lists and watches only the `Source`s whose `status.node` is that machine. |
| <span id="status--location"></span>`location` | string | no | Where the card is on the machine, in the kernel's notation: a PCI address such as `0000:00:1f.3`, or a USB port path such as `1-6`. |
| <span id="status--connectiontype"></span>`connectionType` | string | no | The way sound enters the machine. One of: `analog`, `usb`. |
| <span id="status--card"></span>`card` | [object](#statuscard) | no | The ALSA card that the endpoint is on. The kernel assigns the number and the id at each boot, and adding a second card can change both, so the endpoint's name does not use them. |
| <span id="status--pcm"></span>`pcm` | [object](#statuspcm) | no | The PCM device that the endpoint captures through. |
| <span id="status--nodename"></span>`nodeName` | string | no | The name of the PipeWire node that a pod's capture streams target. A prepared claim gives the pod the same value in `PIPEWIRE_NODE`. Absent while PipeWire has no node for the endpoint. |
| <span id="status--format"></span>`format` | [object](#statusformat) | no | The format that the node runs at, from PipeWire's `Format` parameter. Absent while the node is not running. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The card's own controls for this endpoint, keyed by the kernel's control name. A `Capture` control applies to the card's sources, and so does a control that selects, boosts, or sets the automatic gain of an input, such as `Input Source`, `Mic Boost`, or `Auto Gain Control`. Jack controls are left out, because they are read-only and the operator reads them for the `Connected` condition. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value that the operator read for each setting. The operator reads the card's control device on each control event, and PipeWire's graph on each change that PipeWire reports, so a change at a knob or by a client appears here without waiting for a timer. |
| <span id="status--applied"></span>`applied` | [object](#statusapplied) | no | The node that the operator last wrote the declared volume and mute to, and the values that it wrote. When the operator starts, a node whose PipeWire cookie and node id match this record kept running through the restart. The operator keeps that node's level, and writes the declaration only when it differs from the one recorded here. Any other node is one that PipeWire created since, for example after the machine boots, so the operator writes the declaration to it. Absent while the endpoint has no node. |
| <span id="status--claims"></span>`claims` | [\[\]object](#statusclaims) | no | The claims that hold the endpoint now, sorted by namespace and name. The list has one claim, or up to `spec.sharedClaims` claims on a shared endpoint, and it is absent when no claim holds the endpoint. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The conditions of the endpoint. `Connected` is `True` when the endpoint can capture now: a plug is in the analog jack, or the endpoint is USB, which is always connected. `Ready` is `True` when PipeWire has a node for the endpoint. |

### status.card

The ALSA card that the endpoint is on. The kernel assigns the number and the id at each boot, and adding a second card can change both, so the endpoint's name does not use them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscard--number"></span>`number` | integer | no | The card number that the kernel assigned in this boot. |
| <span id="statuscard--id"></span>`id` | string | no | The card's short id, such as `PCH`, with the suffix that the kernel appends when two cards have the same id, such as `PCH_1`. |
| <span id="statuscard--driver"></span>`driver` | string | no | The kernel driver of the card, such as `USB-Audio`. |
| <span id="statuscard--name"></span>`name` | string | no | The card's name as its driver reports it, such as `HDA Intel PCH`. |

### status.pcm

The PCM device that the endpoint captures through.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspcm--device"></span>`device` | integer | no | The PCM device number on the card, in this boot. |
| <span id="statuspcm--id"></span>`id` | string | no | The driver's name for the PCM, such as `USB Audio`. The endpoint's name uses this id, because it stays the same when the card number changes. |

### status.format

The format that the node runs at, from PipeWire's `Format` parameter. Absent while the node is not running.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusformat--rate"></span>`rate` | integer | no | The sample rate in hertz. |
| <span id="statusformat--channels"></span>`channels` | integer | no | The number of channels. |
| <span id="statusformat--positions"></span>`positions` | []string | no | The channel positions in order, in PipeWire's names, such as `FL`, `FR`, `MONO`. |

### status.capabilities.*

The card's own controls for this endpoint, keyed by the kernel's control name. A `Capture` control applies to the card's sources, and so does a control that selects, boosts, or sets the automatic gain of an input, such as `Input Source`, `Mic Boost`, or `Auto Gain Control`. Jack controls are left out, because they are read-only and the operator reads them for the `Connected` condition.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--type"></span>`type` | string | no | The kind of value that the control takes. One of: `integer`, `boolean`, `enumerated`. |
| <span id="statuscapabilities--min"></span>`min` | integer | no | The smallest number that an integer control accepts. |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number that an integer control accepts. |
| <span id="statuscapabilities--step"></span>`step` | integer | no | The difference between neighboring numbers that an integer control accepts. Absent when every number in the range is accepted. |
| <span id="statuscapabilities--mindecibels"></span>`minDecibels` | string | no | The level at `min`, in decibels, as a decimal string such as `-65.25`, when the control declares one. |
| <span id="statuscapabilities--maxdecibels"></span>`maxDecibels` | string | no | The level at `max`, in decibels, when the control declares one. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value that an enumerated control accepts. |
| <span id="statuscapabilities--channels"></span>`channels` | integer | no | The number of channels that the control has. A write from `spec` sets every channel to the same value. |

### status.observed

The last value that the operator read for each setting. The operator reads the card's control device on each control event, and PipeWire's graph on each change that PipeWire reports, so a change at a knob or by a client appears here without waiting for a timer.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--volume"></span>`volume` | integer | no | The gain that PipeWire applies, as a percent of unity. An idle node reports no level of its own, so for an idle endpoint this field has the level that the operator last wrote to it, and is absent until `spec.volume` declares a level. |
| <span id="statusobserved--mute"></span>`mute` | boolean | no | Whether the endpoint captures silence, reported in the same way as `volume`. |
| <span id="statusobserved--controls"></span>`controls` | map[string]string | no | The value of every control in `capabilities`, in the notation that `spec.controls` takes. For a control with several channels, the value of the first channel. |

### status.applied

The node that the operator last wrote the declared volume and mute to, and the values that it wrote. When the operator starts, a node whose PipeWire cookie and node id match this record kept running through the restart. The operator keeps that node's level, and writes the declaration only when it differs from the one recorded here. Any other node is one that PipeWire created since, for example after the machine boots, so the operator writes the declaration to it. Absent while the endpoint has no node.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusapplied--pipewirecookie"></span>`pipewireCookie` | integer | no | The random number that the PipeWire daemon chose when it started, from its `Core` object. PipeWire reuses node ids after a restart, so an id identifies a node only together with this number. |
| <span id="statusapplied--node"></span>`node` | integer | no | The PipeWire object id of the node. |
| <span id="statusapplied--volume"></span>`volume` | integer | no | The declared level that the node was compared with, as a percent of unity. Absent when `spec` declared no level. |
| <span id="statusapplied--mute"></span>`mute` | boolean | no | The declared mute that the node was compared with. Absent when `spec` declared no mute. |

### status.claims[]

The claims that hold the endpoint now, sorted by namespace and name. The list has one claim, or up to `spec.sharedClaims` claims on a shared endpoint, and it is absent when no claim holds the endpoint.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusclaims--namespace"></span>`namespace` | string | no |  |
| <span id="statusclaims--name"></span>`name` | string | no |  |

### status.conditions[]

The conditions of the endpoint. `Connected` is `True` when the endpoint can capture now: a plug is in the analog jack, or the endpoint is USB, which is always connected. `Ready` is `True` when PipeWire has a node for the endpoint.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The name and the `spec`

A `Source` has the same name rules as a `Sink`, and its `spec` works
in the same way. The operator writes a declared `volume` and `mute`
when the declaration changes and when the endpoint appears. It writes
a declared control to the hardware only when the hardware differs from
it, checks each control against `status.capabilities`, and writes no
value that you did not declare. A `Source` has no `status.session`,
and its `spec.volume` is a number, the level alone. The
[`Sink` reference](/docs/reference/sinks/) has the name table and the
rules in full.

A card's controls are split between its endpoints. A `Capture`
control, and a control that selects, boosts, or sets the automatic
gain of an input, such as `Input Source`, `Mic Boost`, or
`Auto Gain Control`, goes to the card's `Source`s. A `Playback`
control, and any other control, goes to its analog and USB `Sink`s.

## Events

The operator posts a `Normal` `Event` on a `Source` for each change of
its `Connected` and `Ready` conditions, with the condition's reason
and message. It posts a `Warning` when `Ready` becomes `False` while
`Connected` is `True`. It posts `SpecRefused` when the `spec` sets a
value that the endpoint does not accept. The capture API posts
`Captured` each time it returns a recording to a caller. The `Event`s
are in the `default` namespace. The
[`Sink` reference](/docs/reference/sinks/#events) describes each
reason.
