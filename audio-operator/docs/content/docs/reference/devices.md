---
title: Devices
weight: 10
toc: true
---

# Devices

`audio-operator` publishes one
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device for each PCM device on the sound card that its pod claims from
[`liken`](https://liken.sh/docs/). The playback devices are sinks: each
HDMI or DisplayPort output, the analog jack, and the playback side of
a USB card. The capture devices are sources. A PCM device is in the
slice whether or not a cable is plugged into it. When the monitor of
an output is unplugged, the output stays in the slice with taints, so
a claim on it stays Pending instead of failing.

Every device is also a [`Sink`](/docs/reference/sinks/) or a
[`Source`](/docs/reference/sources/) resource with the same name. A
claim selects a device from the slice. You read the endpoint's state
from the resource, and you set the endpoint's volume and controls on
it without a claim.

On a machine whose pod also claimed a Bluetooth media bus from the
`bluetooth.liken.sh` driver, the operator publishes one more device
for each paired Bluetooth speaker. A speaker that is switched off
stays in the slice with taints, and it leaves the slice only when
somebody unpairs it.

A consumer claims a Bluetooth speaker, an HDMI output, and a
microphone the same way. Each is a device of the `audio.liken.sh`
driver, and each claim gives the container the same socket and a
node name.

The operator publishes only A2DP sinks. The headset profiles HFP and
HSP, which add a microphone, need a socket in the host's network
namespace, and this pod has no host network.

The operator writes the devices into one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
for each node, named `<node>-audio.liken.sh`. It is separate from the
slice that `liken` itself publishes for the node:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml
    spec:
      driver: audio.liken.sh
      nodeName: kitchen
      devices:
        - name: a0-ab-51-33-b7-12
          attributes:
            sink: {string: a0-ab-51-33-b7-12}
            address: {string: "A0:AB:51:33:B7:12"}
            name: {string: Kitchen Speaker}
            connectionType: {string: bluetooth}
            connected: {bool: true}
            codec: {string: sbc}
            codecs: {string: "sbc aptx sbc_xq aptx_ll"}
            nodeName: {string: bluez_output.A0_AB_51_33_B7_12.1}
        - name: kitchen-pci-0000-00-1f-3-hdmi-0
          attributes:
            sink: {string: kitchen-pci-0000-00-1f-3-hdmi-0}
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
            nodeName: {string: liken.audio.card0-pcm3}
            monitor.liken.sh/id: {string: gsm-5b09-lg-ultrawide}
        - name: kitchen-usb-0573-1573-a34004801402-usb-audio-capture
          attributes:
            source: {string: kitchen-usb-0573-1573-a34004801402-usb-audio-capture}
            card: {int: 1}
            pcm: {int: 0}
            connectionType: {string: usb}
            nodeName: {string: liken.audio.card1-pcm0c}
        - name: kitchen-pci-0000-00-1f-3-hdmi-1
          attributes:
            sink: {string: kitchen-pci-0000-00-1f-3-hdmi-1}
            card: {int: 0}
            pcm: {int: 7}
            nodeName: {string: liken.audio.card0-pcm7}
          taints:
            - key: audio.liken.sh/disconnected
              effect: NoExecute
            - key: audio.liken.sh/no-monitor
              effect: NoSchedule

## The device classes

A workload claims a device through a
[`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
that selects one direction. A sink has the `sink` attribute and a
source has the `source` attribute, so a class selects on whether one
of them is present:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: audio-sink
    spec:
      selectors:
        - cel:
            expression: has(device.attributes["audio.liken.sh"].sink)
    ---
    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: audio-source
    spec:
      selectors:
        - cel:
            expression: has(device.attributes["audio.liken.sh"].source)

You create these classes, because the classes that workloads claim
through are cluster policy. This manual calls them `audio-sink` and
`audio-source`. You can rename or narrow them, the way you can with a
`StorageClass`. A claim that names only the class gets any endpoint of
that direction. To pick one endpoint, add a selector on the attributes
below, as [Play sound to an output](/docs/guides/claim/) shows.

The base has one class of its own, `sound-card`, which workloads do
not use. The operator's claim template names it, and the operator's
pod claims every sound device on its node through it. Those are the
raw devices that `liken`'s driver publishes.
[Devices](https://liken.sh/docs/reference/devices/) in the `liken`
manual describes them.

## The attributes

The device name comes from the hardware's own identity, so it stays
the same across a reboot and when you add a second card. For an
onboard card it is the node and the PCI address. For a USB card it is
the node, the vendor, the product, and the serial, when the card has
one. The name ends with the driver's own name for the PCM device,
`hdmi-0` or `usb-audio`. The name of a capture endpoint ends with
`-capture`. When a USB name would be longer than 63 characters, the
operator uses a hash of the serial in place of the serial. The
[`Sink` reference](/docs/reference/sinks/#the-name) shows the three
forms. The `sink` or `source` attribute repeats the name, because a
CEL selector can read attributes but not the device name.

The name of a Bluetooth speaker is its peer MAC address in lowercase,
with dashes in place of colons: `a0-ab-51-33-b7-12`. A DRA device name
must be a DNS label, and a colon is not allowed in one. BlueZ keeps
the MAC address of a speaker across reboots, so the name stays the
same.

| Attribute | Type | What it is |
|---|---|---|
| `sink` | string | a playback endpoint's name: `kitchen-pci-0000-00-1f-3-hdmi-0` or `a0-ab-51-33-b7-12` |
| `source` | string | a capture endpoint's name: `kitchen-usb-0573-1573-a34004801402-usb-audio-capture` |
| `connectionType` | string | `hdmi`, `displayport`, `analog`, `usb`, or `bluetooth` |
| `nodeName` | string | the PipeWire node name a consumer's streams target |
| `card` | int | the ALSA card number, which can change at a reboot |
| `pcm` | int | the PCM device number on that card |
| `manufacturer` | string | the monitor's three-letter PNP id, from the ELD: `GSM` is LG |
| `product` | string | the monitor's product code, four lowercase hexadecimal digits |
| `monitorName` | string | the monitor's name, from the same EDID descriptor that the display operator publishes as `model` |
| `lpcmChannels` | int | the highest uncompressed channel count the monitor accepts |
| `lpcmMaxRateHz` | int | the highest uncompressed sample rate the monitor accepts, in hertz |
| `lpcmBitDepths` | string | the uncompressed depths the monitor accepts, ascending: `16 20 24` |
| `speakers` | string | the speaker allocation, in the kernel's names: `FL/FR` |
| `encodedFormats` | string | the encoded formats the monitor decodes itself, from the ELD, in PipeWire's names and separated by spaces: `AC3 DTS EAC3 DTS-HD TrueHD`. Absent when the monitor lists none |
| `eldVersion` | int | the version number of the ELD block |
| `portID` | int | the `Port_ID` of the ELD block, which identifies the display connector that the output plays into. Absent when the block gives 0 |
| `monitor.liken.sh/id` | string | the identity that pairs the speakers with the screen, described in [The pairing identity](#the-pairing-identity) |
| `address` | string | the speaker's peer MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `name` | string | the speaker's name, the alias BlueZ reports |
| `connected` | bool | whether `bluetoothd` has the speaker connected now |
| `codec` | string | the A2DP codec the transport negotiated: `sbc`, `aptx`, `ldac` |
| `codecs` | string | the codecs that the speaker and this image both support, separated by spaces, with the codec that is playing first, spelled like `codec` |

A selector reads an attribute through the driver's domain:
`device.attributes["audio.liken.sh"].sink`. The pairing attribute is
the exception. A selector reads it as the key `id` under the domain
`monitor.liken.sh`.

Every device has exactly one of `sink` and `source`. The other
attributes depend on the kind of device. `card`, `pcm`, and the
monitor attributes belong to an ALSA endpoint. `address`, `name`,
`connected`, `codec`, and `codecs` belong to a Bluetooth speaker.

* The monitor attributes, `manufacturer` through
  `monitor.liken.sh/id`, are present on an HDMI or DisplayPort output
  whose monitor has responded, and absent otherwise. They come from the
  ELD (EDID-Like Data), the block that the graphics driver writes into
  the audio driver when a monitor responds.
* `connectionType` is `analog` on the jack, `usb` on a USB card, and
  `bluetooth` on a speaker. On an HDMI or DisplayPort output it comes
  from the ELD block, so an output with no monitor has no connection
  type.
* `nodeName` is present while PipeWire has a node for the device. It
  is left out when the name is longer than 64 characters, which is the
  API's limit for a string attribute.
* `codec` is present only while the speaker is connected and PipeWire
  has its sink, because a codec belongs to a live connection and not
  to a pairing.
* `name` is absent when BlueZ reports no alias for the speaker.
* `codecs` is present under the same condition as `codec`, and absent
  when the device reports no choice of codecs. It has whole codec
  names only, so a selector reads it with `.contains()`.

This selector looks for one codec name inside the `codecs` string:

    has(device.attributes["audio.liken.sh"].codecs) &&
    device.attributes["audio.liken.sh"].codecs.contains("aptx")

A selector that reads an attribute that is absent makes the whole
allocation fail. Guard each attribute that the bullets above say can
be absent:

    has(device.attributes["monitor.liken.sh"].id) &&
    device.attributes["monitor.liken.sh"].id == "gsm-5b09-lg-ultrawide"

## The pairing identity

`monitor.liken.sh/id` pairs a monitor's speakers with that monitor's
screen. The [display operator](https://liken.sh/display/) publishes
the same attribute for the screen, from the same monitor's EDID. Both
drivers build the value the same way, byte for byte, because the
scheduler compares the two values in a `matchAttribute` constraint.
The value is the lowercase PNP id, then the four-digit hexadecimal
product code, then the lowercase monitor name with each run of spaces
replaced by one dash. An LG UltraWide gives `gsm-5b09-lg-ultrawide`.
A monitor with no name gives only the first two parts, `boe-095f`.
The name is optional because one driver can read a name that the
other cannot. If a missing name removed the whole value, one driver
would publish the attribute and the other would not, and a constraint
across the two would never be satisfied.

The attribute has its own domain because a name with no domain
belongs to the driver that publishes it. A `monitorName` in this
driver's slice and a `model` in the display driver's slice would
never match.

The ELD has no serial number, so the identity names a model and not a
single monitor. Two monitors of one model publish the same value, and
a constraint accepts either pairing.

## The taints

The operator never deletes an output that cannot play. It adds a taint
instead. If the operator deleted the device while a claim still named
it, the kubelet would retry the prepare call for a device that is in
no slice, with no limit on the retries. A device leaves the slice only
when its card leaves the machine, or when somebody unpairs a
Bluetooth speaker.

| Key | Effect | When it appears | Who tolerates it |
|---|---|---|---|
| `audio.liken.sh/disconnected` | `NoExecute` | the output cannot play now | the consumer, with its own `tolerationSeconds` |
| `audio.liken.sh/no-monitor` | `NoSchedule` | no monitor has responded on this HDMI or DisplayPort output for 5 seconds | no consumer |
| `audio.liken.sh/no-sink` | `NoSchedule` | PipeWire has no node for this device, sink or source | no consumer |

A paired speaker that is switched off has the `disconnected` and
`no-sink` taints. This lets a consumer claim the speaker before its
node exists. The consumer's pod stays Unschedulable until somebody
switches the speaker on. Then WirePlumber creates the node, the
operator removes the taints, and the pod starts.

The `disconnected` taint is `NoExecute`, so it evicts the pod that
uses the claim when the claim's `tolerationSeconds` runs out. A
consumer that should survive a short drop tolerates it for that long.
The scheduler still allocates a device whose `NoExecute` taint the
claim tolerates, so a card's output that cannot play also has a
`NoSchedule` taint that no consumer tolerates. That taint keeps a new
pod Unschedulable until the output can play. `no-monitor` and
`no-sink` are separate keys because they clear in different ways.
`no-monitor` clears when a monitor responds on the output again.
`no-sink` on a card's output clears only when the pod is replaced and
PipeWire declares its nodes again.

The operator reports that a monitor is gone only after the output has
read no monitor for 5 seconds. An A/V receiver in standby can drop hot
plug detect for a fraction of a second, several times a minute. Each
time, the graphics driver clears the output's ELD, and it writes the
same block again when the receiver responds. A drop shorter than 5
seconds changes nothing in the slice or on the `Sink`, and the channel
layout from the ELD stays, so PipeWire does not restart. While the output's ELD keeps
changing, the operator can take up to 20 seconds to report that a
monitor left.

When the operator's pod starts during such a drop, it has no earlier
reading of the monitor, so it uses the monitor that the previous pod
published for that output in the slice. The `declare` init container
also waits up to 5 seconds for that monitor to respond before it
declares the output's layout. It waits only for an output whose `Sink`
reports a monitor, so the HDMI outputs that never had a monitor add no
wait.

The analog jack has none of these taints while its sink exists. Most
codecs do not report whether a plug is in the jack, and no signal
shows that the sound reaches a listener. So the operator publishes the
jack whenever PipeWire has its sink, and you claim it when you have
connected something to it.

## What a prepared claim gives the container

When the operator prepares a claim, the container runtime adds a
mount and two environment variables to the container. The container
gets no device node, because a consumer does not open a PCM device. It
connects to PipeWire, which owns every PCM device on the card.

| What | Value |
|---|---|
| mount | `/var/run/audio.liken.sh`, read-only, the directory that has PipeWire's socket |
| `PIPEWIRE_REMOTE` | `/var/run/audio.liken.sh/pipewire-0` |
| `PIPEWIRE_NODE` | the allocated device's node name |

A claim on a Bluetooth speaker or a source gives the container the
same mount and variables. For a source, `PIPEWIRE_NODE` names the
capture node, and `target.object` selects it for a capture stream as
it does for playback. One PipeWire serves both the card's nodes and
the radio's, so a pod that claims an HDMI output and a speaker gets
one socket and two node names.

PipeWire's client library reads both variables. It uses a
`PIPEWIRE_REMOTE` that starts with a slash as an absolute socket path,
and it sets `target.object` on every stream from `PIPEWIRE_NODE`. The
mount is read-only because connecting to a Unix socket needs write
permission on the socket and not on the directory.

Each variable holds one value, so when two allocations reach one
container, the last one overwrites the first. A container can use only
one output. A pod that plays to two outputs needs two containers.

## The PipeWire nodes

The node that a consumer targets is a node that this operator
declared. WirePlumber's ALSA monitor finds cards through libudev, and
a `liken` machine runs no udevd, so the monitor would create no node.
So the pod's `declare` init container reads the card's PCM devices
through the ALSA control interface, before the daemons start. It
writes one node declaration for each PCM device into a PipeWire
drop-in: a sink for a playback PCM device and a source for a capture
PCM device. The node name comes from the ALSA address alone. It is
`liken.audio.` followed by the card and PCM numbers, with a `c` at the
end for a capture node, so the same card gets the same names at every
start:

    liken.audio.card0-pcm3
    liken.audio.card1-pcm0c

The init container declares every PCM device, whether or not a monitor
is connected. PipeWire reads the declarations once, so the set of
nodes cannot change while the daemon runs. Because every PCM device
is declared, moving a cable needs no restart, and a card's PCM
devices do not change after its driver binds. A PCM device that
appears or leaves after the pod starts has the `no-sink` taint until
the pod is replaced.

Card profile switching is a job of WirePlumber's ALSA monitor, so
the pod does not switch card profiles. Each sink's channel layout comes
from its `Sink`'s `spec.layout`, the monitor's ELD, or a USB device's
channel map, as the
[`Sink` reference](/docs/reference/sinks/#the-channel-layout)
describes, and a new layout restarts PipeWire. A node's volume is
PipeWire's software gain. The operator reads and writes the card's own
mixer controls through the ALSA control interface, and shows them on
the endpoint's [`Sink`](/docs/reference/sinks/) or
[`Source`](/docs/reference/sources/).

## The Bluetooth sinks

WirePlumber's bluez monitor creates each Bluetooth sink. The
`declare` init container declares only the sinks and sources of the
sound card. A Bluetooth speaker
creates nothing in the kernel, so the sink exists only while a sound
server uses `bluetoothd`'s D-Bus socket to register a media endpoint
and negotiate the codec. The sound server then encodes the samples and
writes them to the L2CAP socket that `bluetoothd` passes to it as a
file descriptor.

The pod reaches that socket through its claim. The `bluetooth.liken.sh`
driver publishes its media bus as a device. This operator's class
selects every device that has `sound.liken.sh/supportsSound`, and the
claim's `allocationMode` of `All` allocates the sound card and the
media bus together. The container runtime mounts the directory of the
bus socket read-only and sets `DBUS_SYSTEM_BUS_ADDRESS`. The `declare`
init container reads that variable, and it writes the WirePlumber
fragment that turns the bluez monitor on only when the variable is
set.

WirePlumber names the node `bluez_output.`, then the peer MAC address
with underscores, then an object id. The name can change when the
speaker reconnects. When it does, the operator publishes the new name
and rewrites the file of every prepared claim from the same read of
the graph, as it does for a sink that a profile change renamed.

    bluez_output.A0_AB_51_33_B7_12.1

BlueZ advertises A2DP on the radio only while an endpoint is
registered, which is only while this pod has the media bus. So you can
pair a speaker only while this pod runs and has the bus.

## Choosing the codec

WirePlumber picks the codec when a speaker connects. The `codecs`
attribute lists the other codecs that the speaker offers. A claim
selects a codec in an opaque config block, which is how DRA passes a
driver its own parameters:

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
              deviceClassName: audio-sink

`codec` is the only parameter that this driver reads. An unknown key
makes the prepare fail, so a typo stops the pod and no stream plays
with a setting that nobody chose. A block with no `requests` list
applies to every request in the claim, and a `requests` list limits it
to the requests that it names.

A `DeviceClass` can have the same opaque block, which sets a codec for
every claim that allocates through the class. The scheduler combines
the class's config and the claim's config into one list on the
allocation, and marks the source of each entry. The driver reads that
list. The claim's choice overrides the class's, in whatever order the
two are listed.

When the requested codec is not the one that is playing, the prepare
call writes the codec on the speaker's PipeWire device and waits for
the rebuilt sink to report the new codec. Only then does it set
`PIPEWIRE_NODE`. The wait ends after ten seconds.

The prepare call fails when:

* the claim requests a codec for an output that is not a Bluetooth
  speaker, because a sound card has no wireless codec,
* the claim requests a codec that the speaker does not offer, and the
  message names the codecs that it offers,
* the graph does not report the new codec within the ten seconds.

The pod then stays in `ContainerCreating`, and the events of the claim
have the message.

Releasing the claim does not renegotiate the codec. The chosen codec
stays until the next claim requests another one, or until the speaker
reconnects, when WirePlumber picks the codec again.

## The sink's volume

The pod creates every sink at unity gain (100 percent), and it stores
no volumes. WirePlumber's own default for a sink with no stored volume
is 40 percent. That default suits a desktop. On a machine that plays
only what its claims send, it would lower the resolution of the
signal. A consumer's stream volume is the consumer's own
setting. You set the endpoint's level in `spec.volume.level` on its
`Sink`, as [Set endpoint volume and controls](/docs/guides/rest/)
shows, and an endpoint with no declared level stays at unity.

When no other claim has a Bluetooth speaker, a prepare on it writes
unity on every channel of the sink that it gives to the consumer, even
if the claim has no volume setting. Any level that the prepare finds
was left by an earlier consumer or by a tool that somebody ran by
hand, and the arriving consumer did not choose it. A prepare of a
share that arrives while another claim has the speaker writes no
level, because the stream of that claim plays at the level that the
speaker has now. The card's own outputs get no such write.

## Shared endpoints

A device allocates to one claim at a time, unless its `Sink` or
`Source` sets `spec.sharedClaims`.
[Share an endpoint between claims](/docs/guides/rest/#share-an-endpoint-between-claims)
shows how to set it. When the resource sets `spec.sharedClaims`, the
device in the slice has these fields too:

    - name: kitchen-pci-0000-00-1f-3-hdmi-0
      allowMultipleAllocations: true
      capacity:
        claims:
          value: "4"

The capacity has no `requestPolicy`, so the scheduler charges the
whole capacity to a request that does not ask for `claims`. A claim
that was written for an exclusive device therefore gets the whole
device and shares it with nobody. A request that asks for one claim shares the device
with the other requests that asked for one, up to the count:

    exactly:
      deviceClassName: audio-sink
      capacity:
        requests:
          claims: "1"

The scheduler enforces the count through Kubernetes' consumable
capacity (the `DRAConsumableCapacity` feature gate, beta and on by
default since Kubernetes 1.36). A request that asks for `claims` can
match only a device that publishes the capacity, so a shared claim
never lands on an exclusive device. Each allocation of a shared device
has its own `shareID` and `consumedCapacity` in the claim's status.

The operator treats a claim as a share when the request that allocated
the device asks for `claims`. A share cannot set a codec, in the claim
or in its `DeviceClass`. A codec switch rebuilds the speaker's sink,
and every share plays through that sink. The prepare fails with a
message that says so, and the pod waits in `ContainerCreating`. The
`Sink`'s `spec.codec` sets the codec while no claim has the speaker.

`status.claims` on the resource lists every claim that has the device.
A change to `spec.sharedClaims` writes the slice again and applies to
the next allocation. The claims that already have the device keep it.

## The slice's lifetime

The operator creates its slice on the first reconcile pass and writes
it again when the card or the PipeWire graph no longer matches it. It
never deletes the slice. The `Node` owns the slice, so a node that
leaves the cluster takes the slice with it. The slice stays after the
operator's pod stops, because prepared claims keep naming its devices
across a restart. To remove the operator for good, finish with:

    kubectl delete resourceslice <node>-audio.liken.sh
