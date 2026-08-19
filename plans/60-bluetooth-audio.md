# Bluetooth audio

A Bluetooth speaker publishes as an ordinary `audio.liken.sh` sink.
The same operator and the same PipeWire graph that serve the HDMI
outputs and the analog jack serve the speaker. The Bluetooth operator
owns the path to the speaker: the radio, the pairing, and the bonds.
The audio operator owns playback into it. One thing passes between
them, and it passes through the API: the Bluetooth operator publishes
its media bus as a DRA device, and the audio operator claims it.

This milestone spans three repositories. This document is the design
record. Each operator's own plans directory records its half of the
build.

## The problem

A paired Bluetooth speaker creates nothing in the kernel. There is no
sysfs node and no ALSA card. The audio exists only while a sound
server is connected to `bluetoothd`'s D-Bus socket. The sound server
must register a media endpoint, negotiate the codec, encode the
samples itself, and hold the L2CAP socket that `bluetoothd` passes to
it as a file descriptor. BlueZ does not advertise A2DP at all until an
endpoint registers, so a `bluetoothd` with no sound server beside it
cannot pair a speaker in a useful way. The Bluetooth operator's plan
01 read all of this from the upstream sources, and its citations
remain valid. This document does not repeat them.

That fact splits Bluetooth audio across two operators. The pairing
state is in `bluetoothd`, which milestone 58's operator runs. The
sound server, the graph, and the sink contract are in milestone 59's
audio operator. Plan 01 of the Bluetooth operator resolved the split
by putting a second PipeWire in the Bluetooth pod and publishing
speakers under `bluetooth.liken.sh`. That design has three costs:

* Two sound stacks run on one machine.
* Two sockets exist where `PIPEWIRE_REMOTE` can name only one.
* Its fallback adds a third operator, if an audio fault that restarts
  the controllers proves unacceptable.

This milestone replaces that design.

## The design

Permission to do Bluetooth audio on a machine and permission to
connect to `bluetoothd`'s socket are the same permission. The
Bluetooth operator makes that permission claimable. It publishes one
device per adapter, the media bus, in the ResourceSlice it already
writes. The device is exclusive, because one radio supports one sound
server: two endpoints registered on one `bluetoothd` have no contract
over the streams. The claim's CDI delivery is a mount of the bus
socket's directory, plus `DBUS_SYSTEM_BUS_ADDRESS` set to a path in
that mount. `DBUS_SYSTEM_BUS_ADDRESS` is the standard variable
PipeWire reads.

The audio operator claims the bus through an attribute that both
drivers stamp, in a domain neither driver owns. Milestone 59 used the
same pattern with `monitor.liken.sh/id`:

```yaml
# The attribute states that the device supports a sound server.
# liken stamps it on the HDA controller, the Bluetooth operator
# stamps it on the media bus. The audio operator's class selects
# this attribute and names no driver.
attributes:
  sound.liken.sh/supportsSound: {bool: true}
```

The audio operator's DeviceClass changes from "liken's audio
controller" to "any device that supports a sound server", and its
ResourceClaimTemplate
keeps `allocationMode: All`. Each DaemonSet pod then allocates every
device on its node that can make sound: the card, the radio's media
bus, or both. A node with neither parks the pod `Pending`, which is
the placement rule the operator already relies on.

With the bus delivered, the audio operator enables WirePlumber's
Bluetooth monitor. The monitor registers the media endpoint with
`bluetoothd`, so A2DP is advertised, and speakers are pairable,
exactly while the audio pod holds the claim. When a speaker connects,
`bluetoothd` sends the transport file descriptor over the bus,
WirePlumber builds the sink node, and the sink joins the one graph
that holds the HDMI sinks. The operator publishes it under
`audio.liken.sh`, named by the peer MAC the way the Bluetooth
operator names its devices, and tainted by milestone 56's rules while
the speaker is disconnected. A consumer claims it like any other sink
and receives the same delivery: the `/var/run/audio.liken.sh` mount,
`PIPEWIRE_REMOTE`, and `PIPEWIRE_NODE`.

One graph and one socket answer plan 01's open question about mixed
claims: a pod that must reach an HDMI output and a Bluetooth speaker
at once names one socket and two nodes.

