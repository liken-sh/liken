---
title: Set what an endpoint rests at
weight: 40
---

# Set what an endpoint rests at

This guide shows you how to change the volume of a speaker, mute
an output, close a microphone, and set a sound card's own controls
with `kubectl`. None of it needs a claim, and none of it interrupts
a pod that is playing or recording. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

Every output and input the operator publishes has a resource of its
own: a [`Sink`](/docs/reference/sinks/) for something that plays,
and a [`Source`](/docs/reference/sources/) for something that
records. The operator fills in `status` with what it knows about
the hardware and what it last read from it. You fill in `spec` with
what you want, and the operator keeps the hardware there. A pod
that plays through the speaker still holds it through a claim, and
still has its own stream volume. Your declaration is the level the
speaker itself sits at, underneath that.

## 1. See what is there

    kubectl get sinks
    kubectl get sources

Each row is one endpoint: the node it is on, how it connects, the
level and mute it was last read at, which claim holds it, and
whether it is connected and ready. To see everything the operator
knows about one, read the whole resource:

    kubectl get sink kitchen-pci-0000-00-1f-3-hdmi-0 -o yaml

Two parts of `status` are worth a look. `capabilities` lists the
controls the sound card itself offers for this endpoint, with the
range or the choices each one takes. `observed` is the last value
the operator read for each setting. It keeps up with the hardware
on its own: turn a knob on a USB DAC, press the volume button on a
Bluetooth speaker, or let a client change the graph, and the new
value shows here within about a second.

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
control on top of this one, so the two never fight.

Once you have declared a volume, it stays declared. If the speaker
reconnects, the operator restarts, or some client changes the level
behind your back, the operator writes your value back. If you never
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
of its own; use `volume` for its level. A Bluetooth speaker has
none.

## 5. Take a declaration back

Remove the field, and the operator stops enforcing it. The hardware
keeps whatever value it has at that moment, because the operator
never makes up a value on its own:

    kubectl patch sink a0-ab-51-33-b7-12 --type json \
      -p '[{"op":"remove","path":"/spec/volume"}]'

## Give someone else the remote

Because the resources are ordinary Kubernetes objects, RBAC decides
who may change them. A role that can patch `sinks` but not
`sources` is the right shape for a wall remote or a home automation
rule that sets volume and must never touch a microphone:

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
