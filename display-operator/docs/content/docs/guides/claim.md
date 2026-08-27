---
title: Put a window on a screen
weight: 20
---

# Put a window on a screen

This guide runs one fullscreen program on one monitor, from a
`Deployment`: a kiosk. It works the same for a dashboard or a video
player. You need the operator
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

The claim names the screen. The scheduler places the pod, and the
container receives the compositor's Wayland socket and the app-id
that puts its window on that screen.

## 1. Pick the screen

If the
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
objects are new to you, read
[How the pieces fit](/docs/guides/#how-the-pieces-fit) first.

List what a node offers:

    kubectl get resourceslice <node>-display.liken.sh -o yaml

Each device is one connector, with the attached monitor's facts as
attributes. Write a selector against them in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/).
Three useful forms:

    # by connector
    device.attributes["display.liken.sh"].connector == "HDMI-A-1"

    # by monitor, so the claim survives a re-cabling
    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

    # any screen at least 1920 pixels wide
    has(device.attributes["display.liken.sh"].widthPixels) &&
    device.attributes["display.liken.sh"].widthPixels >= 1920

Guard `model` and `widthPixels` with `has()`, as above. They come
from the monitor and are absent on an empty connector, and a
selector that reads a missing attribute fails the whole allocation.
`connector` needs no guard, because every device publishes it.
[Devices](/docs/reference/devices/) lists every attribute.

## 2. Write the claim

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-screen
      namespace: house
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

Tolerate `display.liken.sh/disconnected`, the taint a dark connector
has. Its effect is `NoExecute`, and `tolerationSeconds` says how
long your pod may hold a dark screen before the eviction controller
ends it. Thirty seconds means a reseated cable costs nothing, and it
also keeps the pod through a restart of the compositor's container,
which is a restart of every screen on that machine. A claim on a
connector with no monitor parks the pod `Pending`, visibly, and the
pod starts on its own when a monitor is plugged in.

## 3. Reference the claim from a `Deployment`

    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: kitchen-kiosk
      namespace: house
    spec:
      replicas: 1
      strategy:
        type: Recreate
      selector:
        matchLabels:
          app: kitchen-kiosk
      template:
        metadata:
          labels:
            app: kitchen-kiosk
        spec:
          resourceClaims:
            - name: screen
              resourceClaimName: kitchen-screen
          containers:
            - name: browser
              image: <your chromium image>
              args:
                - --class=$(DISPLAY_APP_ID)
                - --kiosk
                - https://grafana.example.com/
              resources:
                claims:
                  - name: screen

Two lines make this work:

* `resources.claims` gives the container the claim. That is what
  places the pod and delivers the socket.
* `--class=$(DISPLAY_APP_ID)` hands the allocated output's app-id to
  the program. The compositor routes a window to a screen by its
  app-id, so the program must present the one the claim delivered.
  Each toolkit has its own flag: `chromium` takes
  `--class=$(DISPLAY_APP_ID)`, and `mpv` takes
  `--wayland-app-id=$(DISPLAY_APP_ID)`.

The image is yours. Any Wayland client works; the operator delivers
only the socket and the app-id.

`strategy: Recreate` matters. Pods that share one `ResourceClaim`
share its output, and the compositor refuses nothing. During a
rolling update, the old and the new pod would both present the same
app-id and cover each other on the one screen. `Recreate` ends the
old pod first.

## 4. What the container receives

A mount and three environment variables. No device node: a Wayland
client draws through the compositor, which holds the card.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, the compositor's runtime directory |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-0` |
| `DISPLAY_APP_ID` | the allocated output's app-id, such as `hdmi-a-1` |

The claim assigns the screen; the app-id only routes. What keeps two
workloads off one screen is the allocation: the second pod cannot
claim an output the first holds, so it parks until the first
releases it.

## Ask for a mode

A claim can state the resolution its screen runs. The operator
writes it into the compositor's config, restarts the compositor,
and delivers the screen only after the card reports the mode. The
name is one of the values in the `modes` attribute, spelled as the
kernel spells it, and it can carry a refresh: `3840x1600@24` runs
a 24 fps film without the 3:2 cadence a 60 Hz mode forces on it.
The refresh is a whole number of hertz.

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-screen
      namespace: house
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

Do not state a mode casually. One compositor drives every output
of the card, and it reads its config once at startup, so a mode on
one connector restarts it and ends every Wayland client on every
screen of that machine. The lab measured about 1.3 seconds of
dark, plus whatever each client takes to come back.

