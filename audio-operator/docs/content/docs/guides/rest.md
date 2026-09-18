---
title: Set endpoint volume and controls
weight: 40
description: "Set endpoint volume, mute an output, close a microphone, or set a sound card control with kubectl, with no claim and no interruption to a playing pod. Use when a speaker needs a different default level or when another operator needs volume control."
---

<a id="set-what-an-endpoint-rests-at"></a>

# Set endpoint volume and controls

This guide shows how to change an endpoint's volume, mute an output,
close a microphone, and set a sound card's controls with `kubectl`.
These changes do not need a claim. They do not interrupt a pod that
is playing or recording. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

Every output and input the operator publishes has its own resource:
a [`Sink`](/docs/reference/sinks/) for playback and a
[`Source`](/docs/reference/sources/) for capture. The operator writes
hardware facts and the latest readings to `status`. You write desired
settings to `spec`, and the operator reapplies each declared setting
when the hardware differs from it. A pod can still claim the speaker
while the operator applies these settings. The pod's stream volume is
separate from the endpoint volume. A claim's codec parameter takes
precedence while it allocates a Bluetooth speaker. A codec declared on
the `Sink` takes effect after that claim ends.

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
level you declared, which is the level it will start at. Until you
declare one, it shows no level at all.

## 2. Set the volume

    kubectl patch sink a0-ab-51-33-b7-12 --type merge \
      -p '{"spec":{"volume":40}}'

`volume` is a percentage, where 100 is full level with no gain
applied. For an output on the sound card, this is the software
level PipeWire applies. For a Bluetooth speaker, it is the
speaker's own volume: the operator sends it over AVRCP when the
speaker supports absolute volume, so the number on the speaker's
display moves too.

The change takes effect at once, even while a pod is playing
through the speaker. The pod's own stream volume is a separate
control on top of this one, so the two never conflict.

Once you have declared a volume, it stays declared. If the speaker
reconnects, the operator restarts, or some client changes the level
on its own, the operator writes your value back. If you never
declare one, the endpoint rests at 100.

## 3. Mute an output, or close a microphone

    kubectl patch sink kitchen-pci-0000-00-1f-3-hdmi-0 --type merge \
      -p '{"spec":{"mute":true}}'

The television goes silent. A player that holds it keeps running,
and it plays into silence until you set `mute` back to `false`.

The same field on a `Source` closes a microphone:

    kubectl patch source usb-0573-1573-a34004801402-usb-audio-capture \
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

    kubectl patch sink usb-0573-1573-a34004801402-usb-audio --type merge \
      -p '{"spec":{"controls":{"PCM Playback Volume":"96"}}}'

The operator checks the name and the value against the capability
before it writes. A name the card does not have, or a value out of
range, is skipped and logged rather than written:

    kubectl -n liken-system logs ds/audio-operator | grep controls

Not every endpoint has controls. An HDMI output has only its
`IEC958 Playback Switch`, because an HDMI PCM has no volume control
of its own. Use `volume` for its level. A Bluetooth speaker has
none.

<a id="5-take-a-declaration-back"></a>

## 5. Remove a declared setting

Remove the field, and the operator stops enforcing it. The hardware
keeps whatever value it has at that moment, because the operator
never makes up a value on its own:

    kubectl patch sink a0-ab-51-33-b7-12 --type json \
      -p '[{"op":"remove","path":"/spec/volume"}]'

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
apart. A remote that applies only `spec.volume` under its own field
manager and a person who sets `spec.controls` never overwrite each
other. If they do collide on one field, the API server reports a
conflict instead of silently taking the last write.
