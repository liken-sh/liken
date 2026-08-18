---
title: Play sound to an output
weight: 20
---

# Play sound to an output

This guide plays one workload's sound through one physical output,
from a `Deployment`: an internet radio player on the kitchen
monitor's speakers. It works the same for the analog jack. You need
the operator [installed](/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

The flow is
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
end to end. A
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
names the output. The scheduler allocates one matching device and
places the pod on that device's machine. The container receives the
PipeWire socket and the name of the sink its streams must reach.

## 1. Pick the output

List what a node offers:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml

Each device is one playback PCM device of the card, with the
attached monitor's facts as attributes. Write a CEL selector against
them. If the Dynamic Resource Allocation (DRA) objects are new to
you, read [How the pieces fit](/docs/guides/#how-the-pieces-fit)
first. Three useful forms:

    # by output
    device.attributes["audio.liken.sh"].output == "card0-pcm3"

    # the analog jack
    has(device.attributes["audio.liken.sh"].connectionType) &&
    device.attributes["audio.liken.sh"].connectionType == "analog"

    # by monitor, so the claim survives a re-cabling
    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

Guard `connectionType` and `monitor.liken.sh/id` with `has()`, as
above. On an HDMI or DisplayPort output both come from the monitor,
so an output with no monitor publishes neither, and a selector that
reads a missing attribute fails the whole allocation. `output` needs
no guard, because every device publishes it.
[Devices](/docs/reference/devices/) lists every attribute, and
explains why the pairing attribute reads under its own domain,
`monitor.liken.sh`.

## 2. Write the claim

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-speakers
      namespace: media
    spec:
      devices:
        requests:
          - name: output
            exactly:
              deviceClassName: audio-output
              selectors:
                - cel:
                    expression: |
                      device.attributes["audio.liken.sh"].output == "card0-pcm3"
              tolerations:
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

Tolerate `audio.liken.sh/disconnected` and nothing else. Its effect
is `NoExecute`, and `tolerationSeconds` says how long your pod may
hold a silent output before it is evicted. Thirty seconds means a
reseated cable costs nothing, and it also carries the pod across a
restart of the operator itself. Leave `audio.liken.sh/no-monitor`
and `audio.liken.sh/no-sink` untolerated: they hold a new pod
`Pending` until the output can really play, and the pod starts on
its own when it can.

## 3. Reference the claim from a `Deployment`

    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: kitchen-radio
      namespace: media
    spec:
      replicas: 1
      strategy:
        type: Recreate
      selector:
        matchLabels:
          app: kitchen-radio
      template:
        metadata:
          labels:
            app: kitchen-radio
        spec:
          resourceClaims:
            - name: output
              resourceClaimName: kitchen-speakers
          containers:
            - name: player
              image: <your mpv image>
              args:
                - --no-video
                - --ao=pipewire
                - https://radio.example.com/stream
              resources:
                claims:
                  - name: output

One line carries the whole arrangement: `resources.claims` gives the
container the claim. That is what places the pod and delivers the
socket. No flag hands over the sink, because PipeWire's client
library reads the two delivered environment variables itself:
`PIPEWIRE_REMOTE` names the socket, and `PIPEWIRE_NODE` sets
`target.object` on every stream the client creates.

The image is yours, with two requirements:

* The player must use PipeWire's stream API, as mpv's `--ao=pipewire`
  does. A client on the PulseAudio protocol or the ALSA
  compatibility plugin selects its sink another way, which the
  delivered variables do not set.
* The image must carry PipeWire's client configuration. `libpipewire`
  does not open a client context without
  `/usr/share/pipewire/client.conf`. Debian ships that file in
  `pipewire-bin`, the daemon package, not in the library package
  `libpipewire-0.3-0`. An image that installs the library alone
  fails before it reaches the socket, with `can't load config
  client.conf: No such file or directory`.

`strategy: Recreate` matters. Pods that share one `ResourceClaim`
share its output, and PipeWire mixes streams: during a rolling
update, the old and the new pod would both play through the one
sink. `Recreate` ends the old pod first.

## 4. What the container receives

A mount and two environment variables. No device node: the
container does not open a PCM device, it connects to PipeWire, which
holds every PCM device on the card.

| What | Value |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that holds PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated output's sink name, such as `liken.audio.card0-pcm3` |

The mount is read-only because connecting to a Unix socket needs
write permission on the socket itself, not on the directory that
carries it.

**One container holds at most one output.** `PIPEWIRE_REMOTE` and
`PIPEWIRE_NODE` each hold one value, so two allocations delivered to
one container overwrite, and the last wins. A pod that plays into
two outputs runs two containers, each naming its own request in the
claim.

## Unplugs, restarts, and a second claim

**A monitor unplugged.** The device keeps its place in the slice and
gains the `disconnected` taint. After your `tolerationSeconds`, the
eviction controller ends the pod. A cable reseated within the
toleration costs nothing. A claim that selects by
`monitor.liken.sh/id` instead of by `output` follows the monitor to
whichever output its cable lands on next.

**A PipeWire restart ends every client's audio.** The socket belongs
to the PipeWire container, so its restart takes the socket away. A
client that reconnects finds the new socket at the same path; a
client that does not has to restart. The operator's own restart
takes nothing away, because the daemons run in their own containers
and keep playing through it.

**A second claim on the same output parks.** Every device this
operator publishes is exclusive, so the second pod waits `Pending`
until the first releases the output.
[A sink can be shared and this one is not](https://github.com/liken-sh/audio-operator/blob/main/plans/open-problems/a-sink-can-be-shared-and-this-one-is-not.md)
records that decision.

To put sound and picture on one monitor, continue with
[Pair sound with its screen](/docs/guides/pair/).
