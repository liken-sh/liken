---
title: Devices
weight: 10
toc: true
---

# Devices

`audio-operator` publishes one
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device for each **playback PCM device** on the sound card its pod
claims from [`liken`](https://liken.sh/docs/): each HDMI or
DisplayPort output, and the analog jack. Membership follows the
card, not the cables. An output whose monitor is unplugged stays in
the slice, with taints, so a claim on it parks instead of failing.

The devices live in one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
per node, named `<node>-audio.liken.sh`, beside the slice `liken`
itself publishes:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml
    spec:
      driver: audio.liken.sh
      nodeName: kitchen
      devices:
        - name: card0-pcm3
          attributes:
            output: {string: card0-pcm3}
            card: {int: 0}
            pcm: {int: 3}
            connectionType: {string: hdmi}
            manufacturer: {string: GSM}
            product: {string: "5b09"}
            monitorName: {string: LG ULTRAWIDE}
            lpcmChannels: {int: 2}
            speakers: {string: FL/FR}
            sinkName: {string: liken.audio.card0-pcm3}
            monitor.liken.sh/id: {string: gsm-5b09-lg-ultrawide}
        - name: card0-pcm7
          attributes:
            output: {string: card0-pcm7}
            card: {int: 0}
            pcm: {int: 7}
            sinkName: {string: liken.audio.card0-pcm7}
          taints:
            - key: audio.liken.sh/disconnected
              effect: NoExecute
            - key: audio.liken.sh/no-monitor
              effect: NoSchedule

## The device class

A consumer claims through a
[`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
that selects every device this driver publishes:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: audio-output
    spec:
      selectors:
        - cel:
            expression: device.driver == "audio.liken.sh"

A class is cluster-scoped policy, so it is yours to name and create;
the base ships none, and this manual calls the class `audio-output`
throughout. The class alone allocates any output on the card. To
name one, add a selector on the attributes below, as
[Play sound to an output](/docs/guides/claim/) shows.

[Install the operator](/docs/guides/install/) also has you create
`audio-controller`. That one is not for consumers: it selects the
raw sound card that `liken`'s own driver publishes, and the
operator's pod claims every one on its node through it.
[Devices](https://liken.sh/docs/reference/devices/) in the `liken`
manual describes those raw devices.

## The attributes

The device name is the ALSA card and PCM number, `card0-pcm3`. The
number comes from the codec's pin order, which the driver enumerates
the same way at every boot on the same hardware and kernel; it is
not stable across machines, and a claim that must survive a kernel
change selects on the monitor attributes instead. The name repeats
as the `output` attribute because a CEL selector reads attributes
and never the device's name.

| Attribute | Type | What it is |
|---|---|---|
| `output` | string | the device's name: `card0-pcm3` |
| `card` | int | the ALSA card number |
| `pcm` | int | the PCM device number on that card |
| `connectionType` | string | `hdmi`, `displayport`, or `analog` |
| `sinkName` | string | the PipeWire node name a consumer's streams target |
| `manufacturer` | string | the monitor's three-letter PNP id, from the ELD: `GSM` is LG |
| `product` | string | the monitor's product code, four lowercase hexadecimal digits |
| `monitorName` | string | the monitor's name, the same EDID descriptor the display operator publishes as `model` |
| `lpcmChannels` | int | the highest uncompressed channel count the monitor accepts |
| `speakers` | string | the speaker allocation, in the kernel's names: `FL/FR` |
| `monitor.liken.sh/id` | string | the pairing identity, described below |

A selector reads an unqualified attribute through the driver's
domain: `device.attributes["audio.liken.sh"].output`. The pairing
attribute is the one exception; it reads as the key `id` under the
domain `monitor.liken.sh`.

Only `output`, `card`, and `pcm` are always present:

* The monitor attributes, `manufacturer` through
  `monitor.liken.sh/id`, are present on an HDMI or DisplayPort
  output whose monitor answers, and absent otherwise. They come from
  the ELD (EDID-Like Data), the block the graphics driver writes
  into the audio driver when a monitor answers.
* `connectionType` is `analog` on the jack. On an HDMI or
  DisplayPort output it comes from the ELD block, so an output with
  no monitor publishes no connection type.
* `sinkName` is present while PipeWire holds a sink for the output,
  and left out when the name passes the API's 64-character limit on
  a string attribute.

A selector that reads a missing attribute fails the whole
allocation, so guard every attribute in the two bullets above:

    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

## The pairing identity

`monitor.liken.sh/id` pairs a monitor's speakers with that monitor's
screen, which the [display operator](https://display.liken.sh)
publishes from the same monitor's EDID. Both drivers build the value
the same way, byte for byte, because the scheduler compares them
under a `matchAttribute` constraint: the lowercase PNP id, the
four-digit hexadecimal product code, then the lowercase monitor name
with each run of spaces turned to one dash. An LG UltraWide reads
`gsm-5b09-lg-ultrawide`. A monitor with no name keeps the two-part
form, `boe-095f`. The name is optional because one driver can read a
name the other cannot; if a missing name dropped the whole value,
one driver would publish the attribute and the other none, and a
constraint across the two would park forever.

The attribute carries its own domain because an unqualified name
belongs to the driver that published it. A bare `monitorName` here
and a bare `model` in the display driver's slice would never match.

The ELD carries no serial number, so the identity names a model, not
a unit. Two monitors of one model publish one value, and a
constraint is satisfied by either pairing.

## The taints

An output that cannot play is tainted, never deleted. Deleting it
would strand the claim that names it: the kubelet retries its
prepare call against a device in no slice, with no bound. A device
leaves the slice only when the card does.

| Key | Effect | When it appears | Who tolerates it |
|---|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play now | the consumer, with its own `tolerationSeconds` |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor answers on this HDMI or DisplayPort output | nobody |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire holds no node for this PCM device | nobody |

The `NoExecute` taint ends the holder's pod after the claim's
`tolerationSeconds`, so a consumer tolerates it to survive a short
drop. A tolerated `NoExecute` taint still permits allocation, so one
of the untolerated `NoSchedule` taints always stands beside it, and
that one holds a new pod `Unschedulable` until the output can really
play. The two reasons carry separate keys because they have separate
repairs: `no-monitor` clears when the cable returns, and `no-sink`
clears only when the pod is replaced and PipeWire declares its nodes
again.

The analog jack carries none of these while its sink is up. Most
codecs report nothing about the socket, and no signal can prove that
sound reaches anyone, so the operator publishes the port it can see
and a person who wired something claims it.

## What a prepared claim delivers

The delivery is a mount and two environment variables, applied to
the container by the runtime. There is no device node, because a
consumer does not open a PCM device: it connects to PipeWire, which
holds every PCM device on the card.

| What | Value |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that holds PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated output's sink name |

Both variables are read by PipeWire's own client library: a
`PIPEWIRE_REMOTE` that starts with a slash is used as an absolute
socket path, and `PIPEWIRE_NODE` sets `target.object` on every
stream. The mount is read-only because connecting to a Unix socket
needs write permission on the socket, not on the directory.

Each variable holds one value, so two allocations delivered to one
container overwrite, and the last wins. One container holds at most
one output; a pod that plays into two outputs runs two containers.

## The sinks PipeWire holds

The sink a consumer targets is one this operator declared.
WirePlumber's ALSA monitor enumerates cards through libudev, and a
`liken` machine runs no udevd, so the monitor would build nothing.
Instead, the pod's `declare` init container enumerates the card's
playback PCM devices through the ALSA control interface and writes
one sink declaration for each into a PipeWire configuration drop-in,
before the daemons start. The sink name is derived from the ALSA
address alone, `liken.audio.` plus the device name, so it is the
same at every start on the same card:

    liken.audio.card0-pcm3

Every PCM device is declared, monitor or not. PipeWire reads the
declarations once, so the set is fixed for the daemon's life; a set
that followed the cables would need a restart every time somebody
moved one, and a card's PCM devices are fixed when its driver binds.
A PCM device that appears or leaves after that publishes with the
`no-sink` taint until the pod is replaced.

This path gives some things up. The card-profile machinery needs the
ALSA monitor, so there is no hardware mixer volume and no profile
switching: volume is PipeWire's software volume, and the channel
layout comes from what is connected when PipeWire starts.

## The slice's lifetime

The operator creates its slice on the first pass, rewrites it when
the card or the graph disagrees with it, and never deletes it. The
`Node` owns the slice, so a node that leaves the cluster takes the
slice with it. The slice outlives the operator's pod on purpose:
prepared claims keep naming its devices across a restart. Removing
the operator for good ends with:

    kubectl delete resourceslice <node>-audio.liken.sh
