---
title: Set endpoint volume and controls
weight: 40
description: "Set endpoint volume, mute an output, close a microphone, set a sound card control, or state a sink's channel layout with kubectl, with no claim and no interruption to a playing pod. Use when a speaker needs a different default level, when another operator needs volume control, or when a multichannel stream plays in stereo."
---

<a id="set-what-an-endpoint-rests-at"></a>

# Set endpoint volume and controls

This guide shows how to change an endpoint's volume, mute an output,
close a microphone, and set a sound card's controls with `kubectl`.
These changes do not need a claim. They do not interrupt a pod that
is playing or recording. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

Every output and input that the operator publishes has its own
resource: a [`Sink`](/docs/reference/sinks/) for playback and a
[`Source`](/docs/reference/sources/) for capture. You write the
settings you want in `spec`, and the operator writes the hardware's
facts and its latest readings in `status`.

The operator applies a volume and mute from `spec` when you change
them and when the endpoint appears. It applies a control or a codec
from `spec` again whenever the hardware drifts from it. None of this
needs the speaker to be free: a pod can claim it and play while the
operator applies the settings. The endpoint volume is separate from
the volume of the pod's own stream.

While a claim holds a Bluetooth speaker, the codec in the claim's
parameters applies. The codec in the `Sink`'s `spec` takes effect when
that claim ends.

## 1. See what is there

    kubectl get sinks
    kubectl get sources

Each row is one endpoint: the node it is on, how it connects, the
level and mute it was last read at, which claim holds it, and
whether it is connected and ready. To see everything the operator
reports about one, read the whole resource:

    kubectl get sink kitchen-pci-0000-00-1f-3-hdmi-0 -o yaml

Read two parts of `status`. `capabilities` lists the controls the
sound card offers for this endpoint, including the range or choices
for each control. `observed` is the last value the operator read for
each setting. The operator updates it when the hardware reports a
change.
Turn a knob on a USB DAC, press the volume button on a Bluetooth
speaker, or let a client change the graph, and the new value shows
here within about a second. An endpoint that nothing is playing
through has no level of its own to read. `observed` then shows the
level the operator last wrote to it, from your declaration or from a
volume ask, which is the level it will start at. Until the operator
writes one, it shows no level at all.

## 2. Set the volume

    kubectl patch sink a0-ab-51-33-b7-12 --type merge \
      -p '{"spec":{"volume":{"level":40}}}'

`volume.level` is a percentage, where 100 is full level with no gain
applied. For an output on the sound card, this is the software
level PipeWire applies. For a Bluetooth speaker, it is the
speaker's own volume: the operator sends it over AVRCP when the
speaker supports absolute volume, so the number on the speaker's
display moves too.

The change takes effect at once, even while a pod is playing
through the speaker. The pod's own stream volume is a separate
control on top of this one, so the two never conflict.

The declared level is where the endpoint starts. The operator writes
it when you change it and each time the endpoint appears: a speaker
that reconnects, or a node that PipeWire builds again. Between those
moments the level can move. A press of the speaker's own button, a
client that changes the graph, and a remote's volume key all change
it, and the operator reports the new level in `observed` and does not
write your value back. An operator restart writes nothing, so the
level a person chose before the restart stays. If you never declare a
level, a node that PipeWire builds while the operator runs starts at
100.

A remote's volume key reaches a `Sink` through the media operator. It
writes each press as an ask in `status.session.volumeAsk`, and the
operator applies each new ask once. `volume.max` is the highest level
an ask sets, 100 unless you declare it, and `volume.step` is how far
one press moves the level, 5 unless you declare it:

    kubectl patch sink a0-ab-51-33-b7-12 --type merge \
      -p '{"spec":{"volume":{"level":40,"max":80,"step":2}}}'

A person at the speaker can still turn it above `max`.

## 3. Mute an output, or close a microphone

    kubectl patch sink kitchen-pci-0000-00-1f-3-hdmi-0 --type merge \
      -p '{"spec":{"mute":true}}'

