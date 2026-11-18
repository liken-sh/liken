---
title: Put a window on a screen
weight: 20
description: "Run one fullscreen program on one monitor from a Deployment through a ResourceClaim. Use when a pod needs a screen, a specific mode or refresh rate, the panel's brightness or power, or when a monitor moves or unplugs."
---

# Put a window on a screen

This guide runs a fullscreen browser kiosk on one monitor from a
`Deployment`. The same steps work for a dashboard or a video player.
You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

The claim selects the screen. The scheduler places the pod on the node
that has it, and the container receives a Wayland socket that the
compositor opened for that claim. A window that a program opens on that
socket appears on that screen.

## 1. Pick the screen

If the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
objects are new to you, read
[How a claim reaches your pod](/docs/concepts/how-the-pieces-fit/) first.

List what a node offers:

    kubectl get resourceslice <node>-display.liken.sh -o yaml

Each device is one connector, and its attributes describe the
monitor on it. Write a selector against them in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/).
For example:

    # by connector
    device.attributes["display.liken.sh"].connector == "HDMI-A-1"

    # by monitor, so the claim survives a re-cabling
    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

    # any screen at least 1920 pixels wide
    has(device.attributes["display.liken.sh"].widthPixels) &&
    device.attributes["display.liken.sh"].widthPixels >= 1920

Guard `model` and `widthPixels` with `has()`, as above. A connector
with no monitor does not have them, and a selector that reads a
missing attribute makes the whole allocation fail. Every device has
`connector`, so it needs no guard.
[Devices](/docs/reference/devices/) lists every attribute.

## 2. Write the claim

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: menu-screen
      namespace: signage
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              selectors:
                - cel:
                    expression: |
                      device.attributes["display.liken.sh"].connector == "HDMI-A-1"
              tolerations:
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

The operator puts the `display.liken.sh/disconnected` taint on a
connector that never had a monitor, on a connector that was just
plugged into a different monitor, and on every screen of the machine
while its compositor restarts. The toleration keeps your pod running
for `tolerationSeconds` after the taint appears, and then the taint
eviction controller deletes the pod. So with 30 seconds, a short
compositor restart does not delete your pod. The operator does not
taint a connector whose monitor only went dark, such as a monitor that
shows another input.

Without the toleration, a claim on a connector with no monitor does
not allocate. The pod stays `Pending` until a monitor is plugged in.

## 3. Reference the claim from a `Deployment`

    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: menu-kiosk
      namespace: signage
    spec:
      replicas: 1
      strategy:
        type: Recreate
      selector:
        matchLabels:
          app: menu-kiosk
      template:
        metadata:
          labels:
            app: menu-kiosk
        spec:
          resourceClaims:
            - name: screen
              resourceClaimName: menu-screen
          containers:
            - name: browser
              image: <your chromium image>
              args:
                - --kiosk
                - https://grafana.example.com/
              resources:
                claims:
                  - name: screen

The `resources.claims` entry gives the container the claim, so the
scheduler places the pod on the claim's node and the operator gives the
container the Wayland socket. The compositor puts every window that
connects on that socket on the claimed screen, so the program needs no
flag or app-id. Use any image with a Wayland client.

Set `strategy: Recreate`. Pods that share one `ResourceClaim` share
its screen, so in a rolling update the old pod keeps drawing under the
new pod's window until the old pod ends. `Recreate` ends the old pod
first.

## 4. What the container receives

