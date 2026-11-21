---
title: Set endpoint volume and controls
weight: 40
description: "Set endpoint volume, mute an output, close a microphone, set a sound card control, declare a sink's channel layout, give a sink headroom, or let several claims share an endpoint, with kubectl and without a claim or an interruption to a playing pod. Use when a speaker needs a different default level, when another operator needs volume control, when a multichannel stream plays in stereo, when the sound drops out for a moment, or when two workloads must play through one speaker at once."
---

<a id="set-what-an-endpoint-rests-at"></a>

# Set endpoint volume and controls

Each speaker and microphone has a volume, a mute, and sound card
controls that stay set while no claim uses it. A pod that claims the
endpoint later starts at those settings. You set them with `kubectl`
on the endpoint's resource, and the same resource sets the channel
layout of a sink and how many claims can share an endpoint. None of
this needs a claim, and none of it interrupts a pod that is playing
or recording. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

The operator publishes each playback endpoint as a
[`Sink`](/docs/reference/sinks/) and each capture endpoint as a
[`Source`](/docs/reference/sources/). You write the settings you want
in `spec`. The operator reads the hardware's capabilities and current
values and writes them in `status`.

The operator applies the volume and mute from `spec` when you change
them and when the endpoint appears. It applies a control or a codec
from `spec` again whenever the hardware differs from it. A pod can
claim the speaker and play while the operator applies these settings.
The endpoint volume is separate from the volume of the pod's own
stream.

While a claim holds a Bluetooth speaker, the codec in the claim's
parameters applies. The codec in the `Sink`'s `spec` applies when
that claim ends.

## 1. See what is there

    kubectl get sinks
    kubectl get sources

Each row is one endpoint. The columns show the node it is on, how it
connects, its last level and mute, the claims that hold it, and
whether it is connected and ready. To see everything the operator
reports about one endpoint, read the whole resource:

    kubectl get sink kitchen-pci-0000-00-1f-3-hdmi-0 -o yaml

Two parts of `status` matter here. `capabilities` lists the controls
the sound card offers for this endpoint, with the range or the
choices of each. `observed` has the last value the operator read for
each setting, and the operator updates it when the hardware reports a
change. If you turn the knob on a USB DAC, press the volume button on
a Bluetooth speaker, or change the PipeWire graph from a client, the
new value shows in `observed` a few seconds later.

While nothing plays through an endpoint, the hardware has no level to
read. `observed` then shows the last level the operator wrote, from
your `spec` or from a volume request, because the endpoint starts at
that level. Until the operator writes a level, `observed` shows none.

## 2. Set the volume

    kubectl patch sink a0-ab-51-33-b7-12 --type merge \
      -p '{"spec":{"volume":{"level":40}}}'

`volume.level` is a percentage, where 100 is full level with no gain.
On an output of the sound card, it is the software gain that
PipeWire applies. On a Bluetooth speaker, it is the speaker's own
volume. The operator sends it over AVRCP when the speaker supports
absolute volume, so the number on the speaker's display moves too.
When the speaker does not support absolute volume, PipeWire applies
the level as a software gain.

The change takes effect at once, even while a pod is playing through
the speaker. The pod's own stream volume is a separate control on top
of this one, so the two do not conflict.

The operator writes your level when you change it and each time the
endpoint appears, which is when a speaker reconnects or PipeWire
builds the node again. In between, someone can press the speaker's
own button, a client can change the graph, or a remote's volume key
can change the level. The operator reports the new level in
`observed` and does not put your level back. A restart of the
operator alone writes nothing, so a level that a person chose before
the restart stays. When the machine boots, PipeWire builds every node
again, and each node gets your declared level and mute.
`status.applied` records the node and the declaration that the
operator last applied. If you never declare a level, the operator
sets each sink that PipeWire builds to 100.

A remote's volume key changes a `Sink` through the media operator.
For each press, the media operator writes a volume request in
`status.session.volumeAsk`, and this operator applies each new
request once. `volume.max` is the highest level that a request sets. It
defaults to 100. `volume.step` is how far one press moves the level,
and it defaults to 5. The media operator reads both:

    kubectl patch sink a0-ab-51-33-b7-12 --type merge \
      -p '{"spec":{"volume":{"level":40,"max":80,"step":2}}}'

