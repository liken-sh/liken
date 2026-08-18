# Bluetooth audio

A Bluetooth speaker publishes as an ordinary `audio.liken.sh` sink,
from the same operator and the same PipeWire graph that serve the HDMI
outputs and the analog jack. The Bluetooth operator owns reaching the
speaker: the radio, the pairing, and the bonds. The audio operator
owns playing into it. Exactly one thing crosses between them, and it
crosses in public: the Bluetooth operator publishes its media bus as a
DRA device, and the audio operator claims it.

This milestone spans three repositories. This document is the design
record; each operator's own plans directory records its half of the
build.

## The problem

A paired Bluetooth speaker leaves nothing in the kernel. There is no
sysfs node and no ALSA card. The audio exists only while a sound
server is a live party on `bluetoothd`'s D-Bus socket: it must
register a media endpoint, negotiate the codec, encode the samples
itself, and hold the L2CAP socket that `bluetoothd` hands over as a
file descriptor. BlueZ does not advertise A2DP at all until an
endpoint registers, so a `bluetoothd` with no sound server beside it
cannot even pair a speaker usefully. The Bluetooth operator's plan 01
read all of this from the upstream sources and its citations stand;
this document does not repeat them.

That fact splits Bluetooth audio across two operators. The pairing
state lives in `bluetoothd`, which milestone 58's operator runs. The
sound server, the graph, and the sink contract live in milestone 59's
audio operator. Plan 01 of the Bluetooth operator resolved the split
by putting a second PipeWire in the Bluetooth pod and publishing
speakers under `bluetooth.liken.sh`. That design carries three costs:
two sound stacks on one machine, two sockets where `PIPEWIRE_REMOTE`
can name only one, and a fallback that adds a third operator if an
audio fault restarting the controllers proves unacceptable. This
milestone replaces that design.

## The design: the media bus is a device

"May do Bluetooth audio on this machine" is exactly equal to "may talk
on `bluetoothd`'s socket". The Bluetooth operator makes that right
claimable. It publishes one device per adapter, the media bus, in the
ResourceSlice it already writes. The device is exclusive, because one
radio supports one sound server: two endpoints registering on one
`bluetoothd` have no contract over the streams. The claim's CDI
delivery is a mount of the bus socket's directory and
`DBUS_SYSTEM_BUS_ADDRESS` pointing into it, which is the standard
variable PipeWire reads.

The audio operator claims it through an attribute both drivers stamp,
in a domain neither owns, the same move milestone 59 made with
`monitor.liken.sh/id`:

```yaml
# liken stamps it on the HDA controller, the Bluetooth operator stamps
# it on the media bus. The audio operator's class selects the
# attribute, not the driver.
attributes:
  sound.liken.sh/substrate: {bool: true}
```

The audio operator's DeviceClass changes from "liken's audio
controller" to "any sound substrate", and its ResourceClaimTemplate
keeps `allocationMode: All`. Each DaemonSet pod then allocates
everything on its node that can make sound: the card, the radio's
media bus, or both. A node with neither parks the pod Pending, which
is the placement rule the operator already relies on.

With the bus delivered, the audio operator enables WirePlumber's
Bluetooth monitor. The monitor registers the media endpoint with
`bluetoothd`, so A2DP is advertised, and speakers become pairable,
exactly while the audio pod holds the claim. When a speaker connects,
`bluetoothd` hands the transport descriptor over the bus, WirePlumber
builds the sink node, and the sink lands in the one graph beside the
HDMI sinks. The operator publishes it under `audio.liken.sh`, named by
the peer MAC the way the Bluetooth operator names its devices, and
tainted by milestone 56's rules while the speaker is disconnected. A
consumer claims it like any other sink and receives the same delivery:
the `/var/run/audio.liken.sh` mount, `PIPEWIRE_REMOTE`, and
`PIPEWIRE_NODE`.

