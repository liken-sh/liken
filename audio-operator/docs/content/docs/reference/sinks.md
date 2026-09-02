---
title: Sinks
weight: 20
toc: true
---

<!-- Generated from deploy/crds.yaml by crdref. Do not edit. -->

A `Sink` is one playback endpoint as a Kubernetes resource: an
analog jack, an HDMI or DisplayPort output, the playback side of a
USB card, or a Bluetooth speaker. The operator creates one for every
endpoint it publishes, cluster-scoped like a `Node`, named by the
same device name the `ResourceSlice` carries. You never create or
delete one. The operator writes the whole of `status`: where the
endpoint is, the controls the card declares, and the values it last
read. You write `spec`, which states what the endpoint rests at.

```yaml
apiVersion: audio.liken.sh/v1alpha1
kind: Sink
metadata:
  name: usb-0573-1573-a34004801402-usb-audio
spec:
  volume: 80
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
```

The claim and the `Sink` answer two different needs. A pod that
plays sound still holds the output through a claim, as the
[claim guide](/docs/guides/claim/) shows, and it still has its own
stream volume. The `Sink` is for everything about the output that
is not the sound itself: its resting level, its mute, and the
card's own controls. Because it is an ordinary Kubernetes resource,
anything with the right RBAC can change it. A pod that only wants
to mute the kitchen needs no claim, and a rule that lowers every
speaker at night is one patch per `Sink`.
[Set what an endpoint rests at](/docs/guides/rest/) walks through
it.

One playback endpoint: an analog jack, an HDMI or DisplayPort output, a USB card's playback side, or a Bluetooth speaker. It carries what the hardware declares and what the endpoint rests at.

## spec

The settings the endpoint rests at. Every field is optional: the operator writes a declared field back when the endpoint diverges from it, and it never writes a field the spec leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--volume"></span>`volume` | integer | no | The level the endpoint plays at, as a percent of unity, applied to every channel alike. On an ALSA endpoint it is the gain PipeWire applies in software. On a Bluetooth speaker it is the speaker's own volume, sent over AVRCP when the speaker supports absolute volume, and a software gain when it does not. It applies at once, under a claim or not, and a claim holder's own stream fader is a separate level above it. |
| <span id="spec--mute"></span>`mute` | boolean | no | Whether the endpoint is silent. It lands where volume lands, and it applies at once. |
| <span id="spec--controls"></span>`controls` | map[string]string | no | The card's own controls, keyed by the kernel's control name as status.capabilities lists them, such as Master Playback Volume. An integer control takes a number within its range, a boolean control takes on or off, and an enumerated control takes one of its values. The operator writes a control only when it is stated here, and a card with two endpoints that share a control obeys the write that came last, because the hardware has one register. |
| <span id="spec--codec"></span>`codec` | string | no | The A2DP codec a Bluetooth speaker rests at, one of status.bluetooth.codecs. A claim's own codec parameter wins while the claim holds the speaker, and a change here waits for the claim to end, because a codec switch replaces the speaker's node and interrupts playback. It is ignored on an ALSA endpoint. |

## status