The design adds no privilege anywhere. The audio operator keeps none.
A2DP needs no `AF_BLUETOOTH` socket in the sound server's pod, for
three reasons: `bluetoothd` created the L2CAP socket in the initial
network namespace, a socket's namespace is fixed at creation, and
read, write, and `shutdown` on an existing socket do no namespace
check. The descriptor crosses the pod boundary by `SCM_RIGHTS` over
the mounted bus socket. Plan 01 recorded both facts with sources when
it set aside the cross-pod mount. This design is that mount, declared
to the scheduler as a claim.

## Who does what

### liken, this repository

The machine operator stamps `sound.liken.sh/supportsSound: true` on the
audio controller device it already publishes, in
`machine-operator/publishing.go`. That is the only change in this
repository. The OS still publishes raw hardware and nothing refined.

### The Bluetooth operator

The first three items landed with that operator's release
2026.08.19-004, drilled on liken-1 on 2026-08-19 and recorded as its
plan 05. The drill also proved this plan's drill 1 on liken-1: the
audio pod's fresh claim allocated the sound card, a USB audio device,
and the media bus, three devices across the two drivers.

* Publishes the media bus device, one per adapter, exclusive, with
  `sound.liken.sh/supportsSound` and a kind attribute, beside the
  controllers it already publishes.
* Backs the bus socket with a hostPath instead of an emptyDir. The
  path does not change: plan 01 put the socket at
  `/var/run/bluetooth.liken.sh/dbus/system_bus_socket` so that this
  change would replace a volume and not an address.
* Writes the CDI file that delivers the mount and
  `DBUS_SYSTEM_BUS_ADDRESS` to the claim holder.
* Stays out of workload classes with the media bus. This landed
  ahead of the milestone, with the device enrichment of plan 61:
  the consumer class is the cluster owner's, and the manual's
  example, `bluetooth-input`, selects the `input` attribute, so it
  never matches the bus. The old `bluetooth-controller` class,
  which matched everything the driver publishes, is gone.
* Keeps everything it owns today: the adapter claim, `bluetoothd`, the
  pairing API of its plan 04, the bond Secrets, and the `input`
  devices. It never runs PipeWire. This design supersedes its plan 01,
  but the source reading in that plan's "How A2DP works" section
  remains the citation record.

### The audio operator

* Selects on the shared attribute. This landed with plan 61's
  enrichment release: the `sound-card` class names
  `sound.liken.sh/supportsSound` and no driver, and the claim
  template keeps `allocationMode: All`. The deploy base ships the
  class, because the operator's own claim names it, so the media
  bus joins the claim with no further class change anywhere.
* Enables WirePlumber's Bluetooth monitor when a claim delivers a bus,
  pointed at the delivered `DBUS_SYSTEM_BUS_ADDRESS`. The ALSA side is
  untouched on machines with no radio.
* Publishes each paired speaker as a sink device under
  `audio.liken.sh`: named by peer MAC, with the device name BlueZ
  reports, the codec, and the sink's node name. A paired speaker
  publishes even while switched off, tainted, so a consumer can claim
  ahead of the connect and park. That is milestone 56's deferred
  allocation.
* Delivers to consumers exactly what it delivers today. Nothing about
  the consumer contract changes.

## The orchestration flow

This section traces one machine from boot to a playing speaker, in
the order the objects act. The diagram shows the publish, claim, and
prepare chain; the stages after it follow the same order.

```
liken, driver liken.sh          Bluetooth operator, bluetooth.liken.sh
  ResourceSlice                   ResourceSlice
    hda-controller                  controllers (paired devices)
      supportsSound: true           hci0-media, exclusive
        |                             supportsSound: true
        |                               |
        +--------------+----------------+
                       |
                       v
        the audio pod's ResourceClaim
          class CEL: supportsSound == true, no driver named
          allocationMode: All
                       |
        kubelet: NodePrepareResources, one call per driver
           |                          |
           v                          v
  liken.sh plugin delivers    bluetooth.liken.sh plugin delivers
  the card's device nodes     /var/run/bluetooth.liken.sh/dbus
                              DBUS_SYSTEM_BUS_ADDRESS
                       |
                       v
        the audio pod: pipewire, wireplumber, operator
          wireplumber registers the endpoint over the bus
          the operator publishes sinks under audio.liken.sh
                       |
                       v
        a consumer's ResourceClaim, class audio-output
          CDI delivery: /var/run/audio.liken.sh,
          PIPEWIRE_REMOTE, PIPEWIRE_NODE
```

