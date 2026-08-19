---
title: Devices
weight: 10
toc: true
---

# Devices

`audio-operator` publishes one
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device for each playback PCM device on the sound card its pod
claims from [`liken`](https://liken.sh/docs/): each HDMI or
DisplayPort output, and the analog jack. Membership follows the
card's playback PCM devices, whether or not a cable is plugged into
each one. An output whose monitor is unplugged stays in the slice,
with taints, so a claim on it parks instead of failing.

On a machine whose pod also claimed a Bluetooth media bus from the
`bluetooth.liken.sh` driver, the operator publishes one more device
for each paired Bluetooth speaker. Membership there is the paired
set: a speaker that is switched off stays in the slice with taints,
and a speaker leaves it only when somebody unpairs it.

To a consumer, a speaker and an HDMI output are the same kind of
device. Both publish under `audio.liken.sh`, both select the same
way, and both deliver the same socket and a node name.

The operator publishes only A2DP sinks. HFP and HSP, the headset
profiles that add a microphone, would need a socket in the host's
network namespace, and this pod has no host network.

The operator publishes the devices into one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
per node, named `<node>-audio.liken.sh`, beside the slice `liken`
itself publishes:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml
    spec:
      driver: audio.liken.sh
      nodeName: kitchen
      devices:
        - name: a0-ab-51-33-b7-12
          attributes:
            output: {string: a0-ab-51-33-b7-12}
            address: {string: "A0:AB:51:33:B7:12"}
            name: {string: Kitchen Speaker}
            connectionType: {string: bluetooth}
            connected: {bool: true}
            codec: {string: sbc}
            codecs: {string: "sbc aptx sbc_xq aptx_ll"}
            sinkName: {string: bluez_output.A0_AB_51_33_B7_12.1}
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
            lpcmMaxRateHz: {int: 48000}
            lpcmBitDepths: {string: "16 20 24"}
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

You create this class, because a class a workload claims through
is cluster policy, and this manual calls it `audio-output`
throughout; it is yours to rename or narrow, the way a
`StorageClass` is. The class alone allocates any output on the
card. To name one, add a selector on the attributes below, as
[Play sound to an output](/docs/guides/claim/) shows.

The base ships one class of its own, `sound-card`, and that one is
not for consumers: the operator's own claim template names it, and
its pod claims every sound device on its node through it, from the
raw devices `liken`'s driver publishes.
[Devices](https://liken.sh/docs/reference/devices/) in the `liken`
manual describes those raw devices.

## The attributes

The device name is the ALSA card and PCM number, `card0-pcm3`. The
number comes from the codec's pin order, which the driver enumerates
the same way at every boot on the same hardware and kernel. It is
not stable across machines, and a claim that must survive a kernel
change selects on the monitor attributes instead. The name repeats
as the `output` attribute because a CEL selector reads attributes
and never the device's name.

A Bluetooth speaker's name is its peer MAC address in lowercase
with dashes, `a0-ab-51-33-b7-12`, because a DRA device name must be
a DNS label and a colon is not legal in one. The MAC is the one
identity BlueZ carries that survives a reboot.

| Attribute | Type | What it is |
|---|---|---|
| `output` | string | the device's name: `card0-pcm3` or `a0-ab-51-33-b7-12` |
| `connectionType` | string | `hdmi`, `displayport`, `analog`, or `bluetooth` |
| `sinkName` | string | the PipeWire node name a consumer's streams target |
| `card` | int | the ALSA card number |
| `pcm` | int | the PCM device number on that card |
| `manufacturer` | string | the monitor's three-letter PNP id, from the ELD: `GSM` is LG |
| `product` | string | the monitor's product code, four lowercase hexadecimal digits |
| `monitorName` | string | the monitor's name, the same EDID descriptor the display operator publishes as `model` |
| `lpcmChannels` | int | the highest uncompressed channel count the monitor accepts |
| `lpcmMaxRateHz` | int | the highest uncompressed sample rate the monitor accepts, in hertz |
| `lpcmBitDepths` | string | the uncompressed depths the monitor accepts, ascending: `16 20 24` |
| `speakers` | string | the speaker allocation, in the kernel's names: `FL/FR` |
| `monitor.liken.sh/id` | string | the pairing identity, described below |
| `address` | string | the speaker's peer MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `name` | string | the speaker's name, the alias BlueZ reports |
| `connected` | bool | whether `bluetoothd` has the speaker connected right now |
| `codec` | string | the A2DP codec the transport negotiated: `sbc`, `aptx`, `ldac` |
| `codecs` | string | every codec the speaker and this image both support, space separated, the one playing first, in the same spelling as `codec` |

A selector reads an unqualified attribute through the driver's
domain: `device.attributes["audio.liken.sh"].output`. The pairing
attribute is the one exception; it reads as the key `id` under the
domain `monitor.liken.sh`.

Only `output` is always present. The rest divide by the kind of
device that carries them: `card`, `pcm`, and the monitor attributes
are an ALSA output's, and `address`, `name`, `connected`, and
`codec` are a speaker's.

* The monitor attributes, `manufacturer` through
  `monitor.liken.sh/id`, are present on an HDMI or DisplayPort
  output whose monitor answers, and absent otherwise. They come from
  the ELD (EDID-Like Data), the block the graphics driver writes
  into the audio driver when a monitor answers.
* `connectionType` is `analog` on the jack and `bluetooth` on a
  speaker. On an HDMI or DisplayPort output it comes from the ELD
  block, so an output with no monitor publishes no connection type.
* `sinkName` is present while PipeWire holds a sink for the device,
  and left out when the name passes the API's 64-character limit on
  a string attribute.
* `codec` is present only while the speaker is connected, because a
  codec is a property of a live transport, not of a pairing.
* `codecs` is present under the same condition as `codec`, and
  absent when the device answers no choice. It holds whole names
  only, so a selector reads it with `.contains()`.

A selector on the list asks for a name inside a string:

    has(device.attributes["audio.liken.sh"].codecs) &&
    device.attributes["audio.liken.sh"].codecs.contains("aptx")

A selector that reads a missing attribute fails the whole
allocation, so guard every attribute in the bullets above:

    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

## The pairing identity

`monitor.liken.sh/id` pairs a monitor's speakers with that monitor's
screen, which the [display operator](https://display.liken.sh)
publishes from the same monitor's EDID. Both drivers build the value
the same way, byte for byte, because the scheduler compares them
under a `matchAttribute` constraint. The value is the lowercase PNP
id, the four-digit hexadecimal product code, then the lowercase
monitor name with each run of spaces turned to one dash. An LG UltraWide reads
`gsm-5b09-lg-ultrawide`. A monitor with no name keeps the two-part
form, `boe-095f`. The name is optional because one driver can read a
name the other cannot. If a missing name dropped the whole value,
one driver would publish the attribute and the other none, and a
constraint across the two would park forever.

The attribute has its own domain because an unqualified name
belongs to the driver that published it. A bare `monitorName` here
and a bare `model` in the display driver's slice would never match.

The ELD has no serial number, so the identity names a model, not
a unit. Two monitors of one model publish one value, and a
constraint is satisfied by either pairing.

## The taints

An output that cannot play is tainted, never deleted. Deleting it
would strand the claim that names it: the kubelet retries its
prepare call against a device in no slice, with no bound. A device
leaves the slice only when the card does.

A speaker leaves the slice only when somebody unpairs it.

| Key | Effect | When it appears | Who tolerates it |
|---|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play now | the consumer, with its own `tolerationSeconds` |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor answers on this HDMI or DisplayPort output | nobody |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire holds no node for this device | nobody |

A paired speaker that is switched off carries the `disconnected`
and `no-sink` taints, which is what lets a consumer claim it before
it exists to play into. The pod parks Unschedulable, somebody
switches the speaker on, WirePlumber builds the node, the operator
drops the taints, and the pod starts.


The `NoExecute` taint ends the holder's pod after the claim's
`tolerationSeconds`, so a consumer tolerates it to survive a short
drop. A tolerated `NoExecute` taint still permits allocation, so one
of the untolerated `NoSchedule` taints is always present with it,
and that one holds a new pod `Unschedulable` until the output can
play. The two reasons have separate keys because they have separate
repairs: `no-monitor` clears when the cable returns, and `no-sink`
clears only when the pod is replaced and PipeWire declares its nodes
again.

The analog jack has none of these while its sink is up. Most codecs
report nothing about the socket, and no signal can prove that sound
reaches anyone, so the operator publishes the port it reads and a
person who wired something claims it.

## What a prepared claim delivers

The delivery is a mount and two environment variables, applied to
the container by the runtime. There is no device node, because a
consumer does not open a PCM device: it connects to PipeWire, which
holds every PCM device on the card.

| What | Value |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that holds PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated device's sink name |

A Bluetooth speaker delivers the same three things. One PipeWire
holds the card's sinks and the radio's, so a pod that claims an
HDMI output and a speaker at once receives one socket and two node
names.


PipeWire's own client library reads both variables. A
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
declarations once, so the set is fixed while the daemon runs. A set
that followed the cables would need a restart every time somebody
moved one, and a card's PCM devices are fixed when its driver binds.
A PCM device that appears or leaves after that publishes with the
`no-sink` taint until the pod is replaced.

This path gives some things up. The card-profile machinery needs the
ALSA monitor, so there is no hardware mixer volume and no profile
switching: volume is PipeWire's software volume, and the channel
layout comes from what is connected when PipeWire starts.

## The Bluetooth sinks

A Bluetooth sink is built the other way round from an ALSA sink:
WirePlumber's bluez monitor is its source, because a Bluetooth
speaker creates nothing in the kernel. The audio exists only while
a sound server holds `bluetoothd`'s D-Bus socket, registers a media
endpoint, negotiates the codec, encodes the samples, and writes to
the L2CAP socket `bluetoothd` passes it as a file descriptor.

The pod reaches that socket through its claim. The
`bluetooth.liken.sh` driver publishes its media bus as a device,
this operator's class selects every device that stamps
`sound.liken.sh/supportsSound`, and the claim's `allocationMode` of
`All` takes the sound card and the media bus together. The delivery
is a read-only mount of the bus socket's directory and
`DBUS_SYSTEM_BUS_ADDRESS`. The pod's `declare` init container reads
that variable, and writes the WirePlumber fragment that turns the
bluez monitor on only when the variable is set.

WirePlumber names the node from `bluez_output`, the peer MAC with
underscores, and an object id, so the name can change when the
speaker reconnects. The operator republishes the name and rewrites
every prepared claim's file from the same graph read, the same way
it does for a sink that a profile change renamed.

    bluez_output.A0_AB_51_33_B7_12.1

A2DP is advertised on the radio exactly while this pod holds the
media bus, because BlueZ advertises the profile only when an
endpoint is registered. Pairing a speaker therefore works only
while this pod runs and holds the bus.


## Choosing the codec

WirePlumber picks the codec when a speaker connects. The `codecs`
attribute says what else the speaker offers, and a claim states the
one it wants in an opaque config block, the channel DRA gives a
driver for its own parameters:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: kitchen-speakers
      namespace: media
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
              deviceClassName: audio-output

`codec` is the only parameter this driver reads, and an unknown
key fails the prepare, so a typo stops the pod instead of playing
something nobody asked for. A block with no `requests` list applies
to every request in the claim, and a `requests` list narrows it to
the requests it names.

A `DeviceClass` can carry the same opaque block, which makes a
codec cluster policy for every claim that allocates through the
class. The scheduler resolves the class's config and the claim's
into one list on the allocation and marks each entry's source, and
that list is what the driver reads. The claim's own choice wins
over the class's, whichever order the two are listed in.

When the requested codec is not the one playing, the prepare call
writes it on the speaker's PipeWire device, waits for the rebuilt
sink to report the new codec, and only then delivers
`PIPEWIRE_NODE`. The wait is bounded at ten seconds, and the
renegotiation itself takes one to four.

Three things refuse. A codec stated for an output that is not a
Bluetooth speaker fails, because a sound card has no air codec. A
codec the speaker does not offer fails, and the message names the
offered list. A switch the graph never reports fails at the
ten-second bound. Each failure holds the pod in `ContainerCreating`,
and the claim's events carry the message.

Releasing the claim renegotiates nothing. The choice stands until
the next claim states one, or until the speaker reconnects, which
hands the pick back to WirePlumber.

## The sink's volume

Every sink this pod builds is born at unity. The pod stores no
volumes, and WirePlumber's own default for an unstored sink is 40
percent, a desktop guard that would cost resolution on a machine
that plays only what a claim delivers. Loudness belongs to the
consumer's stream volume and to the hardware behind the jack.

A prepare on a Bluetooth speaker also writes unity on every
channel of the sink it delivers, switch or no switch. A speaker
allocates to one claim at a time, so any level a prepare finds is a
leftover from an earlier tenant or a hand-run tool, never the
arriving consumer's choice. The card's own outputs take no such
write.

## The slice's lifetime

The operator creates its slice on the first pass, rewrites it when
the card or the graph disagrees with it, and never deletes it. The
`Node` owns the slice, so a node that leaves the cluster takes the
slice with it. The slice outlives the operator's pod on purpose:
prepared claims keep naming its devices across a restart. Removing
the operator for good ends with:

    kubectl delete resourceslice <node>-audio.liken.sh