One graph and one socket close plan 01's open mixed-claim question: a
pod that must reach an HDMI output and a Bluetooth speaker at once
names one socket and two nodes.

No privilege moves. The audio operator keeps none: A2DP needs no
`AF_BLUETOOTH` socket in the sound server's pod, because `bluetoothd`
created the L2CAP socket in the initial network namespace, a socket's
namespace is fixed at creation, and read, write, and `shutdown` on an
existing socket do no namespace check. The descriptor crosses the pod
boundary by `SCM_RIGHTS` over the mounted bus socket. Plan 01 recorded
both facts with sources when it set aside the cross-pod mount; this
design is that mount, made legible to the scheduler.

## Who does what

### liken, this repository

The machine operator stamps `sound.liken.sh/substrate: true` on the
audio controller device it already publishes, in
`machine-operator/publishing.go`. That is the whole change here. The
OS still publishes raw hardware and nothing refined.

### The Bluetooth operator

* Publishes the media bus device, one per adapter, exclusive, carrying
  `sound.liken.sh/substrate` and a kind attribute beside the
  controllers it already publishes.
* Backs the bus socket with a hostPath instead of an emptyDir. The
  path does not change: plan 01 parked the socket at
  `/var/run/bluetooth.liken.sh/dbus/system_bus_socket` so that this
  move would change a volume and not an address.
* Writes the CDI file that delivers the mount and
  `DBUS_SYSTEM_BUS_ADDRESS` to the claim holder.
* Keeps everything it owns today: the adapter claim, `bluetoothd`, the
  pairing API of its plan 04, the bond Secrets, and the `input`
  devices. It never runs PipeWire, and its plan 01 is superseded by
  this design; the source reading in that plan's "How A2DP works"
  section remains the citation record.

### The audio operator

* Selects on the shared attribute: the class stops naming
  `device.driver == "liken.sh"` and starts naming the substrate
  attribute. The claim template keeps `allocationMode: All`.
* Enables WirePlumber's Bluetooth monitor when a claim delivers a bus,
  pointed at the delivered `DBUS_SYSTEM_BUS_ADDRESS`. The ALSA side is
  untouched on machines with no radio.
* Publishes each paired speaker as a sink device under
  `audio.liken.sh`: named by peer MAC, carrying the device name BlueZ
  reports, the codec, and the sink's node name. A paired speaker
  publishes even while switched off, tainted, so a consumer can claim
  ahead of the connect and park, which is milestone 56's deferred
  allocation.
* Delivers to consumers exactly what it delivers today. Nothing about
  the consumer contract changes.

## Why one claim can span two drivers

The design leans on three allocator facts, read from Kubernetes 1.36,
the release these operators pin, in
`staging/src/k8s.io/dynamic-resource-allocation/structured/internal/stable/allocator_stable.go`:

1. An `All` request collects every selectable device from every pool
   that targets the node, across drivers; each allocated device
   carries its own driver name. The API comment's phrase "in a pool"
   is loose; the code has no per-driver pin.
2. An `All` request with zero matching devices fails allocation, so
   the pod parks Pending. The at-least-one placement rule survives the
   class change.
3. An `All` request fails while any pool targeting the node is
   incomplete or invalid, even a pool with no matching devices, and
   the scheduler retries. Every pool in this fleet is one slice, so
   the window is a slice rewrite, but it is a new way for the audio
   pod to wait on an unrelated operator.

## The costs

**A Bluetooth pod restart still restarts the audio pod.** PipeWire's
`bluez5` plugin survives a `bluetoothd` restart, but it has no
reconnect path after the bus daemon itself goes away, and the bus
daemon lives in the Bluetooth pod. So a Bluetooth pod restart ends
Bluetooth audio and forces the audio pod to restart to get it back,
which also interrupts HDMI audio on that machine. Whether restarting
only WirePlumber repairs it, leaving the PipeWire daemon and its ALSA
sinks up, is an open question the drill answers.