Run every display consumer under a controller. A bare `Pod` whose
compositor restarted dies `Completed` and stays dead. A
`Deployment` brings it back, and the `tolerationSeconds` above is
what keeps the pod scheduled through the restart.

A claim that asks for the mode the screen already runs delivers at
once, with no restart, and a claim that states no refresh matches
whatever rate the screen runs under that name. Releasing the claim
restarts nothing either: the screen keeps the mode until the next
compositor start, and the slice's `currentMode` says what it runs,
refresh included.

## Set the panel's brightness and power

A claim can state the panel's own brightness and power the way it
states a mode, with two more parameters in the same opaque block.
The parameters follow the claim's lifetime. For a setting the panel
should hold with no claim attached, declare it on the panel's
[`Display`](/docs/reference/displays/) instead.

    config:
      - opaque:
          driver: display.liken.sh
          parameters:
            brightness: 87
            power: onWhileClaimed

`brightness` is a percentage from 0 to 100 of the panel's own
maximum. `power: on` powers the panel on at prepare. `power:
onWhileClaimed` also powers it back down when the claim ends, so a
movie pod that ends leaves a dark screen. Use `on` for a workload a
`Deployment` replaces on rollouts, because each replacement pod is a
new claim, and `onWhileClaimed` would blink the screen on every
rollout.

Not every panel takes these. The operator asks each panel what it
carries and publishes the answers as the `controlsBrightness` and
`controlsPower` attributes, so add the matching attribute to your
selector:

    selectors:
      - cel:
          expression: |
            device.attributes["display.liken.sh"].connector == "HDMI-A-1" &&
            has(device.attributes["display.liken.sh"].controlsBrightness)

Without the selector, the scheduler can place the claim on a panel
that refuses the protocol, and the prepare fails with the missing
capability named. Some panels also ship with DDC/CI switched off in
their on-screen menu; turning it on there is what makes the
attributes appear.

Neither parameter restarts the compositor. A claim that states only
these delivers without the dark second a mode costs.

## Hold the panel's control channel

The parameters above are set once, at prepare. A pod that speaks the
panel's protocol itself while it runs claims the connector's control
device instead, and receives the raw i2c node. Most pods never need
the wire: setting or temporarily overriding the panel goes through
the [`Display`](/docs/reference/displays/), and the operator writes
the bus. One claim can take a screen and its control channel
together, with a `matchAttribute` constraint tying the two requests
to one monitor:

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

The `display-control` class is yours to create, like
`display-output`;
[Devices](/docs/reference/devices/#the-control-device) gives its
YAML. The container that names the `control` request receives
`/dev/i2c-N` and `DISPLAY_CONTROL_BUS` holding that path. An init
container that sets the brightness to 87 before the player starts,
using the `ddcutil` the operator image carries:

    initContainers:
      - name: brightness
        image: ghcr.io/liken-sh/display-operator:latest
        command: ["ddcutil"]
        args: ["setvcp", "10", "87"]
        resources:
          claims:
            - name: control

`ddcutil` finds the bus itself from the one `/dev/i2c-*` node the
claim delivered, so the command needs no bus number. A config block
that states `mode`, `brightness`, or `power` must name the `screen`
request when the claim also holds a control request, because those
parameters act on outputs and a control request takes none.

Do not write to any i2c address other than `0x37`. The
[reference](/docs/reference/devices/#the-control-device) explains
what lives at `0x50` and why a write there follows the monitor to
every machine it ever plugs into.

## Unplugged monitors, moved monitors, and second screens

**A monitor unplugged.** The device keeps its place in the slice and
gains the `disconnected` taint. After your `tolerationSeconds`, the
eviction controller ends the pod. A cable reseated within the
toleration costs nothing: the client's Wayland connection never
breaks, and its picture returns with the output.

**A monitor moved to another connector.** A claim that selects by
`model` or by `serial` instead of by `connector` follows the
monitor. The eviction controller ends the old pod on the dark
connector, and its replacement allocates the output the monitor is
on now.

**Two screens from one pod.** One container drives one screen,
because a claim delivers one `DISPLAY_APP_ID` per container. A pod
that drives two screens runs two containers, each naming its own
request in the claim.

**A screen and its speakers.** A monitor's HDMI speakers belong to
the [audio operator](https://audio.liken.sh). Both operators publish
`monitor.liken.sh/id`, the same identity read from the same monitor.
So one claim can request a screen from this driver and the matching
audio output from that one. A `matchAttribute` constraint on
`monitor.liken.sh/id` holds the two requests together.
