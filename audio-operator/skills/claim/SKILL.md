---
name: claim
description: "Play one workload's sound through one physical output with a ResourceClaim and a Deployment, or share one output among several claims. Use when a pod needs a speaker, the analog jack, a Bluetooth speaker with a chosen codec, or a microphone to record from, or when two workloads must play through one speaker at once."
---

This skill is the guide at https://liken.sh/audio/docs/guides/claim/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Play sound to an output

At the end of this guide, a `Deployment` plays its sound through one
physical output. The example plays an internet radio stream on a
kitchen monitor's speakers, and the steps are the same for the analog
jack. You need the operator [installed](https://liken.sh/audio/docs/guides/install/) on your
[`liken`](https://liken.sh/docs/) cluster.

The workload gets its output through
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/).
A
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
selects the output. The scheduler allocates one matching device and
places the pod on the machine that has it. The operator then mounts
PipeWire's socket into the container and sets an environment variable
that names the sink to play to.

## 1. Pick the output

List what a node offers:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml

Each device is one PCM device of a sound card. Its attributes
describe the output and, on HDMI or DisplayPort, the monitor that is
connected to it. A playback device has the `sink` attribute. You
select a device with a CEL selector on these attributes. If DRA
objects are new to you, read
[How a claim reaches your pod](https://liken.sh/audio/docs/concepts/how-the-pieces-fit/)
first. These four selectors cover most claims:

    # by name, as `kubectl get sinks` lists it
    device.attributes["audio.liken.sh"].sink == "kitchen-pci-0000-00-1f-3-hdmi-0"

    # the analog jack
    has(device.attributes["audio.liken.sh"].connectionType) &&
    device.attributes["audio.liken.sh"].connectionType == "analog"

    # by monitor, so the claim still matches after you move the cable
    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

    # one Bluetooth speaker, by the address on its label
    has(device.attributes["audio.liken.sh"].address) &&
    device.attributes["audio.liken.sh"].address == "A0:AB:51:33:B7:12"

When [`bluetooth-operator`](https://liken.sh/bluetooth/) publishes
a machine's media bus, this operator's pod claims it, and each
Bluetooth speaker paired with that machine is also a device. The
device publishes the speaker's address, the name that BlueZ reports
for it, its connection state, and its codecs. A claim selects a
speaker the same way it selects an output.

Guard `connectionType`, `monitor.liken.sh/id`, and `address` with
`has()`, as above. On an HDMI or DisplayPort output, `connectionType`
and the monitor id come from the monitor, so an output with no
monitor publishes neither. Only a Bluetooth speaker publishes
`address`. A selector that reads an attribute that a device does not
publish makes the whole allocation fail, so a selector that tests
`address` without `has()` fails on every output of the sound card. `sink` needs no
guard in the `audio-sink` class, because every device of that class
publishes it. [Devices](https://liken.sh/audio/docs/reference/devices/) lists every
attribute, and explains why the monitor id is under its own domain,
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
              deviceClassName: audio-sink
              selectors:
                - cel:
                    expression: |
                      device.attributes["audio.liken.sh"].sink == "kitchen-pci-0000-00-1f-3-hdmi-0"
              tolerations:
                - key: audio.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

Tolerate `audio.liken.sh/disconnected` and no other taint. The
operator sets that taint, with the effect `NoExecute`, when the output
can no longer play, and `tolerationSeconds` sets how long the pod
keeps running before the eviction controller deletes it. With 30
seconds, the pod keeps running while you reseat a cable or while the
operator restarts. Do not tolerate `audio.liken.sh/no-monitor` or
`audio.liken.sh/no-sink`. While either taint is on the output, a new
pod stays `Pending`, and it starts when the output can play.

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

The `resources.claims` entry gives the container the claim. Because
of it, the scheduler places the pod on the machine with the output,
and the operator mounts the socket and sets the environment variables
in that container. The player needs no flag to name the sink, because
PipeWire's client library reads the variables itself:
`PIPEWIRE_REMOTE` is the socket path, and `PIPEWIRE_NODE` becomes
`target.object` on every stream the client creates.

You supply the image, and it must meet two requirements:

* The player must use PipeWire's stream API, as `mpv` does with
  `--ao=pipewire`. A client that uses the PulseAudio protocol or the
  ALSA compatibility plugin chooses its sink in another way, and
  `PIPEWIRE_NODE` does not affect it.
* The image must contain PipeWire's client configuration.
  `libpipewire` cannot create a client context without
  `/usr/share/pipewire/client.conf`. Debian puts that file in
  `pipewire-bin`, the daemon package, and not in `libpipewire-0.3-0`,
  the library package. An image that installs only the library fails
  before it connects to the socket, with `can't load config
  client.conf: No such file or directory`.

Set `strategy: Recreate`. Every pod that uses one `ResourceClaim`
plays through the same output, and PipeWire mixes their streams, so
during a rolling update you would hear the old pod and the new pod
together. `Recreate` deletes the old pod before it starts the new
one.

## 4. What the container receives

The container receives a mount and two environment variables, and
no device node. It never opens a PCM device itself: it connects to
PipeWire, and PipeWire opens the card's PCM devices.

| What | Value |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that contains PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated output's node name, such as `liken.audio.card0-pcm3` |

The mount can be read-only because connecting to a Unix socket needs
write permission on the socket file, not on the directory that
contains it.

**One container can use only one output.** `PIPEWIRE_REMOTE` and
`PIPEWIRE_NODE` each have one value, so when two allocations reach
one container, the second overwrites the first. A pod that plays to
two outputs needs two containers, and each container names its own
request in `resources.claims`.

## Choose the codec on a Bluetooth speaker

A speaker's `codecs` attribute lists the A2DP codecs that it offers,
and a claim can choose one of them. The operator switches the speaker
to that codec before your pod starts. The choice matters when the
radio band is busy: aptX keeps its bitrate and the sound drops out,
while SBC lowers its bitrate and keeps playing at a lower quality.

    spec:
      devices:
        config:
          - opaque:
              driver: audio.liken.sh
              parameters:
                codec: sbc
        requests:
          - name: speaker
            exactly:
              deviceClassName: audio-sink
              selectors:
                - cel:
                    expression: |
                      has(device.attributes["audio.liken.sh"].address) &&
                      device.attributes["audio.liken.sh"].address == "A0:AB:51:33:B7:12"

Read the speaker's list of codecs before you choose one:

    kubectl get resourceslice <node>-audio.liken.sh \
      -o jsonpath='{.spec.devices[?(@.name=="a0-ab-51-33-b7-12")].attributes.codecs}'

If the speaker does not offer the codec, the pod stays in
`ContainerCreating`, and the pod's events list the codecs that the
speaker offers. The same happens when a claim chooses a codec for one
of the sound card's own outputs, because only a Bluetooth speaker has
a codec to choose.

A `config` entry with no `requests` list applies to every request in
the claim, and a `requests` list limits it to the requests that it
names. `codec` is the only parameter that the operator reads. Any
other key fails the prepare of the claim, so a misspelled key stops
the pod instead of playing the wrong codec.

A `DeviceClass` can contain the same opaque entry, which sets the
codec for every claim that allocates through that class. A codec in
a claim overrides the codec of the class. Put the entry in the claim
when one workload needs a codec, and in the class when every
workload of that class does.

The pod does not start until the switch finishes. Each time a claim
on a speaker is prepared and no other claim has the speaker, the
operator sets the speaker's sink to unity volume, so set the loudness
with your player's own stream volume. You can also declare the speaker's default level
on its `Sink`, as
[Set endpoint volume and controls](https://liken.sh/audio/docs/guides/rest/) shows. A codec
declared there is the default, and the `codec` parameter of a claim
overrides it.

## Record from a source

You claim a capture device the same way, with the `audio-source`
class. The container receives the same socket, and `PIPEWIRE_NODE` is
the name of the capture node:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-microphone
      namespace: media
    spec:
      devices:
        requests:
          - name: microphone
            exactly:
              deviceClassName: audio-source
              selectors:
                - cel:
                    expression: |
                      device.attributes["audio.liken.sh"].source == "kitchen-usb-0573-1573-a34004801402-usb-audio-capture"

A recorder that uses PipeWire's stream API, such as `pw-record`,
reads the two variables and captures from that node without a flag.
`spec.mute` on the `Source` closes the microphone for every client,
whether or not a claim holds it.

## Unplugged monitors, restarts, and a second claim

**You unplug a monitor.** The device stays in the `ResourceSlice` and
gets the `audio.liken.sh/disconnected` taint. When `tolerationSeconds` has passed,
the eviction controller deletes the pod. If you reseat the cable
within that time, nothing happens to the pod. A claim that selects by
`monitor.liken.sh/id` instead of by `sink` finds the monitor again on
whichever output the cable uses next.

**PipeWire restarts.** The PipeWire container creates the socket, so
a restart of that container removes the socket and stops the sound
of every client. A client that reconnects finds the new socket at the
same path. A client that does not reconnect must be restarted. A
restart of the operator does not remove the socket, because the
daemons run in their own containers and keep playing.

**A second claim selects the same output.** The scheduler allocates
an output to one claim, so the second pod stays `Pending` until the
first claim releases the output. An output whose `Sink` sets
`spec.sharedClaims` is the exception, as the next section describes.

## Share an output with other claims

A doorbell chime and a radio can play through one speaker at the
same time, and PipeWire mixes the two streams. First the cluster
owner must set `spec.sharedClaims` on the output's `Sink`, as
[Share an endpoint between claims](https://liken.sh/audio/docs/guides/rest/#share-an-endpoint-between-claims)
shows. Then each claim that shares the output requests one unit of
its `claims` capacity:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-chime
      namespace: media
    spec:
      devices:
        requests:
          - name: output
            exactly:
              deviceClassName: audio-sink
              selectors:
                - cel:
                    expression: |
                      device.attributes["audio.liken.sh"].sink == "kitchen-pci-0000-00-1f-3-hdmi-0"
              capacity:
                requests:
                  claims: "1"

The scheduler allocates the output to as many as `spec.sharedClaims`
of these claims at once. `status.claims` on the `Sink` lists every
claim that has the output:

    kubectl get sink kitchen-pci-0000-00-1f-3-hdmi-0 \
      -o jsonpath='{.status.claims[*].name}'

The `CLAIMS` column of `kubectl get sinks` shows only the first
claim, because a printer column prints only the first value that its
path matches. A claim that does not request `claims` capacity gets
the whole output, as it would on an output that is not shared. It
waits until every sharing claim has released the output, and no
sharing claim gets the output while it has it.

A sharing claim cannot change the output itself. The output plays
at the level that its `Sink` declares, and each workload sets the
volume of its own stream. A sharing claim also cannot choose a codec,
because a codec switch rebuilds a Bluetooth speaker's sink under
every claim that shares it. The pod of a sharing claim that chooses a
codec stays in `ContainerCreating`, and the pod's events give the
reason.

To put sound and picture on one monitor, continue with
[Pair sound with its screen](https://liken.sh/audio/docs/guides/pair/).