The television goes silent. A player that holds it keeps running,
and it plays into silence until you set `mute` back to `false`.

The same field on a `Source` closes a microphone:

    kubectl patch source kitchen-usb-0573-1573-a34004801402-usb-audio-capture \
      --type merge -p '{"spec":{"mute":true}}'

To close every microphone in the house at once, run that patch over
the list from `kubectl get sources`.

## 4. Set a control on the sound card itself

Some cards have their own hardware controls: a USB DAC's volume, a
laptop codec's headphone switch, an input selector on a card with
several jacks. `status.capabilities` lists them under the names the
kernel uses, and you set them in `spec.controls` under the same
names. An integer control takes a number in its range, a switch
takes `on` or `off`, and a selector takes one of its listed values:

    kubectl patch sink kitchen-usb-0573-1573-a34004801402-usb-audio --type merge \
      -p '{"spec":{"controls":{"PCM Playback Volume":"96"}}}'

The operator checks the name and the value against the capability
before it writes. A name the card does not have, or a value out of
range, is skipped and logged rather than written:

    kubectl -n liken-system logs ds/audio-operator | grep controls

Not every endpoint has controls. An HDMI output has only its
`IEC958 Playback Switch`, because an HDMI PCM has no volume control
of its own. Use `volume.level` for its level. A Bluetooth speaker has
none.

<a id="5-take-a-declaration-back"></a>

## 5. Remove a declared setting

Remove the field, and the operator stops applying it. The hardware
keeps whatever value it has at that moment, because the operator
never makes up a value on its own. Removing `volume` also removes
`max` and `step`, and a remote's volume key then steps by the
defaults:

    kubectl patch sink a0-ab-51-33-b7-12 --type json \
      -p '[{"op":"remove","path":"/spec/volume"}]'

## 6. Set the channel layout

A sink of a sound card plays a multichannel stream on its own
channels only when PipeWire knows the position of each one. The
operator reads the positions from an HDMI monitor's ELD and from a
USB device's channel map. The analog jack reports no speakers, so
its streams play in stereo until you state the layout. See which
layout each sink has:

    kubectl get sinks

To play 5.1 through the analog outputs, list the positions in the
order of the card's PCM slots:

    kubectl patch sink node-1-pci-0000-00-1f-3-alc257-analog --type merge \
      -p '{"spec":{"layout":["FL","FR","RL","RR","FC","LFE"]}}'

The same field overrides a monitor or a device that reports its
layout wrong. A new layout restarts PipeWire, and the restart ends
every stream on the machine. So the operator waits until nothing
plays. While it waits, the `Sink`'s `LayoutApplied` condition is
`False` with the reason `AwaitingIdle`. Remove the field to return
the sink to the layout its hardware reports:

    kubectl patch sink node-1-pci-0000-00-1f-3-alc257-analog --type json \
      -p '[{"op":"remove","path":"/spec/layout"}]'

The operator sends PCM and does not write the kernel's channel map.
So a height speaker plays only what a receiver makes from the other
channels, and Dolby Atmos and DTS:X do not reach the receiver. The
[`Sink` reference](/docs/reference/sinks/#the-channel-layout) gives
the layouts the operator selects and the reasons.

<a id="give-someone-else-the-remote"></a>

## Grant volume control

Because the resources are ordinary Kubernetes objects, RBAC decides
who may change them. A role that can patch `sinks` but not
`sources` fits a wall remote or a home automation rule that sets
volume and must never touch a microphone:

    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    metadata:
      name: volume-remote
    rules:
      - apiGroups: [audio.liken.sh]
        resources: [sinks]
        verbs: [get, list, watch, patch]

If two writers share one resource, server-side apply keeps them
apart. A remote that applies only `spec.volume.level` under its own field
manager and a person who sets `spec.controls` never overwrite each
other. If they do collide on one field, the API server reports a
conflict instead of silently taking the last write.