**A new adapter takes a manual pod roll.** A claim is allocated when
the pod schedules, and an `All` allocation does not grow afterward. A
dongle plugged into a machine whose audio pod already runs publishes a
new substrate that the running pod's claim does not include; a person
deletes the pod and the replacement allocates both. A hot-plugged USB
sound card has the same shape today. These machines carry fixed
hardware almost always, so this stays manual.

**Pairing a speaker requires the audio pod.** The endpoint
registration is what makes A2DP pairable, so the pairing UX works only
while the audio operator holds the bus. The Bluetooth operator's
pairing API can report that plainly when the bus is unclaimed.

**A2DP only.** HFP and HSP add a microphone, and their SCO sockets
must be opened by the sound server itself in the host network
namespace. That would push `hostNetwork` onto an operator whose plan
states its privilege is none. The headset profiles stay out of scope
until a real use asks, and the answer may be different then.

## What was considered and set aside

* **PipeWire in the Bluetooth pod**, plan 01 of that operator. Two
  graphs and two sockets on one machine, a consumer that cannot reach
  a monitor's speakers and a Bluetooth speaker at once, and an audio
  fault that disconnects every controller. Its fallback, a third
  stacked operator, costs a third repository, a third image carrying
  PipeWire, and a third pod, to publish what this design publishes
  with none of them.
* **The audio operator mounts the Bluetooth pod's bus directly.**
  Technically identical at the socket, but invisible: nothing places
  the audio operator on the machine with the radio, nothing arbitrates
  a second mounter, and the coupling hides in a volume instead of
  standing in the API. The claim states the same dependency where the
  scheduler and a reader can see it.
* **A DeviceClass that ORs the two drivers by name.** It works, but it
  hard-codes each producer into the consumer's class. The shared
  attribute lets a future substrate join by stamping one field,
  which is why milestone 59 chose the same form for monitors.

## What a drill must show

The drill runs on liken-1, with the radio, at least one controller,
and a real A2DP speaker.

1. **The claim spans the drivers.** The audio pod on liken-1 allocates
   the HDA controller and the media bus in one claim. A guest with a
   card and no radio still runs the pod with one allocated device.
2. **A2DP tracks the claim.** The A2DP Source record is present in the
   SDP exactly while the audio pod holds the bus, observed rather than
   read from the sources.
3. **Pairing end to end.** A speaker pairs through the Bluetooth
   operator's pairing API while the audio pod is registered, and the
   sink appears under `audio.liken.sh` with its codec and node name.
4. **Claim ahead of connect.** With the speaker off, a consumer pod
   claiming it parks; switching the speaker on starts the pod.
5. **The consumer contract holds.** A pod plays into that speaker and
   no other, using only `PIPEWIRE_REMOTE` and `PIPEWIRE_NODE`.
6. **The mixed claim.** One pod claims an HDMI output and the
   Bluetooth speaker, receives one socket and two node names, and
   plays into both.
7. **The restart coupling, priced.** Restart the Bluetooth pod with a
   stream live. Record what the audio side needs to recover: a
   WirePlumber restart alone, or the whole pod, and what HDMI
   playback experiences either way.
8. **Coexistence on one radio.** A controller and the speaker run on
   the one adapter. Record input latency and audio dropouts, each with
   and without the other active, and repeat with the house's Wi-Fi
   busy. Plan 01's protocol argument says A2DP does not starve an HID
   link; this is the measurement that argument has no source for.

## Open questions

* **Whether WirePlumber alone can restart.** If the Bluetooth nodes
  live in WirePlumber and the ALSA sinks live in the PipeWire daemon,
  a WirePlumber-only restart may repair a lost bus without ending HDMI
  playback. Drill 7 answers it, and the answer decides how bad the
  restart coupling really is.
* **Where the headset profiles land** if a microphone use ever
  arrives: `hostNetwork` on the audio operator, or a narrower home.
