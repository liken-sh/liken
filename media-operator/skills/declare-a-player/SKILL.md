---
name: declare-a-player
description: "Declare a Player for the screen and speakers a machine is connected to, take it from created to a Screen condition of Present, and prove it plays. Use when a machine is connected to a television or a monitor and should play media on it, when a Player's idle pod stays Pending, or when a film's Dolby or DTS track should reach an A/V receiver encoded."
---

This skill is the guide at https://liken.sh/media/docs/guides/declare-a-player/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Declare a Player

A `Player` describes one unit of equipment: a screen, the outputs that
play its sound, the GPU that draws the picture, and the controllers
that drive it. In this guide you declare a `Player` for a machine that
is connected to a television or a monitor, read its status until the
screen is present, and start a `Play` to check it. At the end, the
screen shows the idle screen and a remote controls the `Player`.

You need:

* The operator and its bus, from the [install](https://liken.sh/media/docs/guides/install/).
* The [`display-operator`](https://liken.sh/display/) and the
  [`audio-operator`](https://liken.sh/audio/) installed, with a
  `DeviceClass` for a screen, a render node, and an audio output. Each
  operator's install guide has the YAML for its class. The class names
  in this guide are examples.
* A `Remote` for each controller of the `Player`, from
  [Map a new controller](https://liken.sh/media/docs/guides/mapping-a-controller/). A
  `Player` can have no controllers, and you can add them later.
* `kubectl` access to the namespace.

## 1. Confirm the machine publishes the hardware

Every pod of a `Player` must run on the machine that has all of the
`Player`'s devices, so first check what that machine publishes.
`display-operator` publishes a device for each connector, and
`audio-operator` publishes one for each output:

    kubectl get resourceslice <node>-display.liken.sh -o yaml
    kubectl get displays
    kubectl get sinks

The monitor must appear as a `Display`, and the outputs as `Sinks`,
on that node. A connector with no monitor is published with a
`disconnected` taint, and a claim on it stays `Pending`.

A monitor that shows another input can send no hotplug event, and
`display-operator` does not poll for one. If the `Display` for a
connected monitor is missing, switch the monitor to this machine's
input, and the `Display` appears within seconds.

## 2. Write the Player

Create the `Player` in the namespace of its `Remotes`. Give each
device's class, and when the machine has more than one device of a
class, add a selector to choose one:

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

The selectors are CEL expressions over the attributes that each
hardware operator publishes, as in a hand-written `ResourceClaim`.
Guard an attribute that comes from the monitor with `has()`, because
an empty connector has no such attribute, and a selector that reads a
missing attribute fails the whole allocation. The claim guides of
[`display-operator`](https://liken.sh/display/docs/guides/claim/) and
[`audio-operator`](https://liken.sh/audio/docs/guides/claim/) list the
attributes, and selectors that still match after you change the
cabling.

A `Player` with a `display` needs a `render`, because `mpv` decodes
and draws through the GPU, and without one it plays nothing. For a
`Player` that plays sound only, omit both `display` and `render`.
[Players](https://liken.sh/media/docs/reference/players/) describes every field.

## 3. Read the status

The operator creates a claim for the screen, named
`<player>-idle-devices` and owned by the `Player`, and a pod named
`<player>-idle` that draws the idle screen:

    kubectl get player den
    NAME   ZONE   ACTIVITY   PLAY   IDLE                          SCREEN    AGE
    den    den    Idle              media.liken.sh/idle-screen    Present   40s

`SCREEN` shows the reason of the `Screen` condition. `Present` means
that the screen's `Display` reports a panel on its connector. The
condition does not exist until the claim has been allocated once, so
the column is empty right after you apply the `Player`. To see which
screen and outputs the claims were allocated, read the status:

    kubectl get player den -o jsonpath='{.status.screen}{"\n"}{.status.sinks}{"\n"}'

`status.sinks` is empty until the first `Play`, because the operator
reads the outputs from the allocated claim of a playback pod.

## 4. When the idle pod stays Pending

A `Pending` idle pod means that the claim for the screen was not
allocated. The reason of the `Screen` condition shows why:

* `PanelAway`: the `Display` reports no panel on the connector,
  because the monitor shows another input or its cable is
  disconnected. Switch the input or connect the cable, and the
  scheduler starts the pod. `display-operator` reports a panel as
  gone only after the connector has read no panel for 5 seconds, so
  an A/V receiver in standby that drops hot plug detect for a moment
  does not change the condition.
* `NoDisplay` or `NotReported`: no `Display` exists for the monitor
  that the claim was allocated to. Go back to step 1 and check that
  the machine publishes the screen.
* An empty column: the claim has not been allocated. Read the claim's
  events to find a selector that matched no device, or matched
  devices on two machines:

        kubectl describe resourceclaim den-idle-devices

A selector that reads an attribute without `has()` fails on every
device that lacks the attribute. When every candidate device lacks it,
the claim matches no device.

When a pod's events say `Insufficient memory` or `Insufficient cpu`,
the machine that has the screen lacks free memory or cpu for the
pod's requests, and the pod cannot run on another machine. The idle
pod keeps running during a film, so with the default values the
machine needs room for both pods at once: 688Mi of memory and 105m of
cpu, of which the idle pod requests 96Mi and 5m. Free memory on the
machine, lower the requests as the
[install guide](https://liken.sh/media/docs/guides/install/#container-resources) shows, or
connect the screen to a machine with more memory.

    kubectl describe pod den-idle

## 5. Prove it plays

Create a `Play` for the `Player`, with one file that the machine can
read:

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

The `ACTIVITY` of the `Player` changes to `Starting`, and then to
`Playing`. The film appears on the screen, and its sound plays on the
outputs that you selected. Press a button on the remote to check that
it controls the `Player`. To stop the film, delete the `Play`, and the
idle screen returns:

    kubectl delete play den-check

When a `Play` stays `Starting`, the scheduler could not place its pod,
and the pod's events name the device that was not allocated. A `uri`
with a scheme that the operator does not support fails the `Play`
before the operator creates a pod.
[Plays](https://liken.sh/media/docs/reference/plays/) describes the forms of an item.

## Choose what the screen does when idle

By default `media-operator` draws the idle screen. The fields of
`spec.idle` change that, and each field you set on a `Player`
overrides the same field of the default `MediaPreferences`:

* `controller` gives the screen to another operator, such as the
  browser of `library-operator`.
  [Hand the idle screen to another controller](https://liken.sh/media/docs/guides/handing-the-idle-screen-to-another-controller/)
  describes both sides.
* `fadeAfterSeconds` fades the idle screen to black after that many
  seconds with no activity. Zero turns the fade off.
* `offAfterSeconds` turns the panel off after that many seconds with
  no activity. The idle client treats a value below `fadeAfterSeconds`
  as `fadeAfterSeconds`, and the panel turns off only when a `Display`
  exists for the screen. Zero keeps the panel on. Set it to zero for a
  monitor that other machines also use, because the panel cannot tell
  which machine's input it is showing.

To set a default for every `Player` in the cluster, use
[`MediaPreferences`](https://liken.sh/media/docs/reference/mediapreferences/). Set a field
on a `Player` only when that `Player` needs a different value.

## Send Dolby and DTS to the receiver

With `passthrough: On`, `mpv` sends a film's Dolby and DTS tracks to
an A/V receiver encoded, and the receiver decodes them. Only then do
the receiver's height speakers play Dolby Atmos and DTS:X, because
`mpv` decodes a track to its channel bed and drops the height
objects. `mpv` still decodes a track in a format that the receiver
does not accept.

You need:

* A `Receiver` that belongs to the `Player`, with a `Reachable`
  condition of `True`. The equipment operator's
  [install guide](https://liken.sh/equipment/docs/guides/install/#put-it-under-a-player)
  wires it in.
* Exactly one entry in `spec.sinks`, because `mpv` sends one stream
  to one sink.
* A `Sink` that lists the formats that the receiver accepts.
  `audio-operator` writes the list from the receiver's own report, and
  an empty list means that the sink accepts none:

        kubectl get sink <name> -o jsonpath='{.status.passthrough}{"\n"}'
        ["AC3","EAC3","DTS","DTS-HD","TrueHD"]

Set the field on the `Player`:

    kubectl patch player den -n media --type merge -p '{"spec":{"passthrough":"On"}}'

Passthrough is off by default, because it changes how volume works.
PipeWire cannot scale an encoded track, so while one plays, the sink's
level and mute have no effect. The remote's volume keys change the
receiver's own volume, as they do for every `Player` whose volume a
reachable `Receiver` sets.

The operator reads the sink's formats when it creates the playback
pod, from the sink in the `Player`'s `status.sinks`. So `mpv` decodes
the tracks even with passthrough on in these cases:

* The first `Play` that is allocated the `Player`'s sink. The
  operator creates the pod before the scheduler allocates its sink,
  so `status.sinks` has no sink yet. Each later `Play` reads it.
* A `Play` that starts while the receiver is waking up. A receiver in
  standby can report fewer formats or none, and the sink lists only
  those until the receiver is on. Stop the `Play` and start it again
  when the receiver is on.

For each `Play` that starts on a `Player` with `passthrough: On`, the
operator logs a line with the codecs that `mpv` passes through and the
sink, or the reason that `mpv` decodes the tracks:

    kubectl logs -n liken-system deploy/media-operator | grep passthrough

To check that the receiver gets the encoded stream, read its front
panel or its sound mode during the film. It shows the format, such as
Dolby Atmos or DTS:X. A receiver that shows multichannel PCM is
receiving a decoded track.
