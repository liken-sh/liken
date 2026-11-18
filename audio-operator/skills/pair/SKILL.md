---
name: pair
description: "Play a video on one monitor with its sound on that monitor's own speakers, in one claim and one pod. Use when a workload needs a screen and its sound together on one HDMI or DisplayPort monitor."
---

This skill is the guide at https://liken.sh/audio/docs/guides/pair/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Pair sound with its screen

At the end of this guide, one pod plays a video on a monitor's
screen and its sound on that monitor's speakers, through one claim.
You need this operator [installed](https://liken.sh/audio/docs/guides/install/) and
[`display-operator`](https://liken.sh/display/) installed on your
[`liken`](https://liken.sh/docs/) cluster, and a monitor with
speakers on HDMI or DisplayPort.

## How the pairing works

`display-operator` and this operator are separate
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
drivers, and each reads the same monitor on its own.
`display-operator` reads the monitor's EDID (Extended Display
Identification Data) through the graphics card. This operator reads
the ELD (EDID-Like Data), which the graphics driver copies into the
audio driver. Both operators compute the attribute
`monitor.liken.sh/id` from their data in the same way, so both
publish the same value for the same monitor. This value is the
pairing identity.

A single
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
contains one request for each driver, and a `matchAttribute`
constraint requires the same `monitor.liken.sh/id` in both. The
scheduler then allocates a screen and speakers of one monitor, on
whichever connector that monitor uses.

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
              deviceClassName: audio-sink
              tolerations:
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30
        constraints:
          - requests: [screen, speakers]
            matchAttribute: monitor.liken.sh/id

`matchAttribute` takes the full name of the attribute, with its
domain. A CEL selector is different: it writes the domain and the
attribute name separately, as
[Play sound to an output](https://liken.sh/audio/docs/guides/claim/) shows.

This claim accepts any monitor that has both a screen and speakers.
To choose one monitor, add a CEL selector to the `screen` request that
tests the `model` or `serial` attribute that `display-operator`
publishes. The constraint then allocates the speakers of a monitor
with the same pairing identity.

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
                - --ao=pipewire
                - https://media.example.com/movie.mkv
              resources:
                claims:
                  - name: monitor

One `resources.claims` entry names the claim, and the container
receives both allocations: the screen from `display-operator` and the
speakers from this operator. `mpv` needs no flag to find the screen,
because the compositor places each window on the screen of the
Wayland socket that the window arrived on. `--ao=pipewire` plays
through PipeWire, which reads `PIPEWIRE_REMOTE` and `PIPEWIRE_NODE`
itself.

## 3. What the container receives

Each driver adds its own mount and variables, and they do not
conflict:

| From | What | Value |
|---|---|---|
| display | mount | `/var/run/display.liken.sh` |
| display | `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| display | `WAYLAND_DISPLAY` | `wayland-<the claim's UID>`, a socket the compositor opened for this claim |
| display | `DISPLAY_APP_ID` | the allocated output's name; nothing reads it |
| audio | mount | `/var/run/audio.liken.sh`, read-only |
| audio | `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| audio | `PIPEWIRE_NODE` | the allocated output's node name |

`display-operator` sets `XDG_RUNTIME_DIR` to its own directory. That
does not affect the sound, because `PIPEWIRE_REMOTE` starts with a
slash, so PipeWire treats it as an absolute socket path and does not
look in the runtime directory.

## Two identical monitors

The pairing identity identifies a monitor's model, not one unit of
that model. The ELD has no serial number, so two monitors of the same
model have the same `monitor.liken.sh/id`.
[Devices](https://liken.sh/audio/docs/reference/devices/#the-pairing-identity) describes
how the value is computed. On a machine with two identical monitors,
the scheduler can allocate the screen of one monitor and the speakers
of the other, and no selector prevents it. A `serial` selector on the
`screen` request selects one monitor's screen, but this operator
publishes no serial, so no selector can select that monitor's
speakers.
