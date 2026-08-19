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
kernel spells it.

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
once, with no restart. Releasing the claim restarts nothing either:
the screen keeps the mode until the next compositor start, and the
slice's `currentMode` says what it runs.

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
