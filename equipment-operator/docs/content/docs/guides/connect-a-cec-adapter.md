---
title: Connect a USB-CEC adapter
weight: 20
description: "Connect a USB-CEC adapter to a liken machine and to a room's HDMI tree, take its CECBus from Listen to Control, adopt the Television, and let the room's Player wake the TV and turn it off. Use when a TV must wake with a Player, when a TV remote must drive a Player or set the receiver's volume, or when a CECBus or a Television reports a fault."
---

# Connect a USB-CEC adapter

At the end of this guide, a USB-CEC adapter sends and receives
HDMI-CEC messages for a `liken` machine. A `Play`, or the power button
of the room's remote, wakes the TV and switches it to the machine, and
the power button also turns the TV off. The TV's own remote controls
the `Player` and sets the receiver's volume.

The room in this guide has a TV, an AV receiver on one of the TV's
inputs, and the machine `node-1` on one of the receiver's inputs.
`equipment-operator` supports the Pulse-Eight
[USB-CEC adapter](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter),
which the Linux kernel drives with its `pulse8-cec` driver. The
kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
describes that driver and the serial-line attachment it needs.

You need:

* `equipment-operator`, from the [install](/docs/guides/install/)
  guide, with `cec.yaml` applied.
* The [`display-operator`](https://liken.sh/display/), which
  publishes the machine's HDMI output as a `Display` with its CEC
  physical address.
* For the wake and the power button: a `Receiver` that a `Player` of
  the [`media-operator`](https://liken.sh/media/) plays through, with
  an input for the machine and that `Display`. The install guide
  declares that `Receiver`.
* A spare HDMI input on the receiver, and an HDMI cable that has the
  CEC wire connected.
* `kubectl` with cluster-admin access, because the guide edits a
  `Machine` and creates a `DeviceClass`.

## Connect the adapter

Connect the adapter's HDMI output to a spare input of the receiver.
Leave the adapter's own HDMI input empty. Connect the adapter's USB
plug to the machine. The machine's video stays on its own cable to its
own receiver input.

The adapter needs no video. It announces the physical address of the
machine's video input, not the address of the port it is connected
to. All the HDMI ports in the tree share one CEC wire, so the other
devices receive the adapter's messages from any port. Pulse-Eight
documents this
[two-cable setup](https://support.pulse-eight.com/support/solutions/articles/30000022906-usb-cec-adapter-4k-resolution)
for 4K, because the adapter passes 4K at 60 Hz only in 4:2:0 color.

Many HDMI cables do not connect the CEC wire, and nothing reports the
fault. With such a cable, the receiver acknowledges no message from
the adapter, and the adapter receives no traffic. When the `CECBus`
later reports that no device answers, try another cable first.

## Attach the adapter to the machine

A `liken` machine loads only the kernel modules that its `Machine`
lists in `spec.modules`, and the adapter's driver binds to the adapter
only after the machine attaches the adapter's serial line, which a
`spec.serio` entry declares. Add these modules to `spec.modules` in
this order, and add a `spec.serio` entry:

    spec:
      modules:
        - cdc_acm
        - serport
        - pulse8_cec
      serio:
        - protocol: pulse8-cec
          usb:
            vendor: "2548"
            product: "1002"

Keep the modules that the machine already lists, because a merge
patch of `spec.modules` replaces the whole list. The machine loads an
added module and attaches an added entry without a reboot. The `liken`
guide
[Load the drivers for a machine's hardware](https://liken.sh/docs/guides/hardware-modules/#attach-a-usb-cec-adapter)
explains each field, and the
[`Machine`](https://liken.sh/docs/reference/machine/#spec--serio)
reference lists them.

Check the attachment:

    kubectl get machine node-1 -o jsonpath='{.status.serio}' | jq

When the attachment works, the state is `Attached`, and `nodes` lists
`/dev/cec0` and the event node of the TV remote's input device. The
machine then publishes both in its `ResourceSlice`. The device whose
name ends in `-cec` is the CEC bus, which the operator's `cec-adapter`
`DeviceClass` selects. The device whose name ends in `-input` reports
the TV remote's keys, and a media `Remote` claims it at the end of
this guide.

## Read the Display's physical address

Each device on a CEC bus has a physical address, which is its path
from the TV. The TV is `0.0.0.0`. A receiver on the TV's input 1 is
`1.0.0.0`, and a machine on that receiver's input 3 is `1.3.0.0`. The
adapter has no EDID to read an address from, so it announces the
address of the machine's `Display`. The `display-operator` reads that
address from the EDID that the receiver serves:

    kubectl get display don-0070-denon-avr -o jsonpath='{.status.physicalAddress}'

The output is the dotted address, such as `1.3.0.0`. The field is
absent on a DisplayPort output, and when the monitor has never sent a
valid address. The
[`Display`](https://liken.sh/display/docs/reference/displays/#status--physicaladdress)
reference describes the `PhysicalAddressCurrent` condition, which says
whether the address is current or is the last valid one that the
monitor sent.

## Listen to the bus

`cec.yaml` runs the `equipment-operator-cec` `DaemonSet`. Its pod on
a machine, which this guide calls the node workload, runs that
machine's adapter. The pod on `node-1` starts when the machine
publishes the `-cec` device. No `CECBus` lists the machine yet, so the
pod creates one in `Listen` mode, named after the machine, with the
`equipment.liken.sh/discovered` label, and logs this line:

    no CECBus names machine node-1, so the adapter created CECBus node-1 in Listen, which sends nothing on the wire

In `Listen` the adapter claims no logical address and sends nothing.
It opens the bus as a monitor and reports each device that sends a
message. A new adapter starts in this mode because a device that joins
a CEC bus can wake the TV and switch its input, and the operator does
not do that until you allow it.

Read what the adapter heard:

    kubectl get cecbus node-1 -o jsonpath='{.status.adapters}' | jq

Each entry under `devices` is a device that the adapter heard. The
list can be incomplete, because only a poll finds a device that sends
nothing, and an adapter in `Listen` sends no polls. Some devices poll
the bus themselves often enough that the list fills quickly. The
`Scanned` condition is `False` with the reason
`Listening` while the adapter hears devices. It is `False` with the
reason `Silent` while the adapter has heard no device, and the message
then says that the cable may not have the CEC wire connected.

Opening the bus as a monitor needs the `CAP_NET_ADMIN` capability, and
`cec.yaml` grants it to the pod. `Control` mode needs no capability.

## Give the adapter control

To let the adapter send, create a `CECBus` of your own in `Control`,
under a new name, that lists the machine and the `Display`:

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: CECBus
metadata:
  name: living-room
spec:
  mode: Control
  adapters:
    - machine: node-1
      display: don-0070-denon-avr
```

A `CECBus` in `Control` must set `display` for every adapter, and the
API server refuses one that does not. Do not apply your `CECBus` under
the discovered name, `node-1`, because the node workload owns the spec
of that object. When your `CECBus` lists the same machine, the node
workload moves the adapter to it and deletes the one it created:

    deleted the discovered CECBus node-1: CECBus living-room names machine node-1
    the adapter on node-1 moves from CECBus node-1 to CECBus living-room

In `Control` the adapter claims a logical address as a playback
device. It announces the `Display`'s physical address and the OSD
name `liken`, which the TV shows in its list of sources. To show
another name, set `spec.osdName` to 1 to 14 printable ASCII
characters. When the name changes, the adapter releases its logical
address and claims it again, so the TV sees the source leave and come
back.

The adapter scans the bus once when it joins. The scan polls every
logical address and asks each device that answers for its name,
vendor, CEC version, and power. After the scan, the adapter sends
nothing on a timer, because a TV can react to a repeated scan by
switching its own input. The adapter keeps its device list current
from the messages it receives, and it queries a device only when that
device announces itself.

`kubectl get cecbuses -o wide` shows the result:

    NAME          MODE      NAME    MACHINES   JOINED   COHERENT   AGE   SCANNED   STATE     DEVICES
    living-room   Control   liken   node-1     True     True       2m    True      Scanned   TV,AVR-X1700H

When the bus does not reach that state, the conditions say what is
wrong:

| Condition | `False` means | Correction |
| --- | --- | --- |
| `AddressKnown` | The reason `NoAddress`: the adapter has no physical address from its `Display`. | Read the `Display`'s `status.physicalAddress`, and check the name in `spec.adapters[].display`. |
| `Joined` | The reason `NoLogicalAddress`: the adapter has no logical address yet. | Read `status.adapters[].state` and `message`. When the kernel refused a call, the message has the kernel's error text. |
| `Coherent` | The reason `Apart`: two adapters of the bus do not see each other. | The adapters are on different CEC wires. Check each adapter's cable. |
| `Scanned` | The reason `NoAnswer`: no device answered the adapter's polls. | Connect another HDMI cable between the adapter and the receiver. |

The node workload writes its entry under `status.adapters` only when
the entry changes, so `reportedAt` is the time of the last change, and
an old time is normal on a quiet bus. To decide whether an entry is
current, the operator checks the node workload's pod. Each condition
is `Unknown` with the reason `Stale` when no `equipment-operator-cec`
pod runs on the entry's machine, or when that pod stopped being
`Ready` after it wrote the entry. A new pod writes its first entry before it is `Ready`, so the operator
counts that entry as current for 60 seconds, and as `Stale` after that
if the pod is still not `Ready`. The message says which case applies.
When a node workload stops and writes a last entry, each condition is
`False` with the reason `Stopped`, whatever its pod shows.

Each node workload also reads these pods to decide which adapter sends
the bus's commands. When a node workload cannot read the pods, it logs
the failure and treats every other adapter's entry as current until a
read works. While the read fails, an adapter can leave the commands to
an adapter that comes before it in `spec.adapters`, even when that
adapter's pod is gone. The
`Deployment` writes no status while it cannot read the pods. The error
in either log says that the `Role` `equipment-operator-cec-pods` must
grant the read. To list the pods:

    kubectl get pods -n liken-system -l app=equipment-operator-cec -o wide

The [`CECBus`](/docs/reference/cecbuses/) reference describes every
field.

## Adopt the Television

When a `CECBus` in `Control` finds a TV, the operator creates a
`Television` with the bus's name and the
`equipment.liken.sh/discovered` label:

    kubectl get televisions

    NAME          BUS           POWER     DISPLAY   PLAYER   REACHABLE   AGE
    living-room   living-room   Standby                      True        1m

`status.displays` lists the `Display` of each adapter of the bus, and
its `via` field is the `Receiver` that the picture passes through. The
operator sets `via` when an input of a `Receiver` lists the
`Display`'s machine and the `Display` as its monitor, and that
receiver is between the TV and the `Display` in the HDMI tree:

    kubectl get television living-room -o jsonpath='{.status.displays}' | jq

To keep the `Television` in your configuration, apply your own under
the bus's name:

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Television
metadata:
  name: living-room
spec:
  cec:
    bus: living-room
```

Your object keeps the `equipment.liken.sh/discovered` label, and the
operator does not delete it while it is the only `Television` for the
bus. You can also apply a `Television` for the same bus under another
name, and the operator then deletes the discovered one.

Leave `spec.power` out unless you want to turn the TV on or off by
editing the object. The node workload applies each generation of the
spec once, so an object that you create with `spec.power`, or an edit
that adds it, turns the TV on or off once. Quote the value, as
`power: "On"`, because YAML reads an unquoted `On` as the boolean
`true`. The
[`Television`](/docs/reference/televisions/) reference describes the
rules.

`Reachable` is `True` while the TV answers requests for its power
status. A TV in deep standby or an eco mode can stop answering. Then
`Reachable` is `False` with the reason `NoPowerStatus`, `status.power`
is empty, and the operator cannot wake that TV.

## Wake the TV with the room

`media-operator` writes a `Player`'s session into `status.session` of
the `Receiver` that the `Player` plays through. The session wakes the
room when a `Play` starts, when the `Player`'s screen wakes, when the
remote's power button turns the room on, and when the session first
appears with a `Play` or its screen already on. For each wake, the
operator finds the `Television` whose `status.displays` lists the
session's `Display`, and writes a new `status.session.wokeAt` on it.

For each new `wokeAt`, the adapter that speaks for that `Display` runs
one wake:

1. It reads the TV's power, and sends Image View On until the TV
   reports On. One wake sends at most three of these commands.
2. When the TV reports On, or when the three commands end without it,
   the adapter sends Image View On and Active Source for the
   `Display`'s physical address. The TV and the receiver switch to the
   machine's input.
3. For 30 seconds, it guards the input. When another source claims the
   input with Active Source, the adapter waits 2 seconds and claims it
   again, at most twice for one wake.
4. At the end of the guard, it reads the TV's power again and writes
   the `WakeApplied` condition.
5. When the `Display` has a `via`, it asks that receiver for System
   Audio Mode and writes the `SystemAudioApplied` condition. The next
   section describes this step.

The guard is there because a streaming player on another receiver
input can claim Active Source while the room wakes, and the receiver
then switches to the player. The guard's Active Source switches the
receiver back to the machine.

The guard does not override a person. When someone picks another input
on the receiver or in the TV's source menu, the guard ends at once.
The exception is the first 10 seconds after the wake's first claim. A
TV that has just come on can switch to another input by itself, or
because a streaming player that woke with it asked, and it sends the
same message that its source menu sends. During those 10 seconds the
guard continues and takes the input back.

`kubectl get televisions -o wide` shows the wake:

    NAME          BUS           POWER   DISPLAY              PLAYER              REACHABLE   AGE   SOURCE    SESSION              WAKE        STANDBY   APPLIED
    living-room   living-room   On      don-0070-denon-avr   house/living-room   True        1h    1.3.0.0   don-0070-denon-avr   Confirmed

`WakeApplied` is `True` with the reason `Confirmed` when the TV
reported On and the last Active Source on the bus is the `Display`'s.
The other reasons:

* `Unconfirmed`: the TV did not report On.
* `SourceTaken`: another source had the input at the end of the
  guard. When this repeats, the other device claims the input after
  the guard ends, so check its CEC settings.
* `Chosen`: a person picked another input during the guard.
* `Refused`: the adapter could not send.
* `Stopped`: the session went to sleep, the bus changed, or the node
  workload stopped, and the wake ended.
* `Superseded`: a new generation of `spec.power`, or an Active Source
  from another adapter of the bus, ended the wake.
* `TooLate`: the wake had not started 2 minutes after the node
  workload first saw it, so it sent nothing.

For a press of the room remote's home key, `media-operator` writes the
action `show` into `status.session.inputAsk`. The adapter then sends Image View On and
Active Source for the `Display`, without reading the TV's power first,
and the operator selects the session's input on the receiver if the
receiver is on. A home press on a `Player` that is off turns the
`Player` on and writes no `show`, so a `show` comes only from a
`Player` that is on, and its Image View On can wake a TV in standby.

While the `Receiver`'s `InputSelected` condition is not `True`,
`media-operator` writes the action `ensure` for a press of any other
key. It writes nothing for a power key or for a press that turns a
`Player` on. For an `ensure`, the operator
selects the session's input on a receiver that is on, and sends the TV
nothing.

While the session's `awake` flag is true, the adapter also replies to these
messages from other devices, for the session's `Display` only:

* A TV that comes out of standby broadcasts Request Active Source. The
  adapter replies with Active Source, so the TV shows the `Player`
  instead of its own home screen. When a person switched to
  another source after the adapter's last claim, the adapter does not
  reply, and leaves the request to that source.
* A TV whose source menu a person uses broadcasts Set Stream Path with
  an input's physical address. The adapter replies with Active Source
  when that address is the `Display`'s.

A restart of the operator wakes nothing. Both workloads read the
sessions that exist when they start, and send nothing for them.

## Let the TV remote set the receiver's volume

While a receiver's System Audio Mode is on, the TV mutes its own
speakers and sends the volume keys of its remote to the receiver
(HDMI-CEC 1.3a, CEC 13.15.2). While it is off, the TV remote changes
the TV's own volume, which you do not hear when the sound plays on the
receiver. A receiver that `equipment-operator` turns on over the network comes up
with the mode off, so a wake through a receiver ends with a request
for the mode.

The adapter sends System Audio Mode Request only when the session's
`Display` has a `via` in the `Television`'s `status.displays`. You
configure nothing for it. The request goes out once for each wake,
after the guard, and only when the last Active Source on the bus is
the `Display`'s. It carries the `Display`'s physical address, so the
receiver also switches to the input for that address, and it can wake
the receiver. The adapter does not send the request on a timer, for a
home press, or after a person picked another input, and it never
requests the TV's own sound. When you pick the TV's speakers in its
menu after the wake, the TV ends the mode, and the operator does not
turn it on again.

Before you rely on the request, check two settings on the receiver:

* HDMI control (CEC) is on. A receiver with HDMI control off does not
  answer, and `liken` never turns the setting on.
* The receiver's own CEC input for the `Display`'s physical address
  is the input that the `Receiver`'s `spec.inputs` selects. The
  receiver maps the address through its own table, and a receiver
  whose table names another input switches the room away from the
  `Display`.

A receiver that is connected to the TV only by an optical cable is not
on the CEC bus, because an optical cable carries no CEC. The `Display`
then has no `via`, and the wake sends no request. The TV's own
sound-output setting sends its sound to the receiver.

After the request, the adapter waits 5 seconds for the receiver to
broadcast Set System Audio Mode On. The receiver sends its first On
to the TV only, so the adapter can miss it. When no broadcast arrives,
the adapter asks the receiver once with Give System Audio Mode Status.
The `SystemAudioApplied` condition on the `Television` reports the
result:

* `Confirmed`, `True`: the receiver broadcast On after the request, or
  answered On.
* `Unconfirmed`, `False`: the receiver answered Off, or did not
  answer. A TV that does not support System Audio Mode refuses the
  receiver's On, and the receiver stays Off. A receiver with HDMI
  control off does not answer.
* `Refused`, `False`: the receiver refused the request with a Feature
  Abort, or the adapter could not send it.

The condition is absent after a wake whose `Display` has no `via`,
and after a wake that sent no request.

The `Receiver` that is the `via` reports the receiver as the bus
shows it, in `status.cec`:

    kubectl get receiver living-room -o jsonpath='{.status.cec}' | jq

```json
{
  "bus": "living-room",
  "logicalAddress": 5,
  "physicalAddress": "1.0.0.0",
  "osdName": "AVR",
  "vendor": "0005cd",
  "cecVersion": "1.4",
  "systemAudioMode": "On",
  "systemAudioModeAt": "2026-10-06T18:04:07Z"
}
```

The adapter reads the mode once when it joins the bus. After that, each
broadcast of the mode updates it, such as the Off that a receiver
broadcasts before it goes to standby. `systemAudioModeAt` is when an
adapter last read or received the mode. Each change of the mode posts
an `Event` on the `Receiver`, `SystemAudioModeOn` or
`SystemAudioModeOff`. Both are Normal, because the mode turns off in
ordinary use: the receiver turns it off before standby, and a person
turns it off by picking the TV's speakers.

The receiver has one volume, whether the TV remote sets it over CEC or
the `liken` remote sets it over the network. The `Receiver`'s
`status.zones` reports that volume, so it also shows changes that
`liken` did not make.

## Turn the room off with the power button

The room remote's power button turns the whole room on or off, the
TV included. `media-operator` reads the press and sets the `Player`'s
`spec.power`, which you can also set with `kubectl`
([Turning a Player off and on](https://liken.sh/media/docs/reference/players/#turning-a-player-off-and-on)).
When the `Player` turns off, `media-operator` writes the action `off`
into `status.session.powerAsk`. When the `Player` turns on, its screen
wakes, the session's `awake` flag becomes true, and the room wakes as
described above.

A `powerAsk` has the action `toggle`, `on`, or `off`. `media-operator`
writes `off`, and other programs that write the session can write
`toggle` and `on`. A `toggle` always changes the room's state, an `on`
changes it only when the room is off, and an `off` only when the room
is on. A `powerAsk` that changes nothing sends nothing, and the
operator logs one line that says so. Nothing polls the TV's power, so
for each `powerAsk` the operator asks the node workload to read it,
waits up to 3 seconds for the answer, and uses `status.power` when no
answer arrives. A TV that reports
On or ToOn means the room is on, and a TV that reports Standby or
ToStandby means the room is off. With no `Television`, or a TV that
reports no power, the receiver's power decides.

A `powerAsk` that turns the room off writes `status.session.standbyAt`
on the `Television`. The adapter sends the TV Standby, reads the power
back, and writes the `StandbyApplied` condition. The operator also puts
the receiver in standby when the receiver has a standby command. A
WiiM has none, so it stays on, and the log says so.

Only a `Player` that turns off turns the TV off. A `Play` that ends, a
screen that goes idle, and a restart of the operator leave the TV as it
is. The TV can show another input, such as a streaming player, while
the room's `Player` is idle.

The TV can also wake and sleep the `Player`'s screen. When a person
picks the `Player`'s input in the TV's source menu while the screen
sleeps, the screen wakes. When a Standby appears on the bus while the
session's `awake` flag is true, the screen sleeps, unless something
plays. A TV
sends Standby when a person turns it off with its own remote, and some
TVs send nothing then. The adapter writes each such request in the
`Television`'s `status.screenAsk`, and `media-operator` passes it to
the `Player`.

## Read the TV remote's keys

The kernel turns the TV remote's buttons into key events on the
adapter's input device, but only while the adapter has a logical
address, so the bus must be in `Control`. The arrow keys, OK,
and back arrive as `KEY_UP`, `KEY_DOWN`, `KEY_LEFT`, `KEY_RIGHT`,
`KEY_OK`, and `KEY_EXIT`, and `media-operator` binds all six.

`equipment-operator` does not read the keys. A media `Remote` claims
the adapter's `-input` device, the same way it claims a Bluetooth
remote. A `Remote` refers to a `DeviceClass` that you write. This class
selects every TV remote input device that `liken` publishes:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: cec-remote
spec:
  selectors:
    - cel:
        expression: |
          device.driver == "liken.sh" &&
          has(device.attributes["liken.sh"].subsystem) &&
          device.attributes["liken.sh"].subsystem == "input"
```

Declare the `Remote` in the `Player`'s namespace:

```yaml
apiVersion: media.liken.sh/v1alpha1
kind: Remote
metadata:
  name: living-room-tv
  namespace: house
spec:
  device:
    class: cec-remote
```

On a cluster with more than one adapter, add a `selector` on the
device's `address` attribute, so that the `Remote` claims the input
device of one adapter. Then list the `Remote` in the `Player`'s
`spec.remotes`. The `media-operator` guide
[Map a new controller](https://liken.sh/media/docs/guides/mapping-a-controller/)
shows how to find the codes that the other buttons send and map them.

When the adapter is unplugged, the `equipment-operator-cec` pod exits,
and the kubelet starts it again when the adapter returns to the same
USB port. The `Remote`'s pod keeps running but reads nothing, because
a running container does not get the new event node. Delete the pod,
and `media-operator` creates it again with the new node:

    kubectl delete pod -n house living-room-tv-remote

## When the TV does not wake

Check these objects in order, and stop at the first one that is
wrong:

1. The `Machine`'s `status.serio`: the adapter is `Attached`, and its
   nodes exist.
2. The `Display`'s `status.physicalAddress`: the machine's position in
   the tree.
3. The `CECBus`'s conditions and `status.adapters`: the adapter has a
   logical address and announces the physical address.
4. The `Television`'s `Reachable` condition and `status.power`: whether
   the TV answers, and the power state it reports.
5. The `Television`'s `status.session`, `status.wokeAt`, and
   `WakeApplied`: whether the session reached the TV, whether the
   adapter ran the wake, and what the bus reported at the end of it.
   When the TV woke and its remote's volume keys still change the TV's
   own volume, read `SystemAudioApplied` and the `Receiver`'s
   `status.cec`.

A `Television` with no `status.session` gets no wake. Check that the
`Receiver`'s input names the same `Display` as the `CECBus`'s adapter,
and that the `Television`'s `status.displays` lists it. A TV that is
connected straight to the machine, with no `Receiver`, has no session,
and it does not wake with the room.

Each change of a condition of a `CECBus` or a `Television` posts one
Kubernetes `Event` with the condition's reason and message, so
`kubectl describe` shows what changed in the last hour. The `Event` is
a Warning when a person may need to act on the reason: `NotReported`,
`Refused`, `NoAddress`, `NoLogicalAddress`, `Apart`, `NoAnswer`,
`Stale`, `NoBus`, `NotFound`, `NoPowerStatus`, `Unconfirmed`,
`SourceTaken`, and `TooLate`. Every other reason is Normal. When
discovery creates or deletes a `Television`, the operator posts
`TelevisionCreated` or `TelevisionDeleted` on its `CECBus`. A change of
the System Audio Mode posts `SystemAudioModeOn` or
`SystemAudioModeOff` on the `Receiver`. A `CECBus` and a `Television`
are cluster-scoped, so their `Event`s are in the `default` namespace,
and `kubectl events` finds them only with `-n default` or `-A`:

    kubectl describe television living-room
    kubectl events -n default --for television/living-room

The API server deletes an `Event` one hour after its last change.
After that, read the conditions and the logs.

The node workload writes one log line for each wake, standby, and
power read. It also writes one line for each message that affects the
room, such as Active Source, Routing Change, Image View On, or
Standby:

    kubectl get pods -n liken-system -l app=equipment-operator-cec -o wide
    kubectl logs -n liken-system <pod on node-1>

## Check the wire by hand

[`cec-ctl`](https://www.mankier.com/1/cec-ctl), from
[v4l-utils](https://git.linuxtv.org/v4l-utils.git), checks the cable
and the tree without the operator. The adapter can be allocated to only
one claim at a time, so first stop the node workload on the machine:

    kubectl label node node-1 equipment.liken.sh/cec=none

Run a pod on the same machine that claims the adapter through the same
`ResourceClaimTemplate`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: cec-check
  namespace: liken-system
spec:
  nodeSelector:
    kubernetes.io/hostname: node-1
  tolerations:
    - operator: Exists
  containers:
    - name: cec
      image: debian:sid-slim
      command: ["sh", "-c", "apt-get update -qq && apt-get install -qq -y v4l-utils && exec sleep infinity"]
      securityContext:
        capabilities:
          add: ["NET_ADMIN"]
      resources:
        claims:
          - name: adapter
  resourceClaims:
    - name: adapter
      resourceClaimTemplateName: cec-adapter
```

Claim a playback address with the `Display`'s physical address, list
the tree, and ask the TV for its power:

    kubectl exec -n liken-system cec-check -- cec-ctl --playback --osd-name liken --phys-addr 1.3.0.0
    kubectl exec -n liken-system cec-check -- cec-ctl --show-topology
    kubectl exec -n liken-system cec-check -- cec-ctl --to 0 --give-device-power-status

A topology that lists only the adapter, and a power request that the
TV does not acknowledge, mean that the cable has no CEC wire. To watch
all traffic on the wire, run `cec-ctl --monitor-all`, which needs the
`NET_ADMIN` capability that the pod above adds. To wake the TV by hand,
send `cec-ctl --to 0 --image-view-on`.

When you are done, release the adapter, delete the pod, and remove the
label, so that the node workload starts again:

    kubectl exec -n liken-system cec-check -- cec-ctl --clear
    kubectl delete pod -n liken-system cec-check
    kubectl label node node-1 equipment.liken.sh/cec-