The container gets one mount and three environment variables. It
gets no device node, because a Wayland client draws through the
compositor, and the compositor opens the graphics card.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, the compositor's runtime directory |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-<the claim's UID>`, a socket the compositor opened for this claim |
| `DISPLAY_APP_ID` | the name of the allocated output, such as `hdmi-a-1`. The compositor does not read it. It is set for images that pass it to their toolkit |

The [`Display`](/docs/reference/displays/) lists each window under
its claim's name in `status.surfaces`. Only one claim can hold an
output. A second claim for the same output cannot allocate, so its pod
stays `Pending` until the first claim is released. Use a
[`Layout`](/docs/guides/layout/) when two workloads must share one
screen.

A client that sends YUV video frames can set each frame's color
encoding and range through `wp_color_representation_manager_v1`. The
compositor converts a frame with no values set as BT.709 at limited
range.

## Ask for a mode

A claim can set the resolution of its screen with the `mode`
parameter. The value is one of the entries of the device's `modes`
attribute, spelled as the kernel spells it, and it can end with a
refresh rate in whole hertz. For example, `3840x1600@24` plays a 24 fps
film without the uneven frame timing of a 60 Hz mode. The operator
writes the mode into the compositor's configuration, restarts the
compositor, and starts the container only after the graphics card
reports that the screen runs the mode.

An output whose mode is 3840 pixels wide or wider has an output scale
of 2, and a narrower one has a scale of 1, whether a claim set the
mode or the monitor's preferred mode is used. A client that supports
the scale lays out a 4K panel at 1080p size and renders at the panel's
full resolution. The compositor scales up a client that does not, so
that its text stays readable.

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: menu-screen
      namespace: signage
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              selectors:
                - cel:
                    expression: |
                      device.attributes["display.liken.sh"].connector == "HDMI-A-1"
              tolerations:
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                mode: "1280x720"

Set a mode only when you need it. One compositor drives every output
of the card, and it reads its configuration only when it starts. So a
mode on one connector restarts the compositor, which disconnects every
Wayland client on every screen of that machine. The screens are dark
until the compositor is back and each client has reconnected.

A restart that the operator orders starts weston again at once. If
weston crashes instead, the kubelet's crash backoff delays the next
start by up to 5 minutes, and the screens stay dark for that time. The
operator waits up to 6 minutes for the compositor to accept a
connection, and then 10 seconds for the screen to run the mode. If the
screen runs a different mode after that, the prepare fails. The
kubelet retries the prepare, and each retry fails without another
restart.

Run every program that uses a display under a controller. A bare `Pod` ends
as `Completed` when the compositor restarts, and does not start
again. A `Deployment` creates the pod again, and the toleration in the
claim above keeps the pod running during the restart.

A claim for the mode that the screen already runs is prepared at once,
with no restart. A mode with no refresh rate matches the screen at
whatever rate it runs that resolution. Releasing the claim does not
restart anything either. The screen keeps the mode until the
compositor next starts. The `currentMode` attribute in the
`ResourceSlice` shows the mode and refresh rate that the output runs.

## Set the panel's brightness and power

A claim can set the panel's brightness and power with two more
parameters in the same opaque block. They apply while the claim
exists. To keep a setting on the panel when no claim is attached, set
it on the panel's
[`Display`](/docs/reference/displays/) instead.

    config:
      - opaque:
          driver: display.liken.sh
          parameters:
            brightness: 87
            power: OnWhileClaimed

`brightness` is a percentage from 0 to 100 of the panel's own maximum
brightness. `power: On` powers the panel on when the claim is
prepared. `power: OnWhileClaimed` also puts the panel in standby 30
seconds after the claim ends, so a screen goes dark when a movie pod
ends. A new claim that is prepared on the same connector within those
30 seconds cancels the standby, so a `Deployment` rollout does not make
the screen blink. Use `On` for a workload whose screen must stay on
when its pod is gone for longer than that. The parameter also accepts
the lowercase forms `on` and `onWhileClaimed`, with the same meaning.

The operator reads each control before it writes it, and writes
nothing when the panel already has the value, so the screen does not
flicker.

Not every panel supports these controls. The operator asks each panel
over DDC/CI which controls it has and publishes the results as the
`controlsBrightness` and `controlsPower` attributes, so add the
matching attribute to your selector:

    selectors:
      - cel:
          expression: |
            device.attributes["display.liken.sh"].connector == "HDMI-A-1" &&
            has(device.attributes["display.liken.sh"].controlsBrightness)

Without that condition, the scheduler can allocate the claim on a
panel that does not support the control, and the prepare fails with an
error that names the missing control. Some panels ship with DDC/CI
turned off in their on-screen menu. Turn it on in the menu, and the
attributes appear.

Neither parameter restarts the compositor, so the screen does not go
dark.

## Hold the panel's control channel

The operator applies the parameters above once, when it prepares the
claim. A pod that must send DDC/CI commands to the panel while it runs
claims the connector's control device instead, and the container
receives the raw i2c device node. Most pods do not need this: to set
the panel or override a setting for a while, edit the
[`Display`](/docs/reference/displays/), and the operator writes to the
bus. One claim can request a screen and its control device together,
with a `matchAttribute` constraint that ties both requests to the same
monitor:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: movie-screen
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              selectors:
                - cel:
                    expression: |
                      has(device.attributes["monitor.liken.sh"].id) &&
                      device.attributes["monitor.liken.sh"].id == "boe-1080-display"
          - name: control
            exactly:
              deviceClassName: display-control
        constraints:
          - requests: ["screen", "control"]
            matchAttribute: monitor.liken.sh/id

You create the `display-control` class yourself, like
`display-output`. [Devices](/docs/reference/devices/#the-control-device)
has its YAML. A container that lists the `control` request in its
`resources.claims` receives `/dev/i2c-N`, and `DISPLAY_CONTROL_BUS`
holds that path. This init container sets the brightness to 87 before
the player starts, using the `ddcutil` in the operator image:

    initContainers:
      - name: brightness
        image: ghcr.io/liken-sh/display-operator:latest
        command: ["ddcutil"]
        args: ["setvcp", "10", "87"]
        resources:
          claims:
            - name: control

The container has only one `/dev/i2c-*` node, so `ddcutil` finds the
bus without a bus number. When the claim
also has a control request, a `config` block that sets `mode`,
`brightness`, or `power` must list the `screen` request in `requests`.
Those parameters apply to outputs, and a control request is not an
output.

Do not write to any i2c address other than `0x37`. The
[reference](/docs/reference/devices/#the-control-device) explains what
is at `0x50` and why a write there stays on the monitor when you plug
it into another machine.

## Unplugged monitors, moved monitors, and second screens

**A monitor goes dark.** The device stays in the `ResourceSlice` with
the monitor's attributes, so your claim still allocates and your pod
keeps running. The client's Wayland connection stays open, and its
picture comes back when the output does.

A monitor goes dark when a cable is reseated, when an A/V receiver
renegotiates the link after an input change, and when the panel shows
another input. A monitor that shows another input stops asserting hot
plug detect, and the kernel reports that the same way as an unplugged
cable. The operator cannot tell them apart, so it treats an unplugged
monitor the same way: its devices stay allocatable, and your pod keeps
drawing to a screen that is not there. The `Connected` condition on
the `Display` shows what the connector reports.

The operator reports a monitor as gone only after its connector has
read no monitor for 5 seconds. An A/V receiver in standby can drop hot
plug detect for a moment, again and again. A drop shorter than 5
seconds changes nothing in the `ResourceSlice` or on the `Display`,
although the compositor turns the output off for the length of the
drop. While another connector on the same card keeps dropping, the
operator can take up to 20 seconds to report that a monitor left.

**A monitor moved to another connector.** The connector that the
monitor left keeps publishing it, the same as for a monitor that went
dark, and the operator does not taint it. So the pod on the old
connector keeps running. A claim that selects by `model` or `serial`
instead of `connector` matches the monitor on its new connector too.

**Two screens from one pod.** A container has one `WAYLAND_DISPLAY`,
so it draws on one screen. A pod that draws on two screens runs two
containers, and each container lists its own request in
`resources.claims`.

**A screen and its speakers.** The
[audio operator](https://liken.sh/audio/) publishes the monitor's HDMI
speakers. Both operators publish `monitor.liken.sh/id` with the same
value for the same monitor, so one claim can request a screen and the
monitor's speakers with a `matchAttribute` constraint on
`monitor.liken.sh/id`.
