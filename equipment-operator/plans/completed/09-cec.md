# The TV and the receiver over HDMI-CEC

Plan 09. Phase 1 built 2026-09-26 and tested against `vivid`. Phase 2
built 2026-09-26 and tested against `vivid`, with `cec-follower`
playing the TV. Phase 3 built 2026-09-26 and tested against `vivid`,
with a second `vivid` output playing a streaming player. The hardware
drills of phases 1 to 3 ran on `liken-1`. Phase 4 is not built: the
`Receiver`'s `cec:` block moved to
[plan 13](../13-every-setting-a-receiver-exposes.md#cec). Phase 5 was
built on 2026-10-04 as the guide
[Connect a USB-CEC adapter](../../docs/content/docs/guides/connect-a-cec-adapter.md). The power press
that turns the TV off was built 2026-09-27 and tested against `vivid`
and the fakes; see "The power press turns the room off". The adapter
stopped sending on a timer on 2026-09-27, tested against `vivid` and
the fakes; see "The adapter sends nothing on a timer". It depends on `liken` plan 70, which
attaches a USB CEC adapter and publishes it as a device, and on
display-operator plan 23, which publishes each `Display`'s CEC
physical address.

## The problem

A person presses the remote's power button. The receiver turns on and
selects the session's input, and the TV stays off. Plan 07 found the
reason: the receiver is a CEC responder, and CEC has no
receiver-to-TV power-on. A TV wakes only when a device connected to it
sends a CEC message. The `liken` machines have no CEC on their own HDMI
ports, so the cluster cannot wake a TV, cannot tell whether the TV is
on, and cannot switch the TV to a machine's input.

CEC reaches more than the wake. The TV, the receiver, and every
source on one HDMI tree share one CEC wire. A device on that wire can
read which devices are present, where each one is connected, and
whether each one is on. It can also switch the TV and the receiver to
an input. The TV's own remote sends its buttons over the same wire to
the source that is showing. This plan makes that wire a first-class
part of equipment-operator. It covers the bus, the TV, the receiver's
CEC side, and the remote.

The first adapter is the Pulse-Eight USB-CEC adapter
([product page](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter)).
The Linux kernel has a driver for it, `pulse8-cec`, and it presents
the adapter through the kernel's CEC framework. So this plan uses the
kernel's CEC API and does not use libCEC.

## CEC in brief

CEC is Supplement 1 of the HDMI specification. The HDMI 1.4b and 2.x
supplements are available only to HDMI adopters. HDMI 1.3a with its
CEC supplement is free from the
[HDMI specification page](https://www.hdmi.org/spec/index) after a
form. The kernel's
[CEC introduction](https://docs.kernel.org/userspace-api/media/cec/cec-intro.html)
and
[CEC API](https://docs.kernel.org/userspace-api/media/cec/cec-api.html)
are the open references this plan follows, together with the
[kernel's CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
and the tools in [v4l-utils](https://git.linuxtv.org/v4l-utils.git):
[`cec-ctl`](https://www.mankier.com/1/cec-ctl),
[`cec-follower`](https://www.mankier.com/1/cec-follower), and
[`cec-compliance`](https://www.mankier.com/1/cec-compliance).

The terms this plan uses:

* A **physical address** is a device's place in the HDMI tree, as four
  numbers. The TV is `0.0.0.0`. A receiver on TV input 1 is `1.0.0.0`.
  A machine on that receiver's input 3 is `1.3.0.0`. A source reads its
  physical address from the EDID of the device it is connected to, and
  the address changes when the cables change.
* A **logical address** is a number from 0 to 15 that a device claims
  on the bus when it starts. The number states the device type. 0 is
  the TV and 5 is the audio system, and each of those two exists at
  most once on a bus. Playback devices take 4, 8, and 11, recording
  devices take 1, 2, and 9, and tuners take 3, 6, 7, and 10. A switch
  and a video processor have no address of their own. 15 is
  "unregistered" as a sender and "broadcast" as a destination.
* A device reports an **OSD name** of at most 14 characters, a
  **vendor ID** (an IEEE OUI), a **CEC version**, and a **power
  status**: on, standby, or one of the two transitions between them.

CEC gives a device no stable identity. The physical address follows
the cables, the logical address follows the order in which devices
claimed, and two devices of one model report the same OSD name and
vendor. A person identifies a device the same two ways CEC does: by
role ("the TV", "the receiver") or by where it is connected ("the
player on the receiver's input 2"). This plan names objects the same
way.

## The design

### The layers

Each layer owns one part of the work, and each reads the layer below
it through a Kubernetes API.

1. `liken` (plan 70) attaches the adapter and publishes two devices:
   the CEC bus device (`/dev/cecN`) and the input device that carries
   the TV remote's buttons (`/dev/input/eventN`).
2. display-operator (plan 23) publishes each `Display`'s physical
   address, which it reads from the EDID.
3. equipment-operator speaks CEC. It owns the `CECBus`, the
   `Television`, and the `cec:` block of a `Receiver`. It is the only
   component in the cluster that sends or reads CEC messages.
4. media-operator claims the input device for a `Remote`, the same way
   it claims a Bluetooth remote (media-operator plan 35).

MQTT carries no CEC state. The state is in the CRDs, and the bus
topics stay what plan 04 made them.

### The node workload

A CEC adapter is attached to one node, so the code that holds it runs
on that node. Plan 05 made the split by attachment: network equipment
is the host-network `Deployment`, and wired equipment is a component
on the node that has the hardware.

The binary gains a second mode, `equipment-operator cec`, and the
manifests run it as a second workload from the same image: a
`DaemonSet` whose pods each claim one CEC bus device through a
`ResourceClaimTemplate`. One image keeps one version and one release,
so the two halves cannot drift apart. The `Deployment` keeps discovery,
the network drivers, the sessions, and the bus topics. The `DaemonSet`
needs no host network, no MQTT, and no receiver credentials. It needs
its claim and the RBAC for `CECBus`, `Television`, and `Receiver`.

Each attachment kind gets its own workload, because a device claim is
not optional. A pod with a claim is placed only on a node where the
claim can be allocated. That rule places the CEC pod on exactly the
nodes that have an adapter, with no node label. The `DaemonSet` still
makes a pod on every other node, and that pod stays `Pending`, unless
a person labels the node `equipment.liken.sh/cec: none`. A single
"local equipment" pod would need a claim for every kind it could
manage, and it would be placed only on nodes that have all of them.
Serial or IR equipment would be a third workload,
`equipment-operator serial` for example, with its own claim and RBAC.

The `DeviceClass` for the claim selects `liken`'s CEC bus device by
its `subsystem` attribute, which `liken` plan 70 defines. It ships
with this operator, as bluetooth-operator's `bluetooth-adapter` class
ships with that operator.

### The CEC library

No Go library speaks the kernel's CEC API. The existing Go CEC
packages bind libCEC through cgo or target only the Raspberry Pi. So
`cec/` is a new package on `golang.org/x/sys/unix`: the ioctls from
the kernel's `linux/cec.h`, the message set, and the encoders and
decoders for the operands this plan uses. The kernel's `cec.h` and
`cec-funcs.h` are its reference.

The kernel does part of the protocol by itself. When the adapter has
a logical address, the kernel answers polls, Give Physical Address,
Give OSD Name, and Get CEC Version. It answers Give Device Vendor ID
too: with the vendor the claim states, or with a Feature Abort when
the claim states none, as the pod's does. The request never reaches
the pod. The
pod opens the device as the exclusive initiator and as a follower,
and it answers the messages the kernel passes up. Give Device Power
Status is one of them: the kernel does not answer it.

`cec/` follows the repository's driver rule. Its `AGENTS.md` holds the
protocol references, the message families, and the vendor notes, as
`denon/AGENTS.md` does for the Denon.

### The `CECBus`

A `CECBus` is one HDMI tree, which means one CEC wire. It is
cluster-scoped, like a `Receiver`.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: CECBus
metadata:
  name: den
spec:
  mode: Control
  adapters:
  - machine: node-3
    display: acm-0001-receiver
status:
  adapters:
  - machine: node-3
    physicalAddress: 1.3.0.0
    logicalAddress: 4
  devices:
  - physicalAddress: 0.0.0.0
    logicalAddress: 0
    type: TV
    osdName: TV
    vendor: 00e091
    cecVersion: "1.4"
    power: Standby
    represents:
      kind: Television
      name: den
  - physicalAddress: 1.0.0.0
    logicalAddress: 5
    type: AudioSystem
    osdName: AVR
    power: On
    represents:
      kind: Receiver
      name: den
  conditions:
  - type: AddressKnown
  - type: Joined
  - type: Coherent
  - type: Scanned
```

The bus has no name of its own in CEC, so a person names it.

**The adapters.** An entry names the `Machine` that carries the
adapter and the `Display` the adapter announces. It needs no CEC
address, because both names are visible in `kubectl get displays`. The
normal case is one adapter per machine. A selector for a second
adapter on one machine is left for later.

**The adapter announces its `Display`'s physical address.** The
adapter is connected to a spare input and carries no video.
Pulse-Eight documents this
[two-cable setup](https://support.pulse-eight.com/support/solutions/articles/30000022906-usb-cec-adapter-4k-resolution)
for 4K, because the adapter passes 4K60 video only in 4:2:0 color. The
bus is one wire, so the adapter's own port does not matter to the
other devices. A physical address is what a device announces. The
adapter announces the physical address of the machine's video, which
display-operator plan 23 publishes on the named `Display`. So an
Active Source from the adapter switches the TV and the receiver to the
machine's input, and the adapter speaks for that machine on the bus.
Nobody declares which receiver input the adapter is connected to, and
nobody types an address.

**The mode.** `spec.mode` is `Listen` or `Control`. It is an enum, and
it is not a boolean, because the
[Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#primitive-types)
advise that a boolean tends to need a third state later. A third state
is easy to imagine here: one that polls the bus and sends no command.

* `Listen` claims no logical address and sends nothing. The adapter
  opens the bus in the kernel's monitor mode and reports the
  broadcasts it hears, such as Active Source and Report Physical
  Address. A list of devices needs polls, and a poll is a
  transmission, so the device list in `Listen` is partial, and a
  condition says so. `Listen` needs no physical address. The kernel
  lets a monitor only in the no-initiator mode, and clearing a logical
  address needs an initiator, so the pod takes the initiator mode,
  clears the adapter's logical addresses, and then becomes a monitor.
* `Control` claims a logical address as a playback device, sets the
  OSD name, scans the bus, answers the TV, and sends commands. It needs
  the physical address from the `Display`. A CEL rule on the CRD
  requires `display` on every adapter when `mode` is `Control`.

The mode is the person's consent. A device that joins a CEC bus can
switch the TV's input or wake it. A new adapter must not change the
room until a person allows it.

**The OSD name.** In `Control`, an adapter's OSD name is its bus's
name, cut to 14 characters, because the kernel's `osd_name` field
holds 14 bytes and a terminating NUL. A TV lists each source by its
OSD name, and a room's name reads better there than a machine's. Every
adapter of one bus announces the same name, so a change of the bus, or
of its name, is a new claim of the logical address under the new name.

**Observation and derivation.** Each node pod writes only its own
entry under `status.adapters`, with server-side apply keyed by the
machine, so two node pods never write the same field. The
`Deployment` derives `status.devices` and the conditions from the
adapters' reports, and it merges the devices by physical address.

* `AddressKnown`: every adapter in `Control` has a physical address
  from its `Display`.
* `Joined`: every adapter in `Control` holds a logical address.
* `Coherent`: the adapters of one bus that finished a scan see each
  other, each at the physical address it announces. An adapter that
  never sees the others is on a different wire than the spec states.
  The OSD name cannot tell the adapters apart, because they all
  announce the bus's name. The physical address can: each is its own
  `Display`'s address, and it stays the same when an adapter joins
  again at another logical address.
* `Scanned`: the device list is complete. Only `Control` completes
  it: every adapter finished a scan and found at least one device. A
  bus in `Listen` reports `False` with the reason `Listening`. An
  adapter that finds no device reports that the HDMI cable at the
  adapter's output may not carry the CEC wire.

Each entry carries the time of its last report, and the node pod
writes the entry every 30 seconds even when nothing changed. A pod
that dies writes nothing more, so the `Deployment` treats an entry
older than 90 seconds as stale: each condition is `Unknown` with the
reason `Stale`, and the entry's devices leave the merged list. A pod
that stops on `SIGTERM` or loses its adapter clears the adapter's
logical addresses, leaves the bus, and writes the state `Stopped`, and
each condition is then `False` with the reason `Stopped`.

**Discovery.** A node pod that holds an adapter that no `CECBus`
names creates a `CECBus` in `Listen`, named after the machine, with
the discovery label. The person can read what the adapter hears
before they allow anything. A person's `CECBus` that names the same
machine takes over, and the discovered copy steps aside. `discovery.go`
uses the same rule for a WiiM `Receiver`.

**Two adapters on one bus.** The adapters coordinate through the
spec. Scans and power reads merge in the `Deployment`. A command goes
out from one adapter only: the first adapter in `spec.adapters` that
holds a logical address. The exception is the wake: its Image View On
and its Active Source go out from the adapter that speaks for the
`Display` being shown.

### The `Television`

`Television` is a new kind of equipment, beside `Receiver`. It is
cluster-scoped, and it names its protocol by the block it carries.
The first block is `cec:`. A later block could reach a TV over its
network protocol.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Television
metadata:
  name: den
spec:
  cec:
    bus: den
  power: "On"
status:
  cec:
    physicalAddress: 0.0.0.0
    logicalAddress: 0
    osdName: TV
    vendor: 00e091
    cecVersion: "1.4"
  power: Standby
  activeSource: 1.3.0.0
  powerGeneration: 1
  session:
    player: media/den
    display: acm-0001-receiver
    awake: true
    wokeAt: "2026-09-26T18:04:05.123Z"
  wokeAt: "2026-09-26T18:04:05.123Z"
  displays:
  - name: acm-0001-receiver
    physicalAddress: 1.3.0.0
    via:
      kind: Receiver
      name: den
  conditions:
  - type: Reachable
  - type: InCharge
  - type: PowerApplied
  - type: WakeApplied
```

**The name is the role.** A bus has at most one TV, and the TV is
always at `0.0.0.0`. So `spec.cec.bus` identifies the TV, and no
address or name is needed. Discovery creates a `Television` with the
bus's own name when a bus in `Control` finds a TV. The name carries no
suffix, because the kind already says the object is a TV, and a
`CECBus` name carries none either. Discovery owns only the labeled
`Television` with its bus's name. A person adopts it into their own
configuration repository by committing a `Television` under the bus's
name with the spec they want. The object keeps the discovered label,
and discovery keeps it for as long as no other `Television` names the
same bus. A `Television` of another name for the same bus also works,
and discovery then deletes its own. A labeled `Television` under
another name, such as a copy of the discovered YAML, is a person's.

**`spec.power`** is applied once per change, and it is never
re-asserted. This is the rule `Receiver.spec.power` follows. So a
person who turns the TV off with its own remote is not overruled at
the next reconcile. When the field changes, the node pod sends the
command. Then it reads the power status until the TV reports the new
state, and it sends the command again while the TV does not. The
Denon's `ConfirmedBy` uses the same pattern.

**`status.power`** comes from Give Device Power Status. The node pod
polls it on a slow timer, about every ten seconds, and it also reads
every Report Power Status the bus carries. One CEC message takes tens
of milliseconds, so the poll costs little. A TV in a deep standby or
eco mode can stop answering. Then `Reachable` is `False` and
`status.power` is empty. The operator does not report `Standby` for a
TV that did not answer, and it cannot wake that TV.

**`status.activeSource`** is the physical address of the last Active
Source the bus carried. CEC has no query for the TV's input. A TV that
switches to its own apps or to an input without CEC sends nothing, so
this field states the last CEC source, and it does not state what the
TV shows.

**`status.displays`** lists the `Display` that each adapter of the bus
names in `spec.adapters[].display`, while the bus is in `Control`. The
`Deployment` derives it: the adapter announces its `Display`'s
physical address on this wire, so a `Display` at `1.3.0.0` leads to
the TV at `0.0.0.0`. A `Display` no adapter names is not listed, even
on a machine the bus names. That machine's other HDMI output can go to
another TV, and a physical address does not name its tree, so the
session match would read that `Display` here and could wake the wrong
TV. `via` names the
`Receiver` on that path when one is. With this list, the path from a
`Player` to its TV is a lookup: `Player`, then `Display`, then
`Television`. A TV with no CEC has no tree to read. A declared list
for that case is left for later.

**The session.** A `Television` has a `status.session` with the same
`player` and `awake` fields a `Receiver`'s session has, a `display`,
and a `wokeAt`. The `Deployment` writes it under its own field
manager. It is status and not spec, because no person asks for it,
and a status write changes no `metadata.generation`, so it asks
nothing of `spec.power`. When a `Receiver`'s session wakes the room, the `Deployment`
takes the session's input, reads the input's `monitor`, which is a
`Display`, and finds the `Television` whose `status.displays` lists
that `Display`. It sets that `Television`'s `session.awake` and a new
`session.wokeAt`. Each new `wokeAt` runs the wake job once on the node
pod that speaks for the `Display`: it wakes the TV and makes the
session's `Display` the active source.
A TV connected directly to a machine, with no receiver, has no
`Receiver` session to follow. media-operator writing the
`Television`'s session for that case is left for later.

### Intent, and the vendor order

The node pod acts on intent: wake the TV, show this `Display`, put the
TV in standby. It does not accept raw CEC messages from other
components. The order of the messages depends on the TV's brand, so
that knowledge is in one component. The generic order to show a
`Display` is Image View On to the TV, then an Active Source broadcast
with the `Display`'s physical address. Some TVs need more.
[libCEC issue #753](https://github.com/Pulse-Eight/libcec/issues/753)
reports a TV that shows the input but sends no remote keys until the
source sends Active Source again. libCEC keeps a handler for each
brand in
[`src/libcec/implementations`](https://github.com/Pulse-Eight/libcec/tree/master/src/libcec/implementations).
`cec/` takes the behavior from that code, one vendor at a time, with a
link to the lines it follows and a note in `cec/AGENTS.md`. It copies
no code, because libCEC's license differs from this repository's.

### The `Receiver`'s `cec:` block

A receiver on the bus is the audio system. A bus has at most one, so
`spec.cec.bus` identifies it, as it identifies a TV. The block lets
one `Receiver` hold both paths to one receiver, and the receiver needs
no second object:

```yaml
spec:
  denon:
    address: den.example
  cec:
    bus: den
```

The schema rule changes. A `Receiver` holds at most one network block,
`denon:` or `wiim:`, and it may add `cec:`. It holds at least one
block.

Each control has one owner. The network block owns every control it
can act on: power, input, volume, and mute. The `cec:` block owns what
the network cannot do, which starts with System Audio Mode: the
request that makes the TV send its sound to the receiver and hand its
volume keys to the receiver. When a `Receiver` has only `cec:`, CEC
owns power, volume, and mute as well. `status.cec` reports the
receiver's address, power, and System Audio Mode from the bus.

### The TV remote

The kernel turns the TV remote's buttons into key events when the
adapter holds a logical address and sets the pass-through flag in its
logical address request. The node pod sets that flag in `Control`.
`liken` plan 70 publishes the input device separately from the CEC bus
device, so a media `Remote` claims it the same way it claims a
Bluetooth remote. The kernel names the keys with the
[`rc-cec` keymap](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/rc/keymaps/rc-cec.c?h=v7.2),
and those names are the names a `Keymap` uses. equipment-operator
does not read or forward the keys. A remote on a bus in `Listen`
sends no keys, because the adapter holds no logical address.

## What the first drill measured

A drill on 2026-09-26 ran the design by hand with `cec-ctl`. The
machine had a Pulse-Eight adapter on a receiver's spare input, with
nothing in the adapter's own HDMI socket. The TV was on the
receiver's output, and a streaming player was on another receiver
input. The adapter needs no source cable: the scan found the whole
tree with the socket empty, and with a second cable from the
machine into the socket.

* **The tree is what the design expects.** With the adapter set to
  the physical address from the machine's `Display` EDID, a scan
  found the TV at `0.0.0.0`, the receiver at `1.0.0.0` as the audio
  system, the adapter at `1.2.0.0` as Playback Device 1, and the
  streaming player at `1.5.0.0` as Playback Device 2. Each device
  answered Give OSD Name, Give Device Vendor ID, Give Device Power
  Status, and Get CEC Version. The TV's OSD name was cut at 14
  characters.
* **The receiver keeps the CEC wire and the EDID in standby.** After
  a broadcast Standby, the TV and the receiver both answered their
  power status as standby, and the machine's two ports read the same
  physical addresses as before. Image View On to the TV, then Active
  Source for `1.2.0.0`, woke the TV and the receiver.
* **Another source can take the input after a wake.** The first
  Active Source went out while the receiver woke, and the receiver
  came up on the streaming player's input, because that player
  claimed Active Source for itself. A second Active Source, sent
  once the room was on, switched the receiver to the machine. So
  the wake job confirms the active source after the room is on, and
  it sends Active Source again when another source holds it.
* **`Listen` hears directed messages.** In the kernel's monitor-all
  mode, the adapter reported messages between other devices, such
  as the TV's power query to the receiver. The TV and the streaming
  player each polled every logical address about every 15 seconds,
  so a bus in `Listen` learns which addresses are taken without
  sending a poll.
* **The TV asks the adapter for its power status.** The kernel does
  not answer Give Device Power Status, so the node pod answers it as
  a follower.
* **The TV remote reaches the input device.** With the adapter as the
  active source, the TV sent User Control Pressed for the arrows, OK,
  and back, and the kernel's input device delivered `KEY_UP`,
  `KEY_DOWN`, `KEY_LEFT`, `KEY_RIGHT`, `KEY_OK`, and `KEY_EXIT`.
  media-operator binds all six today. The TV's remote has no play or
  pause key, so the drill did not test the transport keys.
* **A cable without the CEC wire fails silently.** With the first
  cable from the adapter to the receiver, every message ended in
  `TRANSMIT_FAILED_ACK`, and the adapter heard no traffic at all,
  with both this operator's path and libCEC's `cec-client`. A second
  cable fixed it. Many HDMI cables leave out the CEC wire. So when
  every poll from an adapter goes unacknowledged and it hears nothing,
  the `CECBus` condition says that no device answers and that the
  cable between the adapter and the receiver may not carry CEC.

**A claim works with no privilege, except `Listen`.** A second drill
on 2026-09-26 ran on `liken` 2026.09.26-001 with a `spec.serio`
entry. A pod with no privileged flag, no added capability, and no host
mount claimed the `-cec` device through a `DeviceClass` on the
`cec` subsystem, received `/dev/cec0` and no other node, joined the
bus as Playback Device 1, scanned the tree, and read the TV's and the
receiver's power status. The kernel refused monitor-all mode to that
pod, because monitor modes need `CAP_NET_ADMIN`. So the node pod adds
that one capability for `Listen`, and `Control` needs none.

## What phase 1 found in the kernel's API

Phase 1 ran against the kernel's `vivid` driver and the kernel's CEC
documentation on 2026-09-26. Four findings correct or add to the text
above:

* **Give Device Vendor ID.** The node pod's claim states no vendor,
  so the kernel answers the request with a Feature Abort itself, and
  the request never reaches the pod (`cec_receive_notify` in
  `drivers/media/cec/core/cec-adap.c`). `cec-compliance` reports the
  feature as "OK (Not Supported)".
* **No acknowledge bit in `Listen`.** A received message carries no
  acknowledge bit for a message between two other devices: the
  receive status holds only OK, timeout, Feature Abort, and aborted.
  So a monitor that hears the TV poll every logical address does not
  learn which polls found a device, which the first drill's note on
  `Listen` assumed. A device is present in `Listen` when it sends a
  message. The hardware drill 2 below checks this on a Pulse-Eight.
* **The order of the `Listen` calls.** A monitor must be in the
  no-initiator mode, and clearing the logical addresses needs an
  initiator. The pod takes the initiator mode, clears the addresses,
  and then becomes a monitor.
* **`vivid` refuses a physical address.** An adapter on a video port
  takes its address from its own port, and `vivid`'s output adapters
  refuse `CEC_ADAP_S_PHYS_ADDR` with `ENOTTY`. An output has an
  address only after `v4l2-ctl` connects it to one of the TV's inputs.
  So the path that announces the `Display`'s address runs only on a
  USB adapter and in the bus in memory the tests use, and on `vivid`
  the pod announces the port's own address and says so in its entry.

## What phase 2 found

Phase 2 ran against the kernel's `vivid` driver on 2026-09-26, with a
TV the tests play in Go and with `cec-follower` from v4l-utils playing
the TV. Five findings correct or add to the text above:

* **Quote `On`.** `kubectl` reads YAML through `sigs.k8s.io/yaml`,
  which follows YAML 1.1, so an unquoted `On` is the boolean `true`,
  and the API server refuses it for the `spec.power` enum. The
  example above now writes `power: "On"`, and the field's description
  says so.
* **The kernel does not play the TV.** With vivid's TV adapter
  claimed as a TV and no follower on it, the CEC core answered Give
  Device Power Status with a Feature Abort, before and after an Image
  View On. So a test plays the TV, or `cec-follower` does. After Image View On, `cec-follower` reported
  Standby for about 2 seconds, then ToOn, and On at about 9 seconds.
* **Read before the command.** The node workload reads the TV's power
  first and sends nothing when the TV already reports the state asked
  for, because Image View On also switches a TV's input.
* **Send again only without progress.** A TV that reports ToOn or
  ToStandby is on its way. So the node workload sends the command
  again only when a 10-second window ends without the new state or
  the transition toward it. It sends at most three commands and stops
  after 30 seconds. A fixed resend after 10 seconds would have sent a
  second Image View On to a TV that `cec-follower` plays.
* **A TV that stops answering loses its power.** A scan keeps a fact a
  device does not answer. The power read of this phase clears the
  power after two questions in a row without an answer, so
  `status.power` is absent and `Reachable` is `False` for a TV that
  acknowledges and does not answer, as the design above states. One
  missed answer keeps the power, because a TV that wakes can miss one,
  and `Reachable` would otherwise change back and forth.

Phase 2 settled these questions that the design left open:

* **What records a change as applied: the generation.** The node
  workload applies each generation of each `Television` once, keyed
  by the object's uid and its `metadata.generation`, and writes the
  generation it applied to `status.powerGeneration`. So a restart of
  the node workload, such as after an unplug, sends no command again.
  Every spec edit is a new generation, so a person asks for the same
  value again by an edit, such as removing `spec.power` and adding it
  back. A new object applies its `spec.power` when it is created, so
  a `Television` deleted and created again, by a GitOps prune or a
  restore, applies its `spec.power` again; the uid tells it from the
  old object of the same name. The node workload records a generation
  as done in memory before it writes the result, so a write the API
  server refuses is written again at a later pass, and the command is
  never sent again. One generation gets at most three commands,
  whatever happens to its applications. A new generation cancels an
  application of an older one, and an application checks for its
  cancellation before each command. The node workload records a
  confirmed and an unconfirmed result alike, so a TV that never
  confirms gets no command loop. The `PowerApplied` condition, which
  the node workload owns, states the command, the number of commands,
  and the power the TV last reported. The `Deployment` owns
  `Reachable` and `InCharge`. The conditions are a map keyed by type,
  so the two writers do not remove each other's conditions.
* **Read first, except right after a command.** The node workload
  reads the TV's power before the command and sends nothing when the
  TV already reports the state. It skips that read when the adapter
  sent the TV a command in the last 15 seconds, because a TV answers
  its old state for a while after a command. Without that rule, a
  change from On to Standby inside one window left the TV on, with
  the spec and the status both at Standby.
* **Which workload discovers a Television.** The `Deployment`
  creates a `Television` with the bus's own name when a `CECBus` in
  `Control` has a TV in its merged `status.devices`, because only the
  `Deployment` reads every adapter's report. It creates the object
  with a create, not an apply, so an object that already has the name
  stays as it is. It deletes its own `Television` only when another
  `Television` names the same bus.
* **Two Televisions on one bus.** A person's `Television` is in
  charge over the discovered one, and of two of a person's, the first
  by name. The node workload applies only the `spec.power` of the one
  in charge, and the `InCharge` condition of the other names the one
  in charge. When that one is deleted, the other takes over, and its
  `spec.power` is applied once then, as a new object's is.
* **A cluster without the Television definition.** Both workloads
  read a missing `Television` collection as empty and start no watch
  on it, so the `CECBus` work of phase 1 goes on.
* **A stale adapter entry.** `Reachable` takes the reason `Stale` from
  the bus's `Scanned` condition, and not `NotScanned`.
* **Every adapter reads the power, and one adapter commands.** Each
  adapter in `Control` asks the TV for its power every 10 seconds, and
  the `Deployment` merges the reads with the rest of the devices, the
  first adapter in `spec.adapters` first. The node workload reads the
  other adapters' state from their current entries to find the first
  adapter that holds a logical address.
* **`via` in `status.displays`.** The `Receiver` a picture passes
  through is the `Receiver` with an input that names the `Display`'s
  machine and the `Display` as its monitor, when the bus has an audio
  system above the `Display` in the tree: a receiver at `1.0.0.0` is
  above `1.3.0.0` and not above `2.0.0.0`. An input match alone is not
  enough, because a receiver's optical input can name a machine whose
  `Display` is connected straight to the TV.
* **`status.displays` follows the adapters.** The list is the
  `Display` each adapter of a bus in `Control` names, as the design
  above now states. The first draft listed every `Display` on a
  machine the bus names. That would list a machine's second output to
  another TV under this TV, and the session match of phase 3 could
  then wake the wrong TV.
* **`status.activeSource`** is not built. The session match of
  phase 3 is its first reader.

## What phase 3 found

Phase 3 ran against the kernel's `vivid` driver on 2026-09-26. The
node workload woke a TV that the test plays on vivid's capture
adapter, and a second vivid output played a streaming player that
claims Active Source once after another device's claim. The TV heard
`8->0 04`, `8->f 82 10 00`, `4->f 82 20 00`, and `8->f 82 10 00`: Image
View On, the node workload's Active Source for its output at `1.0.0.0`,
the player's claim for `2.0.0.0`, and the node workload's claim again,
760 ms after the first. The test shortens the 2-second settle before a
claim again to 500 ms, so the time is the settle and the player's own
delay. Four findings correct or add to the text above:

* **The session is status.** The first draft of phase 3 put the
  session in `spec.session`, as the design said. The API server counts
  every spec edit as a generation, whoever writes it, so each wake
  would have been a new generation of `spec.power`: a TV a person
  turned off with its own remote would come back on at the next Play,
  and a `spec.power` of Standby would put the TV back in standby as
  the wake woke it. The wake request is not a person's intent, so it
  moved to `status.session`. A status write changes no generation, so
  the key of phase 2 stands. The `Deployment` applies the session to
  the status subresource under the field manager
  `equipment-operator-session`, apart from the manager of its derived
  status, so neither of its writes removes the other's fields. A
  person's apply of the spec never reaches a status field.
* **A wake is a time, not a change of `awake`.** A person presses the
  remote's power button to turn the room on while the session is
  already awake: the screen stays up for a while after the receiver
  goes to standby. The receiver's session turns the receiver on then,
  and `awake` does not change. So the session carries `wokeAt`, the
  time of the wake, and the node workload runs one wake for each new
  time. `awake` still states whether the session holds the room awake,
  and when it goes false, a wake in progress stops.
* **A wake is a change the operator sees happen.** The operator acts on
  a change of intent it observes while it runs, never on what it finds
  when it starts. So a wake is a flag of a standing session that turns
  on, when a Play starts or the screen wakes, a toggle that turns the
  receiver on, or a session that appears with a flag already on, such
  as a Play on a Player with no standing session. In its first pass
  after a start the operator adopts each session it finds into
  `status.session` with the `wokeAt` already there, and wakes nothing.
  A session of the same `Player` and `Display` that returns while its
  removal waits is the same session, such as one the media operator
  lifted for a moment while an idle pod restarts, and is adopted the
  same way. A new address for a `Receiver` starts a new unit, so the
  operator lifts the old unit's session, and the new unit's session
  returns within the lift. The first draft woke the TV at every
  session's start, and a review found that a deploy would have woken
  every room with a standing session, and that a brief lift would have
  woken the TV unasked. The
  session tells the room in the order the flags change, before the
  one-shot's own goroutine runs, because a test found that a sleep
  right after a wake could otherwise reach the TV first.
* **Two node writers need two field managers.** An apply removes each
  field its manager owns and does not state. The power and the wake
  are written apart, and often by two machines, so the node workload
  writes the wake under a second manager named after the machine,
  `equipment-operator-wake-<machine>`.

Phase 3 settled these questions that the design left open:

* **Who sends the wake.** The adapter that speaks for the session's
  `Display` sends both Image View On and Active Source: the first
  adapter in `spec.adapters` that names the `Display` and holds a
  logical address. Active Source must come from that adapter, because
  it states its sender's physical address. One-touch play is one
  source's sequence, so its Image View On comes from the same adapter,
  and the wake is still one intent with one sender.
* **The order.** The wake reads the TV's power and sends Image View On
  by the rules of `spec.power`, from its own budget of three commands.
  It sends Active Source once the TV reports On, or once the power
  application ends without it, because a TV that does not answer its
  power can still take the input.
* **The bound on the second Active Source.** After its first Active
  Source, the adapter listens for 30 seconds. When another source
  claims the input, it waits 2 seconds, so the claiming device's burst
  of messages ends, and sends Active Source again, at most twice. After
  the 30 seconds, and after the second, a claim stands, so a person
  who switches the input on purpose is not fought. CEC gives no way to
  tell a person's switch from a device's, so the bound is time and
  count alone. The TV's Routing Change is not read as a person's
  switch, because a TV can send one when it wakes.
* **What confirms the wake.** The last Active Source on the bus when
  the guard ends. `WakeApplied` is `True` when the TV reported On and
  that Active Source is the `Display`'s own. It is `False` with the
  reason `Unconfirmed` when the TV did not report On, at the command or
  at the end of the guard, `SourceTaken` when another source holds the
  input, `Refused` when the adapter could not send, `Stopped` for a wake
  that stopped before its end, `Superseded` for a wake that a new
  generation of `spec.power` or another adapter's Active Source ended,
  and `TooLate` for a wake it did not run. While a wake runs, the
  condition is `Unknown` with the reason `Waking`.
* **A late wake.** A wake that has not started 2 minutes after the
  node workload first saw it sends nothing, and it is recorded as
  `TooLate`, such as one that waited that long for `spec.power` or for
  the adapter to join. The node workload measures the time on its own
  clock, from when it first saw the `wokeAt`, and not from the
  `Deployment`'s time in `wokeAt`, because the two machines' clocks can
  differ.
* **A restart.** Both workloads start with nothing sent. A `wokeAt`
  that is in the status when the node workload starts is what it
  finds, so it sends nothing for it and logs one line that says so.
  Before its first command a wake writes its `wokeAt` in
  `status.wokeAt` as a started mark, under the wake's field manager,
  so a wake a pod roll, an unplug, or a change of mode stops is never
  run again: its Image View On and its Active Source each come from a
  budget of one wake. A wake that stops records `Stopped`.
* **A lift.** When a `Receiver` session ends, its `Receiver` is
  deleted, or its `Receiver`'s address changes, the `Deployment`
  removes the TV's `status.session` 60 seconds later, unless a session
  of the same `Player` starts first.
  A removal the API server refuses is tried again every 10 seconds. A
  `Deployment` that stops removes nothing, because the next one adopts
  the same sessions.
* **One writer of `status.session`.** Each write of a session reads the
  `Television` and applies what it read with one change, so a sleep
  from the reconcile pass and a wake from the remote's power button at
  once could undo each other. One mutex in the `Deployment` takes them
  one at a time.
* **A Standby the adapter hears.** A receiver that goes to standby
  sends the TV Standby, and the TV can still answer On for a while
  after it. So a Standby to the TV that the adapter heard in the last
  15 seconds counts as a recent command, the same as its own: the wake
  sends Image View On without a read first. The wake also reads the
  TV's power at the end of the guard, and a TV that is not On then is
  `Unconfirmed`.
* **Two adapters.** An Active Source from another adapter of the bus is
  a later wake of the same bus, so the adapter that guards ends its
  guard at once, with the reason `Superseded`, and claims nothing more.
  The next pass also stops a wake whose `Display` this adapter no
  longer speaks for.
* **The lines.** A wake writes a line when it starts, a line when it
  ends, and a line when it waits behind `spec.power`.
* **A sleep.** A toggle that puts the receiver in standby, and a
  session whose Play and screen both go off, set `awake` to false. The
  wake in progress stops, and the adapter sends the TV nothing: the
  receiver's own CEC link turns the TV off with it.
* **`spec.power` and a wake at the same moment.** `spec.power` goes
  first. A wake does not start while a generation of `spec.power` is
  not applied yet, because the two would send the TV opposite commands
  at once; it starts when `status.powerGeneration` catches up, if the
  wake is still less than 2 minutes old. A generation that arrives
  while a wake runs is a person's edit, newer than the wake, so it
  stops the wake for good, with the reason `Superseded`, and the
  adapter sends nothing more for that wake. The rule reads
  `status.powerGeneration`, which every node workload sees, so it holds
  when the adapter that sends `spec.power` is on another machine. The
  node workload runs the wake's pass before the power pass, so a wake
  stops before the new generation's first command.
* **Which `Television`.** The `Television` in charge of the bus whose
  `status.displays` lists the `Display`. A room with no `Television`
  that lists the `Display` gets no write and no log line. A session
  that starts on another `Display` leaves the TV that showed the old
  one at once.
* **`status.activeSource`.** Each adapter reports the last Active
  Source it heard or sent in its entry, in `Listen` as well as in
  `Control`. The kernel does not pass an adapter its own transmission,
  so the node workload records its own. The `Deployment` copies the
  one the first adapter in `spec.adapters` with a current entry
  reports.

## The power press turns the room off

A home cluster showed the gap on 2026-09-27. A person pressed the
power button on a room's remote to turn the room off. The room's
receiver is a WiiM, which has no standby command, and its `Receiver`
session logged a failed command. Nothing turned the TV off. The rule
"A sleep" above assumed that the receiver's own CEC link turns the TV
off with it. A WiiM has no CEC link, and a receiver that stays on
sends the TV nothing. No path led from a power press to the TV.

The press now toggles the room, TV included:

* **The TV decides.** The `Receiver` session finds the `Television`
  that lists its input's `Display`, as the wake does. A TV that
  reports On or ToOn means the room is on, and the press turns it off.
  A TV in Standby or ToStandby means the room is off, and the press
  turns it on. With no `Television`, or a TV with no reported power,
  the receiver's power decides, as before.
* **Off.** The `Deployment` writes a new `status.session.standbyAt`
  on the `Television`, with `awake` false and the `wokeAt` it held,
  under the session's field manager. The receiver goes to standby when
  it has a standby command. The `equipment.Driver` contract answers
  that with `HasStandby`: a Denon has `PWSTANDBY`, and a WiiM has
  none, so a WiiM stays on and its line states that as the outcome of
  the press.
* **On.** The press writes a new `wokeAt`, and the wake runs as
  before, with its reclaims and its `TooLate` rule. A new wake removes
  `standbyAt`, because the apply does not state it.
* **The node workload.** The adapter that speaks for the session's
  `Display` sends the TV Standby once for each `standbyAt`, through
  the same power confirmation `spec.power` uses: a read first, at most
  three commands, and a readback. Before its first command it writes
  `status.standbyAt` as a started mark under
  `equipment-operator-standby-<machine>`, and `StandbyApplied` states
  the result. A `standbyAt` in the status when the node workload
  starts sends nothing. `spec.power` goes first, by the rule of the
  wake. A wake stops a standby in progress, and a standby never starts
  beside a wake, because the two send the TV opposite commands.
* **Only a press.** A `Play` that ends, a screen that sleeps, a
  session that the media operator lifts, and an operator restart write
  no `standbyAt`. A TV in a living room shows other inputs, such as a
  streaming player, while the room's player is idle, and those events
  must not turn it off.

The fakes and `vivid` prove the paths. A hardware drill on the room
that showed the gap is open.

## The adapter sends nothing on a timer

A home cluster showed the problem on 2026-09-27. The bus held a
TV, an AV receiver, and a streaming player. While
the adapter was in `Control`, the TV switched its own input three
times in about 20 minutes. Each time the TV broadcast a Routing
Change from `1.2.0.0` to `1.0.0.0`, the receiver answered with Routing
Information `1.2.0.0`, and about 12 seconds later the TV sent Set
Stream Path `1.2.0.0`. Once it went to the streaming player's path
instead. With the bus in `Listen`, where the adapter sends nothing,
the TV switched no input in 20 minutes. The move from `Control` to
`Listen` itself caused one Routing Change at once. So the adapter's
own traffic caused the switches. In `Control` the node workload sent
a full scan every minute, which is a poll to each of 14 logical
addresses and five questions to each device that answered, and it
asked the TV for its power every 10 seconds.

The node workload now follows the organization's rule for state it
keeps current. It subscribes first, reads the state once as a
baseline, and after that changes what it holds only on the
subscription's events. When the subscription fails, it subscribes
and reads again. No timer re-reads a state.

* **The wire.** The subscription is the follower mode, which the
  handle takes before its claim, so the kernel queues each message the
  adapter hears from the moment it joins. The baseline is one scan
  when the adapter joins, which also reads the power of each device.
  An adapter that leaves ends the process, and the kubelet starts a
  new one. A claim the kernel takes away, a new physical address, and
  a change of mode each join again and scan again. A scan the kernel
  refuses is tried again after 30 seconds, doubling up to 10 minutes,
  as a join is.
* **A device that announces itself.** A device that joins after the
  scan claims an address, and its kernel broadcasts Report Physical
  Address. When the adapter hears a sender it does not hold, or a
  Report Physical Address with a new physical address, it asks that
  device once for the facts the directory does not hold. It sends
  nothing to the other devices, and nothing more to a device that
  repeats its broadcast, with one exception. A TV whose power the
  directory does not know, such as one in a deep standby at the scan,
  is asked for its power alone when it broadcasts Report Physical
  Address or Device Vendor ID, as a TV does when it wakes. A burst of
  announcements queues one question, and a TV whose power stays
  unknown, such as one that refuses Give Device Power Status, is not
  asked again at the same physical address. A device that leaves in
  silence stays in the list until a question to it goes
  unacknowledged or the adapter joins again.
* **The TV's power.** The directory sets the TV's power from the
  messages that change it: a Standby to the TV or to every device
  means Standby; Image View On and Text View On, which an adapter
  hears only in `Listen`, mean On; a Routing Change, a Set Stream Path,
  or a Request Active Source from the TV means On; an Active Source
  from any device means On. A Report Power Status that reaches the
  adapter sets the power as before. In `Control` the kernel passes the
  adapter only the broadcasts and the messages to its own address,
  and it drops a broadcast Report Power Status for a claim that states
  CEC 1.4. So a TV that a person turns off with its own remote and
  that broadcasts no Standby sends nothing the adapter hears. `status.power` then stays On until the next read:
  the next command, the next press of the remote's power button, or
  the next join. A TV turned on with its own remote usually sends a
  Routing Change or a Request Active Source, and a TV that sends
  neither stays in Standby the same way.
* **The power press reads the TV.** The press decides the whole room
  from the TV's power, so it cannot use a value that can be stale.
  For a `Television` whose TV an adapter in `Control` finds, the
  session writes a new `status.session.powerReadAt`. That is a
  `Television` that is `Reachable`, or one that is not `Reachable`
  with the reason `NoPower`: its `status.power` is empty after the TV
  gave no power at the join scan or on two reads, and such a TV can be
  on. The node workload whose adapter sends
  the bus's commands asks the TV once and writes the answer in
  `status.powerRead`. The session waits up to 3 seconds, on a watch of
  the `Television`s, and decides from the answer; with no answer it
  decides from `status.power` and logs that it did. A request that is
  already in the status when the node workload starts is older than
  the wait, and the node workload sends nothing for it. The node
  workload could decide the toggle itself, but the receiver's half of
  the press needs the same decision at the same moment, so the read
  comes back to the session.
* **The commands.** `spec.power`, the wake, and the standby read the
  TV before and after their commands, as before. The follower's
  answers and the commands a person causes are unchanged.
* **The API.** The watches of `CECBus`es, `Television`s, and
  `Display`s are the subscriptions, and each pass reads them from the
  watches' stores. The node workload now watches `Display`s, because
  the adapter announces a `Display`'s physical address, and its role
  gains `list` and `watch` on them. The watch of a kind whose
  definition is missing holds no object and asks the API server
  again every five minutes, so it finds a definition installed after
  the pod starts within five minutes. The backstop pass
  every 30 seconds is gone. A join that failed is tried at its retry
  time, and a list or a write the API server refused is tried again
  after 10 seconds.
* **The heartbeat.** The node workload still writes its entry every
  30 seconds, and a heartbeat now writes only the entry and runs no
  pass. The write goes to the API server and sends nothing on the
  wire. The `Deployment` treats an entry older than 90 seconds as
  stale, and that rule needs the heartbeat: an entry that nothing
  changes would otherwise look like a pod that stopped. A lease or a
  read of the pod's readiness could replace it later.

The fakes prove that an idle bus hears nothing after the scan over
many heartbeats and passes, that a device that announces itself is
asked four questions once and nothing when it announces again, that
a TV whose power stays unknown is asked one question over five pairs
of announcements and a player none, and that a press decides correctly from a TV whose power changed with no
message the adapter heard. On `vivid`, the TV's handle received no
message from the adapter over 2 seconds of passes and 5-millisecond
heartbeats after the scan; the kernel passes a follower no poll, so
the check covers the questions and the commands. On `vivid` a TV's claim sometimes ends at logical address 14
with no device at 0; the tests' TV claims again until it holds 0. A
drill on the room that showed the switches is open: the adapter in
`Control` for an hour, with a count of the TV's Routing Changes, and a
press of the remote's power button after the TV's own remote turned
it off.

The `Deployment`'s `CECBus` loop watches the `Display`s and the
`Receiver`s' specs too, so a change to either reaches a `Television`
at once. Its 30-second tick is a clock: it finds an entry gone stale,
tries a refused write again, and opens the watch of a definition
installed later. The `Receiver` loop keeps its 30-second backstop for
the failures that no event follows, such as a refused list or write.
Neither sends on the wire.

## Failure and recovery

**The receiver in standby.** The adapter is connected to a receiver's
input. A receiver can cut the CEC wire between its inputs and the TV
while it is in standby. Then the adapter cannot reach the TV while the
TV and the receiver are off, which is when the wake is needed. A
receiver can also stop giving the machine an EDID, or give it the TV's
EDID, while it is in standby. Then the `Display`'s physical address is
missing or wrong. Display-operator plan 23 keeps the last address it
read, and the node pod announces that address. Both behaviors are
unknown for the receivers at hand, and the first drill measures them.

**The adapter's own firmware.** The Pulse-Eight firmware has an
autonomous mode that answers a TV and wakes on some vendor commands
without the host. The kernel driver turns it on when the host claims a
logical address and turns it off when the host clears the address. It
stores its settings in the adapter's EEPROM only when the
`persistent_config` module parameter is 1, and `liken` leaves it at 0.
The notes at the top of
[`pulse8-cec.c`](https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/tree/drivers/media/cec/usb/pulse8/pulse8-cec.c?h=v7.2)
describe this mode. What an adapter does when a previous host left
autonomous mode in its EEPROM is unknown, and a drill measures it.

**Unplugging the adapter.** When the adapter leaves, the kernel
removes `/dev/cecN`, and every read on it returns `ENODEV`. A running
container keeps the file descriptor of a device that is gone, and DRA
does not evict a pod when its device leaves the slice. So the node pod
exits when a read or an `ioctl` returns `ENODEV`, and it states the
error in its last log line. This is the claimant contract of `liken`
plan 70: the kubelet restarts the container, and the new container
receives the nodes the claim's CDI spec names when the adapter is
back on the same USB port. `liken` publishes no taint for a device
that is gone.

**Reading a failure.** "The TV did not wake" crosses four objects.
Each one states what it saw, in this order:

1. `Machine.status.serio`: the adapter is attached, and its nodes
   exist.
2. `Display.status.physicalAddress`: the machine's place in the tree.
3. `CECBus` conditions and `status.adapters`: the adapter holds a
   logical address, announces the address, and sees the other
   adapters.
4. `Television` `Reachable` and `status.power`: the TV answers, and
   what it reports.
5. `Television` `status.session`, `status.wokeAt`, and `WakeApplied`:
   the session reached the TV, the adapter ran the wake, and what the
   bus reported at the end of it.

## Testing

The kernel's
[`vivid`](https://docs.kernel.org/admin-guide/media/vivid.html#cec-consumer-electronics-control)
driver creates virtual HDMI inputs and outputs, and each one has a
real kernel CEC adapter. Each output's adapter is connected to one of
the input adapter's ports, and each output reads an EDID with its own
physical address. So one `vivid` instance is a CEC bus with a TV and
several sources. `liken`'s kernel ships `vivid` with
`CONFIG_VIDEO_VIVID_CEC=y`, and so does Ubuntu's generic kernel.

* `cec/` is tested against a fake of the ioctl boundary for the
  encoders, the decoders, and the error paths.
* The node pod's loops are tested against `vivid`, with `cec-follower`
  playing the TV on the input adapter. `cec-compliance` checks the node
  pod's answers as a follower.
* The `Deployment`'s derivations, `status.devices`, `status.displays`,
  and the session match, are tested with objects in the API, the way
  the session tests are now.

## What was considered and set aside

* **A separate CEC operator.** It would hold the adapter and publish
  the bus, and equipment-operator would use it. The TV and the audio
  system are roles that the CEC specification fixes, and both are A/V
  equipment. A separate operator would split one receiver across two
  owners and two objects.
* **One object for each CEC device.** A `CECDevice` for every device on
  the bus needs a stable name, and CEC gives none. The TV and the audio
  system are unique by role, so they become equipment. Every other
  device is in `CECBus` status until an equipment kind needs it.
* **An HTTP API or MQTT for commands.** A request API such as the one
  display-api serves would add a data plane for what is state:
  whether the TV is on and what it shows. The state is readable from
  the bus, so it is reconciled from the spec.
* **libCEC.** It needs cgo and a second protocol stack beside the
  kernel's, and its adapter code duplicates the kernel driver.
* **The adapter in the video path.** The adapter passes video, but at
  most 4K60 in 4:2:0 color. A machine that sends 4K60 in full RGB gets
  no picture through it.

## Phases

1. `cec/`, the node workload, and the `CECBus` in `Listen` and
   `Control`, tested against `vivid`.
2. The `Television`, with `spec.power`, `status.power`, and
   `status.displays`.
3. The session match and the wake job, which close plan 07's TV wake.
4. The `Receiver`'s `cec:` block and System Audio Mode.
5. A CEC setup guide in `docs/content/docs/guides/` and a matching
   skill in `skills/`. The guide follows one room from a new adapter
   to a TV that wakes: the `Machine`'s `spec.modules` and `spec.serio`
   (`liken` plan 70), the `Display`'s physical address
   (display-operator plan 23), a `CECBus` from `Listen` to `Control`,
   the `Television` and the `Receiver`'s `cec:` block, and a `Remote`
   on the input device (media-operator plan 35). It states what the
   drills taught: the adapter goes on a spare input with only its own
   cable, many HDMI cables leave out the CEC wire, and another source
   can take the input after a wake. It links the kernel's CEC admin
   guide, the Pulse-Eight pages, and `cec-ctl` for a check by hand.

## Verification

The drills run on hardware, with a Pulse-Eight adapter on a receiver's
spare input:

1. The receiver in standby: whether the adapter still reaches the TV,
   and what EDID the machine reads. This drill runs before phase 1,
   with `cec-ctl`, because its result can change the design.
2. `Listen`: whether the adapter reports broadcasts sent to other
   devices.
3. The firmware: what the adapter does on the bus with no host after a
   host set autonomous mode.
4. The wake: from standby, a press of the remote's power button leaves
   the receiver on the session's input and the TV on and showing it.
   This proves plan 07's TV wake.
5. Unplug and replug: the node pod exits on `ENODEV` and returns with
   the adapter.

## What this leaves for later

* A declared `status.displays` for a TV with no CEC.
* The `Television` session for a TV connected directly to a machine.
* A selector for two adapters on one machine.
* A `Scan` mode between `Listen` and `Control`.
