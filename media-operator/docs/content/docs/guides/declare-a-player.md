---
title: Declare a Player
weight: 15
description: "Declare a Player for the screen and speakers a machine is connected to, take it from created to a Screen condition of Present, and prove it plays. Use when a machine is connected to a television or a monitor and should play media on it, or when a Player's idle pod stays Pending."
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
* The [`display-operator`](https://display.liken.sh) and the
  [`audio-operator`](https://audio.liken.sh) installed, with the
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
      namespace: house
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
[claim guide](https://display.liken.sh/docs/guides/claim/) and the
audio operator's [claim guide](https://audio.liken.sh/docs/guides/claim/)
list the attributes and the selectors that survive a re-cabling.

`render` is required for video. `mpv` decodes and draws through the
GPU, and a `Player` with a display and no render node plays nothing.
Omit `display` and `render` together for a unit that plays sound
alone. [Players](/docs/reference/players/) describes every field.

## 3. Read the status

The operator turns the `Player` into a standing claim on the screen
and a pod that draws the idle screen, named `<player>-idle`:

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
* `NoDisplay` or `NotReported`: no `Display` carries the monitor the
  claim remembers. Go back to step 1 and confirm the machine
  publishes the screen.
* An empty column: the claim has not resolved. Read the claim's
  events for the selector that matched nothing, or matched devices
  on two machines:

        kubectl describe resourceclaim den-idle-devices

A selector that reads an attribute without `has()` fails on every
device that lacks it, which reads as a claim that matches nothing.

## 5. Prove it plays

Create a `Play` that names the unit, with one file the machine can
reach:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: den-check
      namespace: house
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
