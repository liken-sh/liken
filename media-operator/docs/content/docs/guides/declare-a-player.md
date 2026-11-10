---
title: Declare a Player
weight: 15
description: "Declare a Player for the screen and speakers a machine is connected to, take it from created to a Screen condition of Present, and prove it plays. Use when a machine is connected to a television or a monitor and should play media on it, when a Player's idle pod stays Pending, or when a film's Dolby or DTS track should reach an A/V receiver encoded."
---

# Declare a Player

A `Player` is one unit of equipment: a screen, the outputs its sound
goes to, the GPU that draws, and the controllers that drive it. This
guide declares one for a machine that is connected to a television or
a monitor, reads its status until the screen is present, and proves
it with one `Play`. At the end, the unit shows its idle screen and a
remote drives it.

You need:

* The operator and its bus, from the [install](/docs/guides/install/).
* The [`display-operator`](https://liken.sh/display/) and the
  [`audio-operator`](https://liken.sh/audio/) installed, with the
  consumer classes your cluster names for a screen, a render node,
  and an output. Each operator's install guide gives the YAML for
  its class, and the class names below are examples.
* A `Remote` for each controller the unit owns, from
  [Map a new controller](/docs/guides/mapping-a-controller/). A
  `Player` with no controllers is fine; add them later.
* `kubectl` access to the namespace.

## 1. Confirm the machine publishes the hardware

The scheduler places every pod of a `Player` on the one machine that
owns all of its devices, so start with what that machine publishes.
The display operator publishes one device per connector, and the
audio operator one per output:

    kubectl get resourceslice <node>-display.liken.sh -o yaml
    kubectl get displays
    kubectl get sinks

The monitor you want must appear as a `Display`, and the outputs you
want as `Sinks` on that node. A connector with no monitor publishes
with a `disconnected` taint, and a claim on it parks.

A monitor that is switched to another input sends no hotplug event,
and the display operator polls nothing. If the `Display` for a
connected monitor is missing, switch the monitor to this machine's
input, and it appears within seconds.

## 2. Write the Player

Declare the unit in the namespace its `Remotes` are in. Name each
device by class, and narrow the class with a selector where the
machine has more than one of a kind:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Player
    metadata:
      name: den
      namespace: media
    spec:
      zone: den
      displayName: Den Television
      display:
        class: display-output
        displayName: Television
        selector: |
          has(device.attributes["display.liken.sh"].model) &&
          device.attributes["display.liken.sh"].model == "LG TV"
      render:
        class: gpu-render
      sinks:
        - class: audio-output
          displayName: Television Speakers
          selector: |
            has(device.attributes["audio.liken.sh"].sink) &&
            device.attributes["audio.liken.sh"].connectionType == "hdmi"
      remotes:
        - name: den-remote
          displayName: Den Remote
    EOF

The selectors are the same CEL a hand-written claim would use, over
the attributes each hardware operator publishes. Guard an attribute
that comes from the monitor with `has()`, because it is absent on an
empty connector, and a selector that reads a missing attribute fails
the whole allocation. The display operator's
[claim guide](https://liken.sh/display/docs/guides/claim/) and the
audio operator's [claim guide](https://liken.sh/audio/docs/guides/claim/)
list the attributes and the selectors that survive a re-cabling.

`render` is required for video. `mpv` decodes and draws through the
GPU, and a `Player` with a display and no render node plays nothing.
Omit `display` and `render` together for a unit that plays sound
alone. [Players](/docs/reference/players/) describes every field.

## 3. Read the status

The operator creates a persistent claim for the screen and a pod that
draws the idle screen, named `<player>-idle`:

    kubectl get player den
    NAME   ZONE   ACTIVITY   PLAY   IDLE                          SCREEN    AGE
    den    den    Idle              media.liken.sh/idle-screen    Present   40s

`SCREEN` is the reason of the `Screen` condition. `Present` means the
screen's `Display` reports a panel on its connector, and the idle
screen is on it. The condition is absent until the claim resolves
once, so an empty column right after the apply is normal. Read the
whole status for the screen and the outputs the claim resolved to:

    kubectl get player den -o jsonpath='{.status.screen}{"\n"}{.status.sinks}{"\n"}'

`status.sinks` fills after the first `Play`, because the operator
reads the outputs from an allocated playback claim.

## 4. When the idle pod stays Pending

A `Pending` idle pod means the claim on the screen did not allocate.
The `Screen` condition says why:

* `PanelAway`: the `Display` reports no panel on the connector. The
  monitor shows another input, or its cable is out. Switch the input
  or seat the cable, and the pod schedules on its own. This park is
  by design, and the pod stays `Pending` until the panel returns.
  `display-operator` reports a panel gone only after its connector
  has read no panel for 5 seconds, so an A/V receiver in standby that
  drops hot plug detect for a moment does not move the condition.
* `NoDisplay` or `NotReported`: no `Display` carries the monitor the
  claim remembers. Go back to step 1 and confirm the machine
  publishes the screen.
* An empty column: the claim has not resolved. Read the claim's
  events for the selector that matched nothing, or matched devices
  on two machines:

        kubectl describe resourceclaim den-idle-devices

A selector that reads an attribute without `has()` fails on every
device that lacks it. If every candidate lacks that attribute, the
resulting claim matches no device.

A pod whose events say `Insufficient memory` or `Insufficient cpu`
has a different cause: the machine that has the screen has too little
memory or cpu left for the pod's requests. Every pod of a `Player`
must run on that machine, so the scheduler cannot place it on another
one. At the defaults, the idle pod requests 96Mi of memory and 5m of
cpu. A playback pod requests 592Mi and 100m in all: 432Mi and 80m for
the player, 144Mi and 10m for the display, and 16Mi and 10m for the
command sidecar. The idle pod keeps running under a film, so a machine
must hold both pods at once: about 688Mi and 105m. Free memory on the
machine, lower the requests as the
[install guide](/docs/guides/install/#container-resources) shows, or
connect the screen to a machine with more.

    kubectl describe pod den-idle

## 5. Prove it plays

Create a `Play` that names the unit, with one file the machine can
reach:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: den-check
      namespace: media
    spec:
      players: [den]
      items:
        - uri: nfs://nas/media/movies/Example (2019)/Example.mkv
    EOF

The `Player`'s `ACTIVITY` goes to `Starting`, then `Playing`, and the
film is on the screen with its sound on the outputs you named. Press
a button on the remote to confirm it drives the unit. Delete the
`Play` to stop, and the idle screen returns:

    kubectl delete play den-check

A `Play` that stays `Starting` has a pod that did not schedule, and
its events name the device that did not allocate. A `uri` scheme the
operator does not know fails the `Play` before any pod exists.
[Plays](/docs/reference/plays/) describes the item forms.

## Choose what the screen does when idle

The operator's own idle screen is the default. Three fields on
`spec.idle` change it, and each one overrides the default
`MediaPreferences` on its own:

* `controller` hands the screen to another operator, such as the
  library operator's browser.
  [Hand the idle screen to another controller](/docs/guides/handing-the-idle-screen-to-another-controller/)
  covers both sides.
* `fadeAfterSeconds` fades the idle screen to black after that much
  quiet. `offAfterSeconds` darkens the panel itself after that much
  quiet, at least `fadeAfterSeconds`, and only where a `Display`
  exists for the screen.
* Set `offAfterSeconds: 0` for a monitor that other machines share.
  The panel cannot say whose input it would darken.

Set the defaults once for every unit in the cluster's
[`MediaPreferences`](/docs/reference/mediapreferences/), and set a
field on a `Player` only where that unit differs.

## Send Dolby and DTS to the receiver

With `passthrough: On`, a film's Dolby and DTS tracks go to an A/V
receiver encoded, and the receiver decodes them itself. That is the
only way the receiver's height speakers play Dolby Atmos and DTS:X,
because `mpv` decodes a track to its channel bed and drops the height
objects. A track whose format the receiver does not take is decoded,
as it is with passthrough off.

You need:

* A `Receiver` under the `Player`, with a `Reachable` condition of
  `True`. The equipment operator's
  [install guide](https://liken.sh/equipment/docs/guides/install/#put-it-under-a-player)
  wires it in.
* Exactly one entry in `spec.sinks`, because one `mpv` stream goes to
  one sink.
* A sink that lists the formats the receiver accepts. The audio
  operator writes them from the receiver's own report, and an empty
  answer means the sink takes none:

        kubectl get sink <name> -o jsonpath='{.status.passthrough}{"\n"}'
        ["AC3","EAC3","DTS","DTS-HD","TrueHD"]

Set the field on the `Player`:

    kubectl patch player den -n media --type merge -p '{"spec":{"passthrough":"On"}}'

The setting is off by default, because it changes how the volume
works. PipeWire cannot scale an encoded track, so while one plays, the
sink's level and mute do nothing. The remote's volume keys move the
receiver's own volume, as they do for every `Player` whose level a
reachable `Receiver` sets.

The operator reads the formats when it creates the playback pod, from
the sink in `status.sinks`. So two plays decode although the setting
is on:

* The first play after the `Player`'s first sink allocation. The
  operator creates the pod before the scheduler allocates its sink,
  so `status.sinks` names no sink yet. Every later play reads it.
* A play that starts while the receiver is still waking. A receiver in
  standby can report fewer formats or none, and the sink then lists
  only those until the receiver is on. Stop the play and start it
  again when the receiver is on.

The operator writes one line for each play start on a `Player` with
`passthrough: On`. The line names the codecs `mpv` passes through and
the sink, or says why the play decodes:

    kubectl logs -n liken-system deploy/media-operator | grep passthrough

To confirm that the receiver gets the bitstream, read its front panel
or its sound mode during the film. It names the format, such as Dolby
Atmos or DTS:X. A receiver that shows multichannel PCM gets a decoded
track.