A person at the speaker can still turn the volume above `max`.

## 3. Mute an output, or close a microphone

    kubectl patch sink kitchen-pci-0000-00-1f-3-hdmi-0 --type merge \
      -p '{"spec":{"mute":true}}'

The television goes silent. A player that holds the output keeps
running and plays into silence until you set `mute` back to `false`.

The same field on a `Source` closes a microphone:

    kubectl patch source kitchen-usb-0573-1573-a34004801402-usb-audio-capture \
      --type merge -p '{"spec":{"mute":true}}'

To close every microphone in the cluster, run the patch on each
`Source`:

    kubectl get sources -o name \
      | xargs -I{} kubectl patch {} --type merge -p '{"spec":{"mute":true}}'

## 4. Set a control on the sound card itself

Some cards have hardware controls of their own, such as a USB DAC's
volume, a laptop codec's headphone switch, or an input selector on a
card with several jacks. `status.capabilities` lists them under the
names the kernel uses, and you set them in `spec.controls` under the
same names. An integer control takes a number in its range, a switch
takes `on` or `off`, and a selector takes one of its listed values:

    kubectl patch sink kitchen-usb-0573-1573-a34004801402-usb-audio --type merge \
      -p '{"spec":{"controls":{"PCM Playback Volume":"96"}}}'

Before it writes, the operator checks the name and the value against
the capabilities. It does not write a name that the card lacks or a
value out of range. Instead, it posts a `SpecRefused` warning `Event`
on the `Sink` that names the control and the reason:

    kubectl describe sink kitchen-usb-0573-1573-a34004801402-usb-audio

Not every endpoint has controls. An HDMI output has only its
`IEC958 Playback Switch`, because an HDMI PCM has no volume control,
so use `volume.level` for its level. A Bluetooth speaker has no
controls.

<a id="5-take-a-declaration-back"></a>

## 5. Remove a declared setting

When you remove a field, the operator stops applying it, and the
hardware keeps the value it has at that moment. The exception is the
level of a sink with no `volume` and no `mute`: when PipeWire builds
that sink's node again, the operator sets it to 100, as for a sink
whose level you never declared. Removing `volume` also removes `max`
and `step`, so a remote's volume key steps by the defaults:

    kubectl patch sink a0-ab-51-33-b7-12 --type json \
      -p '[{"op":"remove","path":"/spec/volume"}]'

## 6. Set the channel layout

PipeWire plays a multichannel stream on the separate channels of a
sound card's sink only when it knows the position of each channel.
The operator reads the positions from an HDMI monitor's ELD and from
a USB device's channel map. The analog jack reports no speakers, so
its streams play in stereo until you declare the layout. To see the
layout of each sink:

    kubectl get sinks

To play 5.1 through the analog outputs, list the positions in the
order of the card's PCM channels:

    kubectl patch sink node-1-pci-0000-00-1f-3-alc257-analog --type merge \
      -p '{"spec":{"layout":["FL","FR","RL","RR","FC","LFE"]}}'

You can also use the field when a monitor or a device reports its
layout wrongly. A new layout takes effect only when PipeWire
restarts, and the restart ends every stream on the machine, so the
operator waits until nothing plays. While it waits, the `LayoutApplied`
condition of the `Sink` is `False` with the reason `AwaitingIdle`.
To return the sink to the layout its hardware reports, remove the
field:

    kubectl patch sink node-1-pci-0000-00-1f-3-alc257-analog --type json \
      -p '[{"op":"remove","path":"/spec/layout"}]'

