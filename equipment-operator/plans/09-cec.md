# The TV and the receiver over HDMI-CEC

Plan 09. Phase 1 built 2026-09-26 and tested against `vivid`; its
hardware drills and phases 2 to 5 are open. It depends on `liken` plan 70, which
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
nodes that have an adapter, with no node label. A single
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
only when the claim states a vendor. The pod states none, so that
request reaches the pod, which answers it with a Feature Abort. The
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
      name: den-television
  - physicalAddress: 1.0.0.0
    logicalAddress: 5
    type: AudioSystem
    osdName: AVR
    power: On
    represents:
      kind: Receiver
      name: den-receiver
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

**The OSD name.** In `Control`, an adapter's OSD name is its machine's
name, cut to 14 characters. Each adapter then sees the other adapters
of the same bus by name in its scan.

**Observation and derivation.** Each node pod writes only its own
entry under `status.adapters`, with server-side apply keyed by the
machine, so two node pods never write the same field. The
`Deployment` derives `status.devices` and the conditions from the
adapters' reports, and it merges the devices by physical address.

* `AddressKnown`: every adapter in `Control` has a physical address
  from its `Display`.
* `Joined`: every adapter in `Control` holds a logical address.
* `Coherent`: the adapters of one bus that finished a scan see each
  other's OSD names. An adapter that never sees the others is on a
  different wire than the spec states. The logical address is not
  compared, because an adapter that joins again can take another one.
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
holds a logical address. The exception is Active Source, which goes
out from the adapter that speaks for the `Display` being shown.

### The `Television`

`Television` is a new kind of equipment, beside `Receiver`. It is
cluster-scoped, and it names its protocol by the block it carries.
The first block is `cec:`. A later block could reach a TV over its
network protocol.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Television
metadata:
  name: den-television
spec:
  cec:
    bus: den
  power: On
status:
  cec:
    physicalAddress: 0.0.0.0
    logicalAddress: 0
    osdName: TV
    vendor: 00e091
    cecVersion: "1.4"
  power: Standby
  activeSource: 1.3.0.0
  displays:
  - name: acm-0001-receiver
    physicalAddress: 1.3.0.0
    via:
      kind: Receiver
      name: den-receiver
  conditions:
  - type: Reachable
```

**The name is the role.** A bus has at most one TV, and the TV is
always at `0.0.0.0`. So `spec.cec.bus` identifies the TV, and no
address or name is needed. Discovery creates a `Television` named
`<bus>-television` when a bus in `Control` finds a TV. A person can
adopt it into their own configuration repository: they commit a
`Television` of any name whose `spec.cec.bus` names the same bus, and
the discovered copy steps aside, as it does for a `Receiver`.

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

**`status.displays`** lists every `Display` whose physical address is
on this bus. The `Deployment` derives it: a `Display` at `1.3.0.0` on
the bus's machines leads to the TV at `0.0.0.0`. `via` names the
`Receiver` on that path when one is. With this list, the path from a
`Player` to its TV is a lookup: `Player`, then `Display`, then
`Television`. A TV with no CEC has no tree to read. A declared list
for that case is left for later.

**The session.** A `Television` has a `spec.session` with the same
`player` and `awake` fields a `Receiver`'s session has, and a
`display`. The `Deployment` writes it under its own field manager.
When a `Receiver`'s session wakes, the `Deployment` takes the
session's input, reads the input's `monitor`, which is a `Display`,
and finds the `Television` whose `status.displays` lists that
`Display`. It sets that `Television`'s `session.awake`. A change of
`awake` from false to true runs the wake job on the node pod: it
wakes the TV and makes the session's `Display` the active source.
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
    address: den-receiver.example
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

* **Give Device Vendor ID.** The kernel answers it only for a claim
  that states a vendor. The node pod states none, so the request
  reaches the pod, and the pod answers it with a Feature Abort.
  `cec-compliance` reports the feature as "OK (Not Supported)".
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