What the hardware declares and what the operator last read. The operator owns every field here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--node"></span>`node` | string | no | The machine that holds the endpoint now. For a USB card with a serial, and for a Bluetooth speaker, the value changes when the hardware moves. |
| <span id="status--location"></span>`location` | string | no | Where the card is on the machine, in the kernel's spelling: a PCI address such as 0000:00:1f.3, or a USB port path such as 1-6. Absent on a Bluetooth speaker. |
| <span id="status--connectiontype"></span>`connectionType` | string | no | How sound leaves the machine. One of: `analog`, `hdmi`, `displayport`, `usb`, `bluetooth`. |
| <span id="status--card"></span>`card` | [object](#statuscard) | no | The ALSA card the endpoint is on. The number and the id are this boot's, and a second card can change both, so nothing durable is keyed to them. |
| <span id="status--pcm"></span>`pcm` | [object](#statuspcm) | no | The PCM device the endpoint plays through. |
| <span id="status--monitor"></span>`monitor` | [object](#statusmonitor) | no | The monitor an HDMI or DisplayPort slot feeds, from the ELD the graphics driver wrote into the card. Absent while no monitor answers, and absent on every other connection type. An Intel HDMI codec binds a pin to the first free slot when a monitor appears, so a card with two monitors can swap which slot feeds which between plug events, and this object is where that shows. |
| <span id="status--bluetooth"></span>`bluetooth` | [object](#statusbluetooth) | no | The speaker behind a Bluetooth endpoint. Absent on every other connection type. |
| <span id="status--nodename"></span>`nodeName` | string | no | The PipeWire node a consumer's streams target, the same value a prepared claim delivers as PIPEWIRE_NODE. Absent while PipeWire holds no node for the endpoint. |
| <span id="status--format"></span>`format` | [object](#statusformat) | no | The format the node runs at, from PipeWire's own Format parameter. Absent while the node is not running. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The card's own controls that belong to this endpoint, keyed by the kernel's control name. A Playback control goes to the card's analog and USB sinks, and so does a control that names no direction, such as Auto-Mute Mode. An IEC958 Playback Switch goes to the HDMI slot of the same ordinal, and it is the only control an HDMI slot lists, because an HDMI PCM has no volume element. A jack control is not listed because it is read-only and feeds the Connected condition. A Bluetooth speaker lists nothing. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value the operator read for each setting. The operator reads the card's control device on every event it delivers, and PipeWire's graph on every change it prints, so a change a person made with a knob or a client shows here without a poll. |
| <span id="status--claim"></span>`claim` | [object](#statusclaim) | no | The claim that holds the endpoint now, and absent between holders. It answers which workload has the speakers. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Connected reports that the endpoint can play now: a monitor on an HDMI slot, a plug in an analog jack, a connected speaker, and always on USB. Ready reports that PipeWire holds a node for it. The two carry the same facts as the no-monitor and no-sink taints on the device, for a person rather than the scheduler. |

### status.card

The ALSA card the endpoint is on. The number and the id are this boot's, and a second card can change both, so nothing durable is keyed to them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscard--number"></span>`number` | integer | no | The card number the kernel assigned this boot. |
| <span id="statuscard--id"></span>`id` | string | no | The card's short id, such as PCH, with the suffix the kernel appends on a clash, such as PCH_1. |
| <span id="statuscard--driver"></span>`driver` | string | no | The kernel driver that binds the card, such as HDA-Intel. |
| <span id="statuscard--name"></span>`name` | string | no | The card's name, as the driver states it, such as HDA Intel PCH. |

### status.pcm

The PCM device the endpoint plays through.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuspcm--device"></span>`device` | integer | no | The PCM device number on the card, this boot. |
| <span id="statuspcm--id"></span>`id` | string | no | The driver's name for the PCM, such as HDMI 0 or USB Audio. It is the part of the endpoint's name that outlives the number. |

### status.monitor

The monitor an HDMI or DisplayPort slot feeds, from the ELD the graphics driver wrote into the card. Absent while no monitor answers, and absent on every other connection type. An Intel HDMI codec binds a pin to the first free slot when a monitor appears, so a card with two monitors can swap which slot feeds which between plug events, and this object is where that shows.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmonitor--display"></span>`display` | string | no | The name of the Display the display operator publishes for the same monitor, which is the pairing identity monitor.liken.sh/id. |
| <span id="statusmonitor--manufacturer"></span>`manufacturer` | string | no | The monitor's three-letter PNP id, such as GSM. |
| <span id="statusmonitor--product"></span>`product` | string | no | The monitor's product code, four lowercase hexadecimal digits. |
| <span id="statusmonitor--name"></span>`name` | string | no | The monitor's name, from the same EDID descriptor the Display reports as model. |

### status.bluetooth

The speaker behind a Bluetooth endpoint. Absent on every other connection type.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusbluetooth--address"></span>`address` | string | no | The speaker's address, uppercase with colons. |
| <span id="statusbluetooth--name"></span>`name` | string | no | The name the speaker reports for itself. |
| <span id="statusbluetooth--peripheral"></span>`peripheral` | string | no | The name of the Peripheral the bluetooth operator publishes for this speaker, which is the address in lowercase with dashes. |
| <span id="statusbluetooth--codec"></span>`codec` | string | no | The A2DP codec the transport negotiated, present while the speaker is connected. |
| <span id="statusbluetooth--codecs"></span>`codecs` | []string | no | Every codec the speaker and this image both support, the one playing first. |

### status.format

The format the node runs at, from PipeWire's own Format parameter. Absent while the node is not running.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusformat--rate"></span>`rate` | integer | no | The sample rate in hertz. |
| <span id="statusformat--channels"></span>`channels` | integer | no | The channel count. |
| <span id="statusformat--positions"></span>`positions` | []string | no | The channel positions in order, in PipeWire's names: FL, FR, FC, LFE, RL, RR. |

### status.capabilities.*

The card's own controls that belong to this endpoint, keyed by the kernel's control name. A Playback control goes to the card's analog and USB sinks, and so does a control that names no direction, such as Auto-Mute Mode. An IEC958 Playback Switch goes to the HDMI slot of the same ordinal, and it is the only control an HDMI slot lists, because an HDMI PCM has no volume element. A jack control is not listed because it is read-only and feeds the Connected condition. A Bluetooth speaker lists nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--type"></span>`type` | string | no | What the control takes. One of: `integer`, `boolean`, `enumerated`. |
| <span id="statuscapabilities--min"></span>`min` | integer | no | The smallest number an integer control accepts. |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number an integer control accepts. |
| <span id="statuscapabilities--step"></span>`step` | integer | no | The step between numbers an integer control accepts, absent when every number in the range is accepted. |
| <span id="statuscapabilities--mindecibels"></span>`minDecibels` | string | no | The level at min, in decibels, when the control declares one, as a decimal string such as -65.25. The string form keeps the value exact where a float would not. |
| <span id="statuscapabilities--maxdecibels"></span>`maxDecibels` | string | no | The level at max, in decibels, when the control declares one. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value an enumerated control accepts. |
| <span id="statuscapabilities--channels"></span>`channels` | integer | no | How many channels the control carries. A write from spec sets every channel to the same value. |

### status.observed

The last value the operator read for each setting. The operator reads the card's control device on every event it delivers, and PipeWire's graph on every change it prints, so a change a person made with a knob or a client shows here without a poll.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--volume"></span>`volume` | integer | no | The level the endpoint plays at, as a percent of unity, from the node's gain or the Bluetooth device's own volume. An idle node reports no level of its own, so an idle endpoint reports the level the operator last wrote to it, which is the level it will run at, and nothing until a level is declared. |
| <span id="statusobserved--mute"></span>`mute` | boolean | no | Whether the endpoint is silent, on the same terms as volume. |
| <span id="statusobserved--codec"></span>`codec` | string | no | The codec a Bluetooth speaker plays with now. |
| <span id="statusobserved--controls"></span>`controls` | map[string]string | no | The value of every control in capabilities, in the same spelling spec.controls takes. A control with several channels reports the first channel's value. |

### status.claim

The claim that holds the endpoint now, and absent between holders. It answers which workload has the speakers.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusclaim--namespace"></span>`namespace` | string | no |  |
| <span id="statusclaim--name"></span>`name` | string | no |  |

### status.conditions[]

Connected reports that the endpoint can play now: a monitor on an HDMI slot, a plug in an analog jack, a connected speaker, and always on USB. Ready reports that PipeWire holds a node for it. The two carry the same facts as the no-monitor and no-sink taints on the device, for a person rather than the scheduler.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The name

The name is built from the hardware's own identity, so it survives
a reboot and a second card:

| Endpoint | Form | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `node-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | vendor, product, serial, PCM id | `usb-0573-1573-a34004801402-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `node-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45` |

The PCM id is the driver's name for the endpoint, `HDMI 0` or
`USB Audio`, lowercased with dashes. A USB card with a serial keeps
its `Sink` when it moves to another machine, and `status.node` says
where it is. A card with no serial that moves to another port
becomes a new `Sink`. A card that plays and records through one PCM
gives its `Source` the same name with `-capture` on the end.

On an Intel HDMI codec, `hdmi-0` names the card's first HDMI slot
and not a physical port. A pin binds to the first free slot when a
monitor appears, so on a card with two monitors the slot each one
lands in can change between plug events. `status.monitor` reports
which monitor the slot feeds now, and a machine with one HDMI
monitor never sees the difference.

## The resting layer

A declared field is a standing instruction. The operator compares
the declaration with the value it last read, and it writes the
hardware only where the two diverge. A declared control is
validated against `status.capabilities`: a name the card does not
declare, or a value out of its range, fails the pass with the reason
in the operator's log and is never written. An empty `spec` writes
nothing at all. The operator invents no value: an endpoint with no
declarations keeps whatever the hardware holds, except that every
sink starts at unity gain so that no hidden multiplier costs
resolution before the codec runs.

`volume`, `mute`, and `controls` apply at once, whether a claim
holds the endpoint or not. `codec` waits for the claim to end,
because a codec switch replaces the speaker's node and interrupts
playback, and a claim's own `codec` parameter wins while it holds
the speaker.

## Observation

`status.observed` follows two event sources and no timer. The
card's control device reports every control write from any process,
every jack change, every monitor change, and a knob turned on a USB
DAC. PipeWire's graph reports every node and device change. So a
change a person made with a knob, a remote, or a speaker's own
buttons shows in `observed` within about a second. A value the spec
declares is written back on the same event.
