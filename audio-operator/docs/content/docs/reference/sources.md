---
title: Sources
weight: 30
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

A `Source` is one capture endpoint as a Kubernetes resource: an
analog input, or the capture side of a USB card. The operator
creates one for every capture endpoint it publishes, cluster-scoped
like a `Node`, named the same way a `Sink` is. You never create or
delete one. The operator writes the whole of `status`, and you write
`spec`, which states what the endpoint rests at.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Source
metadata:
  name: usb-0573-1573-a34004801402-usb-audio-capture
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

For a microphone, `mute` is the field that matters most. Setting
`spec.mute: true` on a `Source` closes that microphone for everyone,
a rule that closes every microphone is one patch per `Source`, and
`kubectl get sources` shows which ones are open. A pod that records
still holds the microphone through a claim, and its stream reaches
the node that `status.nodeName` names.

An HDA card serves several input jacks through one capture PCM and
picks the live jack with its `Input Source` control, so the `Source`
is the PCM and the jack is a control in `spec.controls`. A Bluetooth
headset's microphone is not published yet, because it needs the
headset profiles the pod does not run.

One capture endpoint: an analog input, or a USB card's capture side. A Bluetooth headset's microphone is not published yet. An HDA card serves several input jacks through one capture PCM and picks the live jack with its Input Source control, so the Source is the PCM and the jack is a control.

## spec

The settings the endpoint rests at. Every field is optional: the operator writes a declared field back when the endpoint diverges from it, and it never writes a field the spec leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--volume"></span>`volume` | integer | no | The gain PipeWire applies to what the endpoint captures, as a percent of unity, applied to every channel alike. It applies at once, under a claim or not. |
| <span id="spec--mute"></span>`mute` | boolean | no | Whether the endpoint captures silence. It applies at once. A closed microphone is this field, on every Source, and kubectl get sources shows which ones are open. |
| <span id="spec--controls"></span>`controls` | map[string]string | no | The card's own controls, keyed by the kernel's control name as status.capabilities lists them, such as Capture Volume or Input Source. An integer control takes a number within its range, a boolean control takes on or off, and an enumerated control takes one of its values. The operator writes a control only when it is stated here. |

## status

What the hardware declares and what the operator last read. The operator owns every field here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--node"></span>`node` | string | no | The machine that holds the endpoint now. For a USB card with a serial, the value changes when the card moves. |
| <span id="status--location"></span>`location` | string | no | Where the card is on the machine, in the kernel's spelling: a PCI address such as 0000:00:1f.3, or a USB port path such as 1-6. |
| <span id="status--connectiontype"></span>`connectionType` | string | no | How sound enters the machine. One of: `analog`, `usb`. |
| <span id="status--card"></span>`card` | [object](#statuscard) | no | The ALSA card the endpoint is on. The number and the id are this boot's, and a second card can change both, so nothing durable is keyed to them. |
| <span id="status--pcm"></span>`pcm` | [object](#statuspcm) | no | The PCM device the endpoint captures through. |
| <span id="status--nodename"></span>`nodeName` | string | no | The PipeWire node a consumer's capture streams target, the same value a prepared claim delivers as PIPEWIRE_NODE. Absent while PipeWire holds no node for the endpoint. |
| <span id="status--format"></span>`format` | [object](#statusformat) | no | The format the node runs at, from PipeWire's own Format parameter. Absent while the node is not running. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The card's own controls that belong to this endpoint, keyed by the kernel's control name. A Capture control goes to the card's sources, and a jack control is not listed because it is read-only and feeds the Connected condition. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value the operator read for each setting. The operator reads the card's control device on every event it delivers, and PipeWire's graph on every change it prints, so a change a person made with a knob or a client shows here without a poll. |
| <span id="status--claim"></span>`claim` | [object](#statusclaim) | no | The claim that holds the endpoint now, and absent between holders. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Connected reports that the endpoint can capture now: a plug in an analog jack, and always on USB. Ready reports that PipeWire holds a node for it. |

### status.card

The ALSA card the endpoint is on. The number and the id are this boot's, and a second card can change both, so nothing durable is keyed to them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscard--number"></span>`number` | integer | no | The card number the kernel assigned this boot. |
| <span id="statuscard--id"></span>`id` | string | no | The card's short id, such as PCH, with the suffix the kernel appends on a clash, such as PCH_1. |
| <span id="statuscard--driver"></span>`driver` | string | no | The kernel driver that binds the card, such as USB-Audio. |
| <span id="statuscard--name"></span>`name` | string | no | The card's name, as the driver states it, such as HDA Intel PCH. |

### status.pcm

The PCM device the endpoint captures through.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspcm--device"></span>`device` | integer | no | The PCM device number on the card, this boot. |
| <span id="statuspcm--id"></span>`id` | string | no | The driver's name for the PCM, such as USB Audio. It is the part of the endpoint's name that outlives the number. |

### status.format

The format the node runs at, from PipeWire's own Format parameter. Absent while the node is not running.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusformat--rate"></span>`rate` | integer | no | The sample rate in hertz. |
| <span id="statusformat--channels"></span>`channels` | integer | no | The channel count. |
| <span id="statusformat--positions"></span>`positions` | []string | no | The channel positions in order, in PipeWire's names: FL, FR, MONO. |

### status.capabilities.*

The card's own controls that belong to this endpoint, keyed by the kernel's control name. A Capture control goes to the card's sources, and a jack control is not listed because it is read-only and feeds the Connected condition.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--type"></span>`type` | string | no | What the control takes. One of: `integer`, `boolean`, `enumerated`. |
| <span id="statuscapabilities--min"></span>`min` | integer | no | The smallest number an integer control accepts. |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number an integer control accepts. |
| <span id="statuscapabilities--step"></span>`step` | integer | no | The step between numbers an integer control accepts, absent when every number in the range is accepted. |
| <span id="statuscapabilities--mindecibels"></span>`minDecibels` | string | no | The level at min, in decibels, when the control declares one, as a decimal string such as -65.25. |
| <span id="statuscapabilities--maxdecibels"></span>`maxDecibels` | string | no | The level at max, in decibels, when the control declares one. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value an enumerated control accepts. |
| <span id="statuscapabilities--channels"></span>`channels` | integer | no | How many channels the control carries. A write from spec sets every channel to the same value. |

### status.observed

The last value the operator read for each setting. The operator reads the card's control device on every event it delivers, and PipeWire's graph on every change it prints, so a change a person made with a knob or a client shows here without a poll.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--volume"></span>`volume` | integer | no | The gain PipeWire applies, as a percent of unity. |
| <span id="statusobserved--mute"></span>`mute` | boolean | no | Whether the endpoint captures silence. |
| <span id="statusobserved--controls"></span>`controls` | map[string]string | no | The value of every control in capabilities, in the same spelling spec.controls takes. A control with several channels reports the first channel's value. |

### status.claim

The claim that holds the endpoint now, and absent between holders.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusclaim--namespace"></span>`namespace` | string | no |  |
| <span id="statusclaim--name"></span>`name` | string | no |  |

### status.conditions[]

Connected reports that the endpoint can capture now: a plug in an analog jack, and always on USB. Ready reports that PipeWire holds a node for it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The name and the resting layer

A `Source` is named by the same rule as a `Sink`, and its `spec`
works the same way: the operator writes a declared field back only
where the hardware diverges from it, validates a control against
`status.capabilities`, and invents no value. The
[`Sink` reference](/docs/reference/sinks/) has the name table and
the rules in full.

The one difference is which controls attach. A `Capture` control,
`Input Source`, and a `Mic Boost` go to the card's sources, and a
`Playback` control goes to its sinks.