### Before a speaker exists

liken publishes each node's raw hardware under `liken.sh`: the HDA
controller, stamped `sound.liken.sh/supportsSound: true`, and the
`btusb` adapter.

The Bluetooth pod claims the adapter through the `bluetooth-adapter`
class. `bluetoothd` serves the bus at
`/var/run/bluetooth.liken.sh/dbus/system_bus_socket`, on a hostPath.
The pod's DRA plugin publishes the `bluetooth.liken.sh` slice: the
paired controllers, plus the media bus, one per adapter, exclusive,
stamped `sound.liken.sh/supportsSound: true`.

The audio pod's claim template keeps `allocationMode: All`, and its
class selects `supportsSound` with no driver named. The scheduler
allocates the card and the media bus into one claim. A node with
neither parks the pod `Pending`.

The kubelet calls `NodePrepareResources` once per driver in the
claim. liken's plugin delivers the card's device nodes. The
Bluetooth plugin's CDI spec delivers the bus mount and
`DBUS_SYSTEM_BUS_ADDRESS`.

The audio pod then starts in its container order: `declare` writes
the PipeWire configuration and enables the Bluetooth monitor because
the claim delivered a bus, `pipewire` serves its socket in
`/var/run/audio.liken.sh`, and `wireplumber` registers the media
endpoint with `bluetoothd`. From that registration on, A2DP is
advertised on the radio.

### Pairing

1. A person creates a `PairingRequest` that names the `Adapter`. Its
   phase starts at `Open`.
2. The Bluetooth operator opens the discoverable window over the
   bus. The speaker can pair because the endpoint is registered.
   When no audio pod holds the bus, the pairing API reports the bus
   unclaimed.
3. On success, the operator creates a `Pairing` owned by the
   `Adapter`, writes the link key into the adapter's bond `Secret`,
   and sets the `PairingRequest` phase to `Paired`.
4. The audio operator publishes the speaker in its `audio.liken.sh`
   slice: a sink named by the peer MAC, with the BlueZ device name,
   the codec, and the node name, tainted while the speaker is
   disconnected.

### A consumer plays

1. A workload's claim uses the `audio-output` class and selects the
   speaker. While the speaker is off, the taint excludes the device,
   and the pod parks `Pending`.
2. The speaker switches on, `bluetoothd` reports it over the bus,
   `wireplumber` builds the sink node, and the audio operator
   removes the taint.
3. The scheduler allocates the claim, and the `audio.liken.sh`
   plugin's CDI spec delivers the `/var/run/audio.liken.sh` mount,
   `PIPEWIRE_REMOTE`, and `PIPEWIRE_NODE`.
4. The consumer connects to the socket and plays into the named
   node. The transport descriptor and the L2CAP stream run below
   this layer, and no orchestration object touches them.

The attribute acts at two moments only: the two stamp sites, and the
class selection at scheduling time. Pairing and consumption never
read it, because a consumer selects `audio.liken.sh` devices and
never the shared attribute.

## Why one claim can span two drivers

The design depends on three allocator facts, read from Kubernetes 1.36,
the release these operators pin, in
`staging/src/k8s.io/dynamic-resource-allocation/structured/internal/stable/allocator_stable.go`:

1. An `All` request collects every selectable device from every pool
   that targets the node, across drivers; each allocated device
   has its own driver name. The API comment's phrase "in a pool"
   is loose; the code has no per-driver pin.
2. An `All` request with zero matching devices fails allocation, so
   the pod parks Pending. The at-least-one placement rule survives the
   class change.
3. An `All` request fails while any pool that targets the node is
   incomplete or invalid, even a pool with no matching devices, and
   the scheduler retries. Every pool in this fleet is one slice, so
   the failure window is one slice rewrite. But it is a new way for
   the audio pod to wait on an unrelated operator.

## The costs

