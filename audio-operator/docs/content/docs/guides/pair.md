---
title: Pair sound with its screen
weight: 30
---

# Pair sound with its screen

This guide plays a video on one monitor with its sound on that same
monitor's speakers: one claim, one pod, one screen. You need this
operator and the [display operator](https://display.liken.sh)
[installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster, and a monitor with
speakers on HDMI or DisplayPort.

## How the pairing works

The two operators are separate
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
drivers, and each publishes its own reading of the same monitor: the
display operator reads the EDID (Extended Display Identification
Data) through the graphics card, and this operator reads the ELD
(EDID-Like Data), the block the graphics driver writes into the
audio driver. Both derive one attribute the same way, byte for byte:
`monitor.liken.sh/id`, the pairing identity.

A single
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
holds one request against each driver, and a `matchAttribute`
constraint requires one value of `monitor.liken.sh/id` across the
two. The scheduler then allocates a screen and speakers that belong
to one monitor, whichever connector it is on.

## 1. Write the claim

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-screen
      namespace: media
    spec:
      devices:
        requests:
          - name: screen
            exactly:
              deviceClassName: display-output
              tolerations:
                - key: display.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
          - name: speakers
            exactly:
              deviceClassName: audio-output
              tolerations:
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
        constraints:
          - requests: [screen, speakers]
            matchAttribute: monitor.liken.sh/id

`matchAttribute` takes the attribute's full name. Only a CEL
selector splits the name on its domain, as
[Play sound to an output](/docs/guides/claim/) shows.

As written, the claim takes any monitor that offers both a screen
and speakers. To name one monitor, add a CEL selector to the
`screen` request, on the display operator's `model` or `serial`
attributes. The constraint brings the matching speakers with it.

## 2. Reference the claim from a `Deployment`

    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: kitchen-player
      namespace: media
    spec:
      replicas: 1
      strategy:
        type: Recreate
      selector:
        matchLabels:
          app: kitchen-player
      template:
        metadata:
          labels:
            app: kitchen-player
        spec:
          resourceClaims:
            - name: monitor
              resourceClaimName: kitchen-screen
          containers:
            - name: player
              image: <your mpv image>
              args:
                - --fullscreen
                - --wayland-app-id=$(DISPLAY_APP_ID)
                - --ao=pipewire
                - https://media.example.com/movie.mkv
              resources:
                claims:
                  - name: monitor

`resources.claims` names the claim once, so the container receives
both allocations: the display operator's delivery for the screen,
and this operator's for the speakers. Two of mpv's flags route the
two halves: `--wayland-app-id=$(DISPLAY_APP_ID)` puts the window on
the allocated screen, and `--ao=pipewire` plays through PipeWire,
which reads the delivered `PIPEWIRE_REMOTE` and `PIPEWIRE_NODE`
itself.

## 3. What the container receives

Each driver applies its own edits, and they do not collide:

| From | What | Value |
|---|---|---|
| display | mount | `/var/run/display.liken.sh` |
| display | `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| display | `WAYLAND_DISPLAY` | `wayland-0` |
| display | `DISPLAY_APP_ID` | the allocated output's app-id |
| audio | mount | `/var/run/audio.liken.sh`, read-only |
| audio | `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| audio | `PIPEWIRE_NODE` | the allocated output's sink name |

The display operator points `XDG_RUNTIME_DIR` at its own directory,
and that does not misroute the audio: a `PIPEWIRE_REMOTE` that
starts with a slash is used as an absolute socket path, and the
runtime directory is not consulted.

## The grain of the pairing

The pairing identity names a monitor's model, not a unit. The ELD
carries no serial number, so two monitors of one model publish one
value, and the constraint is satisfied by either pairing.
[Devices](/docs/reference/devices/#the-pairing-identity) gives the
derivation. On a machine with two identical monitors, the screen and
the speakers the scheduler picks can come from different units, and
no selector can prevent that: a `serial` selector on the `screen`
request pins the screen to one unit, but this operator publishes no
serial for the constraint to hold the speakers to. The ELD offers
nothing finer.