The operator does not write the kernel's channel map, so no PCM
channel goes to a height speaker. A receiver can still play sound on
its height speakers by mixing it from the other channels. It plays
Dolby Atmos and DTS:X on its height speakers when the track reaches
it as a bitstream, and `status.passthrough` of the `Sink` lists the
formats that the sink sends that way. The
[`Sink` reference](/docs/reference/sinks/#the-channel-layout) lists
the layouts the operator selects and the reasons, and its section
[Encoded formats](/docs/reference/sinks/#encoded-formats) lists the
formats.

<a id="give-a-sink-headroom"></a>

## 7. Give a sink headroom when it drops out

A sink drops out when its sound card runs out of samples, which is an
underrun. With PCM an underrun is a gap of a few milliseconds. With a
bitstream it breaks the stream, and an A/V receiver mutes for a second
or more until it finds the stream again. PipeWire logs one line for
each underrun, and `count` is the number of underruns on that sink
since PipeWire started:

    kubectl -n liken-system logs ds/audio-operator -c pipewire | grep XRun
    [I][14:09:27.588955] pw.node | [ impl-node.c: 2258 node_xrun()] (liken.audio.card0-pcm3-29) XRun! rate:4096/192000 count:3 time:163089016944 delay:16 max:53 (0 suppressed)

The line names the sink's PipeWire node and its id,
`liken.audio.card0-pcm3-29` here. The `Sink` whose `status.nodeName` is
`liken.audio.card0-pcm3` is the one that underruns:

    kubectl get sinks -o custom-columns=NAME:.metadata.name,NODE:.status.node,PIPEWIRE:.status.nodeName

Give that sink headroom: samples that PipeWire keeps queued on the
card beyond what it needs, so that a small error in PipeWire's
estimate of the card's position does not empty it:

    kubectl patch sink node-1-pci-0000-00-1f-3-hdmi-0 --type merge \
      -p '{"spec":{"headroom":{"samples":4096}}}'

The value is in samples at the rate the sink runs, so the same value
is a different time at each rate:

| `samples` | At 192 kHz, a Dolby Digital Plus or TrueHD bitstream | At 48 kHz, PCM or a Dolby Digital bitstream |
|---|---|---|
| 1024 | 5 ms | 21 ms |
| 4096 | 21 ms | 85 ms |

Every stream on the sink plays later by the headroom, so set it only
on a sink that underruns, and use the smallest value that stops the
`XRun!` lines. A new headroom takes effect when PipeWire restarts, so
the operator waits until nothing plays, as it does for a layout. To
remove the headroom:

    kubectl patch sink node-1-pci-0000-00-1f-3-hdmi-0 --type json \
      -p '[{"op":"remove","path":"/spec/headroom"}]'

<a id="share-an-endpoint-between-claims"></a>

## 8. Share an endpoint between claims

The operator allocates an endpoint to one claim at a time, so a pod
with a second claim for the same speaker stays Pending until the
first claim ends. PipeWire can mix several streams into one sink, and
`spec.sharedClaims` sets how many claims can hold the endpoint at
once:

    kubectl patch sink kitchen-pci-0000-00-1f-3-hdmi-0 --type merge \
      -p '{"spec":{"sharedClaims":4}}'

The value must be 2 or more. Neither the hardware nor PipeWire
limits it, so choose the number of workloads that you expect to play
at once. The operator publishes the device with a capacity named
`claims` of that size. A claim that requests one unit of `claims`
shares the endpoint, as
[Share an output with other claims](/docs/guides/claim/#share-an-output-with-other-claims)
shows. A claim that requests no `claims` still holds the endpoint
alone, so your existing workloads keep exclusive use. The same field
on a `Source` lets several claims record from one microphone.

A new count applies to the next allocation. If you lower it or remove
it, the claims that already hold the endpoint keep it, and a new claim
stays Pending until the endpoint has room under the new count:

    kubectl patch sink kitchen-pci-0000-00-1f-3-hdmi-0 --type json \
      -p '[{"op":"remove","path":"/spec/sharedClaims"}]'

<a id="give-someone-else-the-remote"></a>

## Grant volume control

`Sink` and `Source` are ordinary Kubernetes resources, so RBAC
decides who can change them. A role that can patch `sinks` but not
`sources` suits a wall remote or a home automation rule that sets the
volume and must never change a microphone:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: volume-remote
    rules:
      - apiGroups: [audio.liken.sh]
        resources: [sinks]
        verbs: [get, list, watch, patch]

When two writers change one resource, use server-side apply, which
tracks the fields of each writer separately. A remote that applies
only `spec.volume.level` under its own field manager and a person who
sets `spec.controls` do not overwrite each other. If both write the
same field, the API server returns a conflict instead of keeping the
last write.