**A Bluetooth pod restart still restarts the audio pod.** PipeWire's
`bluez5` plugin survives a `bluetoothd` restart, but it has no
reconnect path after the bus daemon itself exits, and the bus daemon
runs in the Bluetooth pod. So a Bluetooth pod restart ends Bluetooth
audio, and the audio pod must restart to get it back, which also
interrupts HDMI audio on that machine. A restart of WirePlumber
alone, with the PipeWire daemon and its ALSA sinks left up, may
repair it; drill 7 answers that.

**A new adapter takes a manual pod roll.** A claim is allocated when
the pod schedules, and an `All` allocation does not grow afterward. A
dongle plugged into a machine whose audio pod already runs publishes a
new `sound.liken.sh/supportsSound` device that the running pod's claim does
not include. A person
deletes the pod, and the replacement allocates both. A hot-plugged USB
sound card has the same limit today. These machines have fixed
hardware almost always, so this stays manual.

**Pairing a speaker requires the audio pod.** The endpoint
registration is what makes A2DP pairable, so pairing works only while
the audio operator holds the bus. The Bluetooth operator's pairing
API can report that plainly when the bus is unclaimed.

**A2DP only.** HFP and HSP add a microphone, and the sound server
itself must open their SCO sockets in the host network namespace.
That would put `hostNetwork` on an operator whose plan states its
privilege is none. The headset profiles stay out of scope until a
real use appears, and the answer may be different then.

## What was considered and set aside

* **PipeWire in the Bluetooth pod**, plan 01 of that operator. It
  costs two graphs and two sockets on one machine, a consumer that
  cannot reach a monitor's speakers and a Bluetooth speaker at once,
  and an audio fault that disconnects every controller. Its fallback,
  a third stacked operator, costs a third repository, a third image
  that holds PipeWire, and a third pod, to publish what this design
  publishes with none of them.
* **The audio operator mounts the Bluetooth pod's bus directly.** At
  the socket this is identical, but nothing declares it: nothing
  places the audio operator on the machine with the radio, nothing
  arbitrates a second mounter, and the coupling is in a volume
  instead of the API. The claim states the same dependency where the
  scheduler and a reader can read it.
* **A DeviceClass that ORs the two drivers by name.** It works, but it
  hard-codes each producer into the consumer's class. The shared
  attribute lets a future driver join by stamping one field,
  which is why milestone 59 chose the same form for monitors.

## What a drill must show

The drill runs on liken-1, with the radio, at least one controller,
and a real A2DP speaker.

1. **The claim spans the drivers.** The audio pod on liken-1 allocates
   the HDA controller and the media bus in one claim. A guest with a
   card and no radio still runs the pod with one allocated device.
2. **A2DP tracks the claim.** The A2DP Source record is present in the
   SDP exactly while the audio pod holds the bus. Observe this on the
   machine; do not take it from the sources.
3. **Pairing end to end.** A speaker pairs through the Bluetooth
   operator's pairing API while the audio pod is registered, and the
   sink appears under `audio.liken.sh` with its codec and node name.
4. **Claim ahead of connect.** With the speaker off, a consumer pod
   that claims it parks. When the speaker switches on, the pod
   starts.
5. **The consumer contract holds.** A pod plays into that speaker and
   no other, using only `PIPEWIRE_REMOTE` and `PIPEWIRE_NODE`.
6. **The mixed claim.** One pod claims an HDMI output and the
   Bluetooth speaker, receives one socket and two node names, and
   plays into both.
7. **The cost of the restart coupling.** Restart the Bluetooth pod
   with a stream live. Record what the audio side needs to recover, a
   WirePlumber restart alone or the whole pod, and what happens to
   HDMI playback either way.
8. **Coexistence on one radio.** A controller and the speaker run on
   the one adapter. Record input latency and audio dropouts, each
   with and without the other active, and repeat with the local Wi-Fi
   busy. Plan 01 argues from the protocol that A2DP does not take the
   bandwidth an HID link needs, but that argument has no measurement.
   This drill supplies it.

## Open questions

* **Whether WirePlumber alone can restart.** If the Bluetooth nodes
  are in WirePlumber and the ALSA sinks are in the PipeWire daemon,
  a WirePlumber-only restart may repair a lost bus without an
  interruption to HDMI playback. Drill 7 answers it, and the answer
  sets the real cost of the restart coupling.
* **Where the headset profiles land** if a microphone use ever
  arrives: `hostNetwork` on the audio operator, or a narrower place.
