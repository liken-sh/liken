# Bluetooth audio

Plan 01, Proposed. It would publish each paired Bluetooth speaker as
its own DRA device, so a pod claims one speaker by its MAC address and
receives a PipeWire socket and the name of the sink its streams must
reach. The operator already claims the adapter and runs `bluetoothd`.
This plan adds PipeWire and WirePlumber to the same pod, on the same
private bus, and adds a second kind of device to the slices the
operator already writes.

Everything in the "How A2DP works" section and the "The privilege does
not change" section was read from the upstream sources named there.
None of it was run. Every claim about behavior on real hardware is in
"What a drill must show", and none of it is asserted here.

## The problem

A Bluetooth speaker is the same kind of thing as a Bluetooth
controller, and milestone 58's argument covers it unchanged. The
pairing state is in `bluetoothd`. It is not in sysfs
and it is not on the Machine. So the layer that publishes speakers
must be the layer that runs `bluetoothd`, and that layer is this
operator.

One fact makes the speaker case stronger than the controller case.
BlueZ advertises no A2DP at all until a media endpoint registers with
it. `endpoint_init_a2dp_source` calls `a2dp_add_sep`
([`profiles/audio/media.c:781`](https://github.com/bluez/bluez/blob/master/profiles/audio/media.c)),
and that call puts an AVDTP stream endpoint and its SDP record in
place. A `bluetoothd` with no sound server beside it has no A2DP
Source record, so a speaker cannot connect to it and a person cannot
usefully pair one. The daemon and the endpoint have to be together, so
the pairing UX this operator already owns and the audio path are one
question, not two.

liken publishes the machine's HDA controller as its own device, and
[milestone 59](https://github.com/liken-sh/liken/blob/main/plans/completed/59-the-audio-operator.md)
publishes each of its physical outputs under `audio.liken.sh`. A
Bluetooth speaker is not on that card. It is on this radio.

## What the pod runs and what it holds

The pod runs four daemons: the bus daemon, `bluetoothd`, PipeWire, and
WirePlumber. The first two are already here, in the `bluetoothd`
sidecar, where the bus daemon starts first and `bluetoothd` becomes the
container's process. PipeWire and WirePlumber join that container,
because the A2DP transport is a file descriptor `bluetoothd` hands over
its own bus and the sound server has to be a client of that bus.

Adding them to the sidecar puts all four daemons on one restart domain,
which is the rule the two already follow: a daemon that dies ends the
container, and the kubelet's restart is the repair. The operator stays
in its own container with no capabilities, and the restart of the
sidecar beside it is a bus that goes away and comes back, which the
operator already handles by republishing its devices fully tainted.

The bus is the one this operator already runs. Its socket is at
`/var/run/bluetooth.liken.sh/dbus/system_bus_socket`, on an emptyDir
the two containers share. That location is deliberate. It costs
nothing today, because only this pod's own processes use the bus, and
it keeps the two stacked designs in "What was considered and set
aside" available later without a breaking change to a published
path. Reaching the bus from a second pod would mean backing that same
path with a hostPath instead, which is a change of volume and not a
change of address.

PipeWire's runtime directory is `/var/run/bluetooth.liken.sh`, a
hostPath, so the operator's CDI files can mount the socket into a
consumer's container on the same node. The socket is
`/var/run/bluetooth.liken.sh/pipewire-0`. This mirrors the audio
operator, which puts its own socket at
`/var/run/audio.liken.sh/pipewire-0` for the same reason.

WirePlumber runs with a profile that disables the monitors this pod
does not own. The profile syntax is
[`src/config/wireplumber.conf`](https://github.com/PipeWire/wireplumber/blob/master/src/config/wireplumber.conf),
where each feature takes `required`, `optional`, or `disabled`, and
`main-embedded` is the systemwide stateless profile that matches these
pods.
This pod sets `hardware.audio = disabled`, because it claims no sound
card and must not look for one.

## How A2DP works

Four facts govern the design. Each one is from a source that is named
and linked.

**One process holds the bus connection, the transport file descriptor,
and the encoder.** The `bluez5` SPA plugin takes a private connection
to the D-Bus system bus
([`spa/plugins/bluez5/bluez5-dbus.c:7783`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/bluez5/bluez5-dbus.c),
`spa_dbus_get_connection(this->dbus, SPA_DBUS_TYPE_SYSTEM)`) and
registers its media application with `RegisterApplication`
(`bluez5-dbus.c:6347`), which exports the `org.bluez.MediaEndpoint1`
objects BlueZ calls back into. When the plugin creates a node for a
transport, it passes the transport as an address in the same process:
`snprintf(transport, sizeof(transport), "pointer:%p", t)` under the key
`api.bluez5.transport`
([`bluez5-device.c:733`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/bluez5/bluez5-device.c)).
A pointer does not cross a process boundary, so the media node has to
be built where the monitor runs. WirePlumber builds it there:
`LocalNode("adapter", properties)`
([`src/scripts/monitors/bluez/create-node.lua:42`](https://github.com/PipeWire/wireplumber/blob/master/src/scripts/monitors/bluez/create-node.lua)),
which creates the node in WirePlumber's own process and exports it to
the PipeWire daemon. The ALSA monitor does the opposite,
`Node("adapter", props)`
([`src/scripts/monitors/alsa.lua:83`](https://github.com/PipeWire/wireplumber/blob/master/src/scripts/monitors/alsa.lua)),
which creates the node in the daemon. So ALSA playback runs in
`pipewire` and Bluetooth playback runs in `wireplumber`. That
asymmetry is why the Bluetooth audio stack cannot be split at any line
except the D-Bus one.

**The transport arrives as a file descriptor over the bus.**
`org.bluez.MediaTransport1.Acquire` returns it, and the plugin parses
the reply with `DBUS_TYPE_UNIX_FD, &transport->fd`
(`bluez5-dbus.c:4307`). The descriptor is the L2CAP socket that
`bluetoothd` opened. `bluez5-dbus.c` opens no socket of its own.

**Encoding happens in PipeWire.** `media-sink.c` calls
`this->codec->encode(...)` at lines 806 and 841
([`spa/plugins/bluez5/media-sink.c`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/bluez5/media-sink.c)),
and the codecs are in the same directory: `a2dp-codec-sbc.c`,
`a2dp-codec-aac.c`, `a2dp-codec-aptx.c`, `a2dp-codec-ldac.c`, and the
rest. PipeWire sets two options on the transport socket, `SO_SNDBUF`
(`media-sink.c:1602`) and `SO_PRIORITY` with the value 6
(`media-sink.c:1614`). Six is the highest priority the kernel gives a
process with no capability, and the call is warn-only if it fails.

**The samples do not pass through `bluetoothd`.** After `Acquire`, the
path is client pod, then the PipeWire daemon, then WirePlumber, then
`write()` on the L2CAP socket, then the kernel. `bluetoothd` holds the
control path: AVDTP signalling, `SetConfiguration`, `Volume`, and
AVRCP. This does not make the stream independent of `bluetoothd`.
The plugin watches `org.bluez` on the bus, and when the name loses its
owner it frees every transport, endpoint, device, and adapter
(`bluez5-dbus.c:6889`), and freeing a transport calls `shutdown()` and
`close()` on the descriptor (`bluez5-dbus.c:3498`). So a `bluetoothd`
that exits ends the stream through the control path, and a
`bluetoothd` that is merely slow does not glitch the audio. The same
handler re-lists and re-registers when a new owner appears, so a
`bluetoothd` restart inside this pod is a recoverable event for the
audio side.

**BlueZ's D-Bus policy permits the calls in both directions.**
[`src/bluetooth.conf`](https://github.com/bluez/bluez/blob/master/src/bluetooth.conf)
gives `root` `own="org.bluez"`, `send_destination="org.bluez"`, and
`send_interface="org.bluez.MediaEndpoint1"` and `"org.bluez.Profile1"`,
which are the interfaces `bluetoothd` calls back on. Every process in
this pod runs as root, so nothing in the policy has to change.

## One device for each paired sink

The operator publishes two kinds of device under one driver name,
`bluetooth.liken.sh`, and an attribute states which kind a device is.
The proposal is `bluetooth.liken.sh/kind`, with the values `input` and
`audio-sink`.

* `kind: input` is a paired controller. Milestone 58 defines it and
  nothing here changes it.
* `kind: audio-sink` is a paired A2DP sink.

Both are named the same way, which is the peer MAC address in
lowercase with dashes for the colons, because a device name must be a
DNS label. Both publish the unmodified MAC as an attribute. A speaker
and a controller have different addresses, so the two kinds share a
name space and never collide in it.

Two DeviceClasses keep the kinds apart. A claim that asks for a
gamepad must not be able to allocate a speaker, because the two
deliver different things, and a claim that allocated the wrong kind
would fail inside the consumer's container rather than in the
scheduler. The classes select on `bluetooth.liken.sh/kind`.

The discovery path is different for the two kinds, and this is the
part of the operator that grows. A controller is found by walking
`/sys/bus/hid/devices` and keeping the entries with bus type `0005`. A
speaker has no sysfs node at all. It is a BlueZ device that reports
the A2DP Sink UUID, with an `org.bluez.MediaTransport1` object when it is
connected, and a PipeWire node when the graph holds one. So the audio
half reads D-Bus and the PipeWire graph, and it never reads sysfs.

The attributes on an `audio-sink` device come from BlueZ and from the
graph:

* the peer MAC address, unmodified,
* the device's name as BlueZ reports it,
* the codec in use,
* the PipeWire node name of the sink, which the consumer's
  environment holds.

A paired speaker publishes whether or not it is switched on, the same
as a paired controller, which milestone 58's "Paired, not
connected" section already states and what milestone 56's deferred
allocation makes useful. A speaker that disconnects takes milestone
56's taint path. An `audio-sink` is untainted only while BlueZ
reports the device connected **and** the graph holds a sink node for
it; losing either one applies the taints. The reason is the one
milestone 58 gives for controllers: a session can be up and mute, and
the taint tracks what a claim can deliver.

## The delivery is a socket, not a device node

An `input` device delivers evdev nodes. An `audio-sink` device
delivers no device node at all. It delivers what milestone 59
delivers, in the same form, from a different socket:

* a mount of `/var/run/bluetooth.liken.sh`,
* `PIPEWIRE_REMOTE=/var/run/bluetooth.liken.sh/pipewire-0`,
* `PIPEWIRE_NODE=<the sink's node.name>`.

An absolute `PIPEWIRE_REMOTE` is used as a path and the runtime
directory is not consulted, and `PIPEWIRE_NODE` sets `target.object` on
every stream. Milestone 59 verified both against the PipeWire sources
and this plan takes them unchanged. A consumer that plays through the
PipeWire stream API needs nothing else, and a consumer that plays
through the PulseAudio protocol or the ALSA compatibility plugin
selects its sink another way, which neither plan states.

**One container holds one audio sink claim.** `PIPEWIRE_REMOTE` and
`PIPEWIRE_NODE` are single-valued. Two audio claims in one container
both write them, the container runtime applies CDI environment entries
in order, and the second one wins with no error anywhere. The limit is
the same one milestone 59 has inside a single operator, and it gets
easier to meet by accident here, because a claim on a speaker and a
claim on a monitor's HDMI output name two different sockets. A pod
that must reach both an HDMI output and a Bluetooth speaker is not
served by this design, and the second entry in "Open questions" is
where that case would be answered.

## The privilege does not change

The pod already declares `hostNetwork` and adds `NET_ADMIN`, and the
audio path adds nothing to either. It also adds no capability of its
own. What it adds is one hostPath mount, for the PipeWire socket
directory, beside the two that every DRA driver takes.

The reason `hostNetwork` was already the right call is worth writing
down, because it sets where a Bluetooth sound server can run at all.

* **A2DP needs no `AF_BLUETOOTH` socket.** `bluetoothd` opens the
  L2CAP socket and PipeWire receives the descriptor, so
  `bt_sock_create` never runs on the PipeWire side. That is the check
  at
  [`net/bluetooth/af_bluetooth.c:118`](https://github.com/torvalds/linux/blob/master/net/bluetooth/af_bluetooth.c),
  `if (net != &init_net) return -EAFNOSUPPORT;`, which is the failure
  milestone 58 records.
* **HFP and HSP do need one.** The native backend opens SCO sockets
  itself:
  `socket(PF_BLUETOOTH, SOCK_SEQPACKET | SOCK_CLOEXEC | SOCK_NONBLOCK, BTPROTO_SCO)`
  at
  [`spa/plugins/bluez5/backend-native.c:2622`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/bluez5/backend-native.c)
  for an outgoing connection and at line 3080 for the listening
  socket. It binds to the adapter address, sets `BT_VOICE` for mSBC and
  LC3, and sets `BT_DEFER_SETUP` on the listener. It takes the RFCOMM
  control channel from `bluetoothd` through `Profile1`, which it
  registers with `ProfileManager1.RegisterProfile` at line 3755.
* **The mSBC probe opens a raw HCI socket.**
  [`spa/plugins/bluez5/hci.c`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/bluez5/hci.c)
  does `socket(AF_BLUETOOTH, SOCK_RAW | SOCK_CLOEXEC, BTPROTO_HCI)`,
  binds to `hciN`, and sends Read Local Extended Features. It needs no
  capability. Binding `HCI_CHANNEL_RAW` has no `capable()` check in
  [`net/bluetooth/hci_sock.c`](https://github.com/torvalds/linux/blob/master/net/bluetooth/hci_sock.c),
  and Read Local Extended Features is OGF 4 OCF 4, which the
  `hci_sec_filter` whitelist at `hci_sock.c:138` permits, so the
  `!capable(CAP_NET_RAW)` reject at `hci_sock.c:1885` is not reached.

So the headset profiles need the network namespace and no capability.
A design that put PipeWire anywhere except a pod in the host network
namespace would lose HFP, and this one does not have to choose.

## What a restart takes down

The pod is one restart domain. That has a cost.

`bluetoothd` owns the HID sessions, and killing it disconnects every
controller at once, which the lab proved. Adding audio means it also
ends every audio stream, through the control path described above.
That is true of every design in "What was considered and set aside",
including the ones that put PipeWire in a separate pod, so it is not a
cost of this choice.

The cost of this choice is the reverse. A
PipeWire or WirePlumber fault now restarts the pod, and that
disconnects every controller. Before this plan, an audio fault could
not do that, because there was no audio in the pod.

The size of that cost is unknown and the drill measures it. If it is
unacceptable, option C in the next section is the fallback, and
moving to it changes how the operator is packaged and not what it
publishes.

Two smaller cases are settled already. A `bluetoothd` restart inside
the pod is survivable for the audio side without a pod restart, by the
`NameOwnerChanged` handler at `bluez5-dbus.c:6889`. A bus daemon
restart is not survivable: the PipeWire D-Bus support plugin filters
the `Disconnected` signal and emits an event
([`spa/plugins/support/dbus.c:310`](https://github.com/PipeWire/pipewire/blob/master/spa/plugins/support/dbus.c)),
and `bluez5-dbus.c` registers no listener for it, so there is no
reconnect path. Inside one pod that does not matter, because a bus
daemon that exits ends the container anyway.

## What was considered and set aside

* **The audio operator mounts this pod's bus.** Put the bus socket on a
  hostPath, mount it into `audio.liken.sh`, and let one PipeWire graph
  hold the HDMI sinks, the analog jack, and the Bluetooth sinks
  together. It is technically workable, and the two facts that make it
  workable are worth keeping written down. `SCM_RIGHTS` copies a
  descriptor into the receiver's file table over any unix socket, and
  the mount origin of the socket file does not matter, because both
  ends hold one inode. The L2CAP socket was created by `bluetoothd` in
  the initial network namespace, and namespace membership is fixed at
  creation, so read, write, poll, `setsockopt`, and `shutdown` on an
  existing socket do no namespace check. A PipeWire in a pod with its
  own network namespace can therefore stream A2DP on a descriptor that
  `bluetoothd` handed it. Mounting the host's system bus socket into a
  container is also ordinary prior art, in
  [x11docker's D-Bus notes](https://github.com/mviereck/x11docker/wiki/How-to-connect-container-to-DBus-from-host),
  [kvaps/docker-pulseaudio-bluetooth](https://github.com/kvaps/docker-pulseaudio-bluetooth),
  and
  [Kryxan/Proxmox-Bluetooth](https://github.com/Kryxan/Proxmox-Bluetooth).
  It is set aside because of what it couples, not because of what it
  cannot do. Nothing schedules the audio operator onto the machine with
  the adapter, so on a machine where the card and the radio are apart
  the mount is empty and Bluetooth audio silently does not exist.
  Nothing orders the two pods. The bus daemon has no reconnect path, so
  restarting this pod would force a restart of the audio operator,
  which would end the audio on every monitor that has nothing to do
  with Bluetooth. And HFP would push `hostNetwork` onto an operator
  whose plan states that its privilege is none. Milestone 56 sets aside
  an exclusion list in liken because a switch outside the hardware must
  not describe the hardware. A hostPath from one operator into another
  is the same statement, made worse by being invisible to the
  scheduler.
* **A third operator, stacked on this one.** This operator republishes
  one more device of its own, for example `hci0-media`, exclusive,
  whose CDI delivery is a mount of the bus directory and
  `DBUS_SYSTEM_BUS_ADDRESS`. A `bluetooth-audio` operator claims it the
  way this one claims the raw adapter, runs PipeWire and WirePlumber
  against the delivered bus, and publishes the sinks under a driver of
  its own. It answers every objection above through the public
  contract instead of around it: placement, ordering, and exclusivity
  all come from the claim, and an audio fault then restarts only the
  audio pod and leaves the controllers running. It costs a third
  repository, a third image that ships PipeWire again, a third pod, and a
  published device whose whole meaning is permission to talk to another
  operator's daemon, which is not a thing `bluetoothd` holds. **This is
  the recorded fallback.** If the drill shows that an audio fault
  taking down the controllers is unacceptable, this is where the design
  goes, and the bus socket path in "What the pod runs and what it
  holds" is already where it needs to be for that move.
* **BlueALSA.** `bluez-alsa` is a daemon built for this split. It
  registers the media endpoints with `bluetoothd` and exposes the PCMs
  through its own D-Bus API and an ALSA plugin, and it is lighter than
  PipeWire with WirePlumber. Its client contract is worse for this
  fleet: a consumer's image would need the BlueALSA ALSA plugin and a
  bus mount, in place of one environment variable, and PipeWire is
  already the sound server that `audio.liken.sh` delivers.
* **A second WirePlumber on one graph.** Because Bluetooth nodes are
  `LocalNode` and ALSA nodes are not, a WirePlumber in this pod could
  hold the bus, the transport descriptors, and the encoders while
  exporting its Bluetooth nodes into a PipeWire daemon that runs in the
  audio operator's pod. That would give one graph, one socket, and one
  `PIPEWIRE_REMOTE` covering both the card and the radio, which is the
  only design here that serves a pod needing both. WirePlumber's
  profile system supports the shape, because a profile with
  `hardware.bluetooth` and without `policy.standard` or the metadata
  features avoids two session managers on one graph. It is set aside
  because of the dependency it creates. The audio operator's claim is
  the HDA controller, so a machine with a Bluetooth speaker and no
  sound card would run no audio operator, and Bluetooth audio would
  depend on hardware it does not use.

## What a drill must show

The drill runs against a real adapter, at least one real controller,
and a real A2DP speaker.

* **Pairing a speaker.** Pair a speaker through the operator's pod, the
  way a person pairs a controller today. Record whether the A2DP Source
  record is present only while PipeWire runs, which is the
  `a2dp_add_sep` behavior above, observed rather than read.
* **The sink publishes.** The speaker appears in a ResourceSlice under
  `bluetooth.liken.sh`, named by its MAC address, with
  `kind: audio-sink`, its codec, and its PipeWire node name. The
  pairing survives a restart of the operator's pod.
* **A claim ahead of the connect.** Switch the speaker off, create a
  pod that claims it, and the pod parks as Unschedulable. Switch the
  speaker on, and the pod starts.
* **A consumer plays.** A pod holding the claim plays into that
  speaker and no other, using only `PIPEWIRE_REMOTE` and
  `PIPEWIRE_NODE` from its environment.
* **A controller and a speaker at once.** Run both on the one adapter.
  Record input latency on the controller and dropouts on the audio,
  with and without the other one active. This is the measurement the
  design has no source for. The protocol argument is that one adapter
  is one piconet master scheduling slots across its links, that an SBC
  stream is a real fraction of the practical throughput and not close
  to all of it, and that the profile which reserves periodic slots and
  therefore starves an HID link is SCO, which is HFP and not A2DP. The
  drill turns that argument into a number.
* **2.4 GHz coexistence.** Repeat the previous test with the local
  Wi-Fi busy. Record the difference.
* **A PipeWire restart.** Kill PipeWire while a controller's consumer
  pod runs. This is the cost this plan accepts, and the drill prices
  it: record how long the controllers are gone and what their
  consumers do. The result sets whether the design stays here or moves
  to the stacked operator.
* **A `bluetoothd` restart with a stream live.** Restart `bluetoothd`
  while audio plays. The stream must end, even though no sample
  passes through `bluetoothd`, because the transport is freed and its
  descriptor closed when `org.bluez` loses its owner. Record whether
  the audio side recovers without a pod restart when the name comes
  back.
* **HFP, if it is in scope.** Connect a headset, and record whether SCO
  audio works in the pod's network namespace and what it does to a
  controller on the same adapter. Record whether the adapter needs a
  USB alternate setting change for SCO, which is a per-adapter question
  this plan does not answer.

## Open questions

* **Whether to serve the headset profiles at all.** A2DP covers a
  speaker and a pair of headphones for playback. HFP and HSP add a
  microphone, and they add the profile that reserves radio slots and is
  the likely cause of trouble for a controller on the same adapter. The
  privilege for them is already in place, so this is a question about
  what the operator offers, not about what it may do. The leaning is to
  ship A2DP first and to add the headset profiles only when a real use
  asks for them.
* **A shared attribute so one DeviceClass spans both drivers.** A
  Bluetooth speaker publishes under `bluetooth.liken.sh` and an HDMI
  output publishes under `audio.liken.sh`, so a consumer that can use
  any speaker reads two drivers. A DeviceClass selector can read
  `device.driver` and cover both, and milestone 59 already sets the
  precedent for the tidier form with `monitor.liken.sh/id`, a shared
  attribute in a domain that neither driver owns. A `sound.liken.sh`
  attribute on every sink, published by both operators, would let one
  class span the two. Adding it now costs one field on each device.
  Adding it later means republishing every slice. The decision is not
  this operator's alone, so it is open.
* **The mixed claim.** Nothing here serves a pod that must reach an
  HDMI output and a Bluetooth speaker at the same time, because the two
  name different sockets and `PIPEWIRE_REMOTE` is one variable. The
  single-graph variant above is the only answer found so far, and its
  dependency is wrong. Whether that case is real in this fleet is not
  yet known.
