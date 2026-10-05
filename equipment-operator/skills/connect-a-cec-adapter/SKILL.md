---
name: connect-a-cec-adapter
description: "Connect a USB-CEC adapter to a liken machine and to a room's HDMI tree, take its CECBus from Listen to Control, adopt the Television, and let the room's Player wake the TV and turn it off. Use when a TV must wake with a Player, when a TV remote must drive a Player, or when a CECBus or a Television reports a fault."
---

This skill is the guide at https://liken.sh/equipment/docs/guides/connect-a-cec-adapter/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

This guide follows one room from a new USB-CEC adapter to a TV that
wakes with the room's `Player`. At the end, the adapter speaks for the
machine on the HDMI-CEC wire. A Play or a press of the room remote's
power button wakes the TV and switches it to the machine. The power
button also turns the TV off, and the TV's own remote drives the
`Player`.

The room in this guide has a TV, an AV receiver on one of the TV's
inputs, and the machine `node-1` on one of the receiver's inputs. The
first adapter `equipment-operator` supports is the Pulse-Eight
[USB-CEC adapter](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter).
The Linux kernel drives it with its `pulse8-cec` driver, and the
kernel's
[CEC admin guide](https://docs.kernel.org/admin-guide/media/cec.html)
describes that driver and the serial-line attachment it needs.

You need:

* `equipment-operator`, from the [install](https://liken.sh/equipment/docs/guides/install/)
  guide, with `cec.yaml` applied.
* The [`display-operator`](https://liken.sh/display/), which
  publishes the machine's HDMI output as a `Display` with its CEC
  physical address.
* For the wake and the power press: a `Receiver` whose input names the
  machine and that `Display`, under a `Player` of the
  [`media-operator`](https://liken.sh/media/). The install guide
  declares that `Receiver`.
* A spare HDMI input on the receiver, and an HDMI cable that carries
  the CEC wire.
* `kubectl` with cluster-admin access, because the guide edits a
  `Machine` and creates a `DeviceClass`.

## Connect the adapter

Connect the adapter's HDMI output to a spare input of the receiver.
Leave the adapter's own HDMI input empty. Connect the adapter's USB
plug to the machine. The machine's video stays on its own cable to its
own receiver input.

The adapter carries no video in this setup. It needs no video,
because it announces the physical address of the machine's video
input, not of its own port. CEC is one wire that every HDMI port of
the tree shares, so the adapter's port does not matter to the other
devices. Pulse-Eight documents this
[two-cable setup](https://support.pulse-eight.com/support/solutions/articles/30000022906-usb-cec-adapter-4k-resolution)
for 4K, because the adapter passes 4K at 60 Hz only in 4:2:0 color.

A drill measured this setup with an empty adapter input. A scan from
the adapter found the TV, the receiver, the adapter itself, and a
streaming player on another receiver input. A second cable from the
machine into the adapter's input changed nothing in the scan.

Many HDMI cables leave out the CEC wire, and such a cable fails with
no error of its own. In the drill, the first cable from the adapter to
the receiver carried no CEC. Every message from the adapter went
unacknowledged, and the adapter heard no traffic at all. Another
cable fixed it. When the `CECBus` below reports that no device
answers, try another cable first.

## Attach the adapter to the machine

A `liken` machine loads only the drivers its `Machine` names, and the
adapter's driver binds only after the machine attaches the adapter's
serial line. Declare three modules in `spec.modules`, in this order,
and one `spec.serio` entry:

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

Keep the modules the machine already declares in the list. A merge
patch of `spec.modules` replaces the whole list. An added module and
an added entry take effect without a reboot. The `liken` guide
[Load the drivers for a machine's hardware](https://liken.sh/docs/guides/hardware-modules/#attach-a-usb-cec-adapter)
explains each field, and the
[`Machine`](https://liken.sh/docs/reference/machine/#spec--serio)
reference describes them.

Confirm the attachment:

    kubectl get machine node-1 -o jsonpath='{.status.serio}' | jq

`Attached` is the good state, and `nodes` lists `/dev/cec0` and the
remote's event node. The machine then publishes two devices in its
`ResourceSlice`. The device whose name ends in `-cec` is the CEC bus,
and the `cec-adapter` `DeviceClass` of the operator selects it. The
device whose name ends in `-input` carries the TV remote's keys, and a
media `Remote` claims it at the end of this guide.

## Read the Display's physical address

Each device on a CEC bus has a physical address, which is its path
from the TV. The TV is `0.0.0.0`. A receiver on the TV's input 1 is
`1.0.0.0`, and a machine on that receiver's input 3 is `1.3.0.0`. The
adapter has no EDID to read its own address from. So the adapter
announces the address of the machine's `Display`, which the
`display-operator` reads from the EDID the receiver serves:

    kubectl get display don-0070-denon-avr -o jsonpath='{.status.physicalAddress}'

The output is the dotted address, such as `1.3.0.0`. The field is
absent on a DisplayPort cable, and while the monitor has never served
a valid address. The
[`Display`](https://liken.sh/display/docs/reference/displays/#status--physicaladdress)
reference describes the `PhysicalAddressCurrent` condition, which
says whether the address is current or retained.

## Listen to the bus

The `equipment-operator-cec` pod on `node-1` starts when the machine
publishes the `-cec` device. No `CECBus` names the machine yet, so the
pod creates one in `Listen`, named after the machine, with the
`equipment.liken.sh/discovered` label. The pod's log says so:

    no CECBus names machine node-1; created CECBus node-1 in Listen, which sends nothing on the wire

In `Listen` the adapter claims no logical address and sends nothing.
It opens the bus as a monitor and reports each device that sends a
message. The mode exists so that a new adapter does not change the
room before a person allows it. A device that joins a CEC bus can
wake the TV and switch its input.

Read what the adapter heard:

    kubectl get cecbus node-1 -o jsonpath='{.status.adapters}' | jq

Each entry under `devices` is one device the adapter heard. The list
is partial, because a full list needs polls, and a poll is a
transmission. On the drill's bus, the TV and a streaming player each
polled every logical address about every 15 seconds, so the list
filled within a minute. The `Scanned` condition is `False` with the
reason `Listening` while the adapter hears devices. It is `False`
with the reason `Silent` while the adapter has heard no device, and
the message then says that the cable may not carry the CEC wire.

The monitor needs the `CAP_NET_ADMIN` capability, and `cec.yaml`
grants the pod that one capability. `Control` needs none.

## Give the adapter control

`Control` is your consent. Declare a `CECBus` of your own, under a new
name, that names the machine and the `Display`:

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

A `CECBus` in `Control` must name a `display` for every adapter, and
the API server refuses one that does not. Do not apply your `CECBus`
under the discovered name, `node-1`. The node workload owns that
object's spec. Your `CECBus` names the same machine, so the node
workload follows yours and deletes the one it created:

    deleted the discovered CECBus node-1: CECBus living-room names machine node-1
    the adapter on node-1 moves from node-1 to living-room

In `Control` the adapter claims a logical address as a playback
device. It announces the `Display`'s physical address and the OSD
name `liken`, which the TV shows in its list of sources. To show
another name, set `spec.osdName` to 1 to 14 printable ASCII
characters. A new name makes the adapter release its address and
claim it again, and the TV sees the source leave and come back.

The adapter scans the bus once when it joins. The scan polls every
logical address and asks each device that answers for its name,
vendor, CEC version, and power. After the scan, the adapter sends
nothing on a timer. On one measured bus, a TV switched its own input
in reply to an adapter that scanned every minute. So the adapter
keeps its device list current from the messages it hears, and asks a
device questions only when the device announces itself.

`kubectl get cecbuses -o wide` shows the result:

    NAME          MODE      NAME    MACHINES   JOINED   COHERENT   AGE   SCANNED   STATE     DEVICES
    living-room   Control   liken   node-1     True     True       2m    True      Scanned   TV,AVR-X1700H

The four conditions say what is wrong when the bus does not reach
that state:

| Condition | `False` means | Correction |
| --- | --- | --- |
| `AddressKnown` | The reason `NoAddress`: the adapter has no physical address from its `Display`. | Read the `Display`'s `status.physicalAddress`, and check the name in `spec.adapters[].display`. |
| `Joined` | The reason `NoLogicalAddress`: the adapter holds no logical address yet. | Read `status.adapters[].state` and `message`. A refused call gives the kernel's text there. |
| `Coherent` | The reason `Apart`: two adapters of the bus do not see each other. | The adapters are on different wires. Check each adapter's cable. |
| `Scanned` | The reason `NoAnswer`: no device answered the adapter's polls. | Connect another HDMI cable between the adapter and the receiver. |

An adapter entry older than 90 seconds makes each condition `Unknown`
with the reason `Stale`. A node workload that stopped makes each
condition `False` with the reason `Stopped`. The
[`CECBus`](https://liken.sh/equipment/docs/reference/cecbuses/) reference describes every
field.

## Adopt the Television

When a `CECBus` in `Control` finds a TV, the operator creates a
`Television` with the bus's name and the
`equipment.liken.sh/discovered` label:

    kubectl get televisions

    NAME          BUS           POWER     DISPLAY   PLAYER   REACHABLE   AGE
    living-room   living-room   Standby                      True        1m

`status.displays` lists the `Display` the bus's adapter names, and
`via` names the `Receiver` the picture passes through. The operator
sets `via` when a `Receiver` input names the `Display`'s machine and
the `Display` as its monitor. The receiver must also be above the
`Display` in the tree:

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

The object keeps the discovered label, and the operator keeps it for
as long as no other `Television` names the same bus. A `Television`
under another name that names the same bus also works. The operator
then deletes the discovered one.

Leave `spec.power` out unless you want to drive the TV by an edit. The
node workload applies each generation of the spec once, and it never
applies a generation again. So an object that you create with
`spec.power`, or an edit that adds it, turns the TV on or off once.
Quote the value, as `power: "On"`, because `kubectl` reads an
unquoted `On` as the boolean `true`. The
[`Television`](https://liken.sh/equipment/docs/reference/televisions/) reference describes the
rules.

`Reachable` is `True` while the TV answers its power status. A TV in
a deep standby or an eco mode can stop answering. Then `Reachable` is
`False` with the reason `NoPowerStatus`, `status.power` is empty, and
the operator cannot wake that TV.

## Wake the TV with the room

The TV wakes with the receiver. A `Receiver` session wakes the room
when a Play starts on it, when its screen wakes, or when the remote's
power button turns the room on. A session that appears with a Play or
its screen already on also wakes the room. The operator then finds the `Television`
whose `status.displays` lists the session's `Display`, and writes a
new `status.session.wokeAt` on it.

For each new `wokeAt`, the adapter that speaks for that `Display` runs
one wake:

1. It reads the TV's power, and sends Image View On until the TV
   reports On. One wake sends at most three such commands.
2. When the TV reports On, or when those commands end without it, it
   sends Image View On and Active Source for the `Display`'s physical
   address. The TV and the receiver switch to the machine's input.
3. For 30 seconds, it guards the input. When another source claims the
   input with Active Source, the adapter waits 2 seconds and claims it
   again, at most twice for one wake.
4. At the end of the guard, it reads the TV's power again and writes
   the `WakeApplied` condition.

The guard exists because another source can take the input after a
wake. In the first drill, the receiver woke on a streaming player's
input, because the player claimed Active Source as the room woke. A
second Active Source, sent once the room was on, switched the receiver
to the machine. The adapter claims the input for a person and never
against one. A person who picks another input on the receiver or in
the TV's source menu ends the guard at once.

`kubectl get televisions -o wide` shows the wake:

    NAME          BUS           POWER   DISPLAY              PLAYER              REACHABLE   AGE   SOURCE    SESSION              WAKE        STANDBY   APPLIED
    living-room   living-room   On      don-0070-denon-avr   house/living-room   True        1h    1.3.0.0   don-0070-denon-avr   Confirmed

`WakeApplied` is `True` with the reason `Confirmed` when the TV
reported On and the last Active Source on the bus is the `Display`'s.
The other reasons:

* `Unconfirmed`: the TV did not report On.
* `SourceTaken`: another source held the input at the end of the
  guard. When this repeats, that source claims the input after the
  guard ends. Check the CEC settings of that device.
* `Chosen`: a person picked another input during the guard.
* `Refused`: the adapter could not send.
* `Stopped`: the session's sleep, a change of the bus, or a stop of the
  node workload ended the wake.
* `Superseded`: a new generation of `spec.power`, or an Active Source
  from another adapter of the bus, ended the wake.
* `TooLate`: the wake had not started 2 minutes after the node
  workload saw it, and it sent nothing.

An operator restart wakes nothing. Both workloads adopt the sessions
they find when they start, and send nothing for them.

## Turn the room off with the power button

The room remote's power button turns the whole room on or off, the
TV included. At each press, the adapter asks the TV for its power,
because no timer asks the TV between presses. The press waits up to 3
seconds for the answer, and it decides from `status.power` when no
answer arrives. A TV that reports On or ToOn means the room is on, so
the press turns the room off. A TV in Standby or ToStandby means the
room is off, so the press wakes the room as above. With no
`Television`, or a TV that reports no power, the receiver's power
decides.

A press that turns the room off writes `status.session.standbyAt` on
the `Television`. The adapter sends the TV Standby, reads the power
back, and writes the `StandbyApplied` condition. The receiver goes to
standby too, when it has a standby command. A WiiM has none, so it
stays on, and its log line says so.

Only the power button turns the TV off. A Play that ends, a screen
that goes idle, and an operator restart leave the TV as it is. The TV
can show another input, such as a streaming player, while the room's
`Player` is idle.

The TV can also move the `Player`'s screen. When a person picks the
`Player`'s input in the TV's source menu while the screen sleeps, the
screen wakes. When the bus carries a Standby while the session holds
the room awake, the screen sleeps, but only while nothing plays. A TV
sends Standby when a person turns it off with its own remote, and some
TVs send nothing then. The adapter writes each such ask in
the `Television`'s `status.screenAsk`, and the `media-operator`
relays it to the `Player`.

## Read the TV remote's keys

The kernel turns the TV remote's buttons into key events on the
adapter's input device. It does that only while the adapter holds a
logical address, so the bus must be in `Control`. In the first drill,
with the adapter as the active source, the arrows, OK, and back of
the TV's remote arrived as `KEY_UP`,
`KEY_DOWN`, `KEY_LEFT`, `KEY_RIGHT`, `KEY_OK`, and `KEY_EXIT`. The
`media-operator` binds all six. That TV's remote had no play or pause
key, so the drill did not test the transport keys.

`equipment-operator` does not read the keys. A media `Remote` claims
the adapter's `-input` device, the same way it claims a Bluetooth
remote. A `Remote` names a `DeviceClass` that you write. This class
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
device's `address` attribute, so the `Remote` claims one adapter's
input device. Then name the `Remote` in the `Player`'s
`spec.remotes`. The `media-operator` guide
[Map a new controller](https://liken.sh/media/docs/guides/mapping-a-controller/)
finds the codes the other buttons send and maps them.

When the adapter is unplugged, the `equipment-operator-cec` pod exits,
and the kubelet starts it again when the adapter returns to the same
USB port. The `Remote`'s pod keeps running and reads nothing, because
a running container never receives the new event node. Delete the
pod, and the `media-operator` creates it again with the new node:

    kubectl delete pod -n house living-room-tv-remote

## When the TV does not wake

"The TV did not wake" crosses five objects. Read each one in this
order, and stop at the first one that is wrong:

1. The `Machine`'s `status.serio`: the adapter is `Attached`, and
   its nodes exist.
2. The `Display`'s `status.physicalAddress`: the machine's place in
   the tree.
3. The `CECBus`'s conditions and `status.adapters`: the adapter holds
   a logical address and announces the address.
4. The `Television`'s `Reachable` condition and `status.power`: the
   TV answers, and what it reports.
5. The `Television`'s `status.session`, `status.wokeAt`, and
   `WakeApplied`: the session reached the TV, the adapter ran the
   wake, and what the bus reported at the end of it.

A `Television` with no `status.session` gets no wake. Check that the
`Receiver`'s input names the same `Display` as the `CECBus`'s
adapter, and that the `Television`'s `status.displays` lists it. A TV
that is connected straight to the machine, with no `Receiver`, has no
session to follow, and it does not wake with the room.

The node workload writes one log line for each wake, standby, and
power read. It also writes one line for each message that a person
notices, such as Active Source, Routing Change, Image View On, or
Standby:

    kubectl get pods -n liken-system -l app=equipment-operator-cec -o wide
    kubectl logs -n liken-system <pod on node-1>

## Check the wire by hand

[`cec-ctl`](https://www.mankier.com/1/cec-ctl), from
[v4l-utils](https://git.linuxtv.org/v4l-utils.git), checks the cable
and the tree without the operator. The adapter allocates to one claim
at a time, so first take the node workload off the machine:

    kubectl label node node-1 equipment.liken.sh/cec=none

Run a pod that claims the adapter through the same
`ResourceClaimTemplate`, on the same machine:

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
      image: debian:trixie-slim
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
TV does not acknowledge, point to a cable without the CEC wire. To
watch all traffic on the wire, which needs the `NET_ADMIN` capability
above, run `cec-ctl --monitor-all`. To wake the TV by hand, send
`cec-ctl --to 0 --image-view-on`.

When you are done, release the adapter, delete the pod, and remove the
label, so the node workload returns:

    kubectl exec -n liken-system cec-check -- cec-ctl --clear
    kubectl delete pod -n liken-system cec-check
    kubectl label node node-1 equipment.liken.sh/cec-
