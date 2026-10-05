# The equipment-operator design

## The problem

A liken machine plays a film through a cable. Equipment that the
machine does not own connects to the far end of that cable: an A/V
receiver, a television, a projector, or an amplifier. The machine sees
some of it through the cable. An HDMI receiver sends an EDID, so the display
operator and the audio operator both publish it under one monitor id.
The cable carries no control. The receiver's volume, its power, and
which input it shows come from its own remote or from a control
socket on the network. Intel HDMI ports have no CEC, so a liken box
has no in-band way to reach any of it. Plan 09 adds that path through a
USB CEC adapter on one of the cluster's machines.

The first case is a Denon AVR in a living room. The room's remote
should turn the receiver's volume, not a software gain in mpv, and a
`Play` should wake the receiver and select the right input.

## The boundary

`equipment` is anything a `Player` needs to reach the room and that
liken does not host. A receiver, a television, a projector, an
amplifier, an HDMI switch. Each is a kind under `equipment.liken.sh`,
and each kind has one-of protocol blocks, the shape `MetadataProvider`
uses in `library-operator`. The first kind is `Receiver`, with the
`denon` and `wiim` protocols. Plan 09 adds `CECBus`, one HDMI tree's
CEC wire, and `Television`, the TV at the root of that tree.

The operator is not a second Home Assistant. Lights, blinds, a
thermostat, and a motorized screen stay with Home Assistant, which
reacts to the media bus. A network stream target, such as a Chromecast
or a Sonos, is not equipment either. Equipment is connected to the
cable and never receives the stream over the network.

The name says nothing about the wire on purpose. A receiver with an
RS-232 port, or a television behind an IR blaster, is the same
equipment over a different link. This design builds none of that, and
it chooses no shape that forbids it.

## The `Receiver`

`Receiver` is a cluster-scoped CRD. It records how to reach the
equipment, how it is wired to liken machines, and the session a
`Player` currently uses.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: den
spec:
  denon:
    address: den-avr.home.example
  volume:
    max: 72
    step: 0.5
  inputs:
    - name: GAME
      machine: node-3
      monitor: don-0070-denon-avr
status:
  driver: denon
  zones:
    main:
      power: "On"
      input: GAME
      volume: "55.5"
      soundMode: MULTI CH IN
  session:
    player: media/den
    input: GAME
    active: true
    awake: true
    volumeAsk: {level: 55.5, at: "2026-10-04T12:15:25.164Z"}
  conditions:
    - type: Reachable
      status: "True"
      reason: Connected
    - type: InputSelected
      status: "True"
```

### The wiring

`spec.inputs` is the fact nothing can discover: which machine's HDMI
lands on which input. The receiver knows its input names but not who
is plugged into them. The monitor id alone cannot name an input,
because a receiver forwards one EDID on every input, so two machines
into one receiver both see `don-0070-denon-avr`. Every entry names the
machine.

The monitor id is the check that the wire is really there. The
display operator's `Display` and the audio operator's `Sink` both
carry it, and the media operator already resolves a `Player`'s screen
to a node and a monitor id. Those two values find the input.

### The session

`status.session` is the media operator's block. It applies it with
server-side apply under its own field manager, and it lifts it when the
unit goes away. The cluster owner never writes it. The session is in
status and not in spec because a GitOps repository owns the spec, and
a held volume key would otherwise change `metadata.generation` several
times a second. The operator still reads `spec.session` when the
status holds none, for a media operator that wrote it there.

Each time `active` or `awake` turns on, the operator powers the
receiver on and selects the input. The session also carries the
unit's asks: `volumeAsk`, `powerAsk`, and `inputAsk`. Each ask carries
the time it was made, and a new time is a new ask. The media operator keeps the
session whenever the Player has the screen, including the idle screen.
It sets `active` while a Play is present and `awake` while the room's
screen is on, which the remote's power key controls. A volume press at
the idle screen changes the receiver's volume. The power key wakes the receiver
and brings the browser up. An idle screen that comes up after a reboot
does not wake the receiver. Power and input are one-shot commands. The
operator sends them when a flag turns on and never holds them. If a
person selects another input on the receiver's remote, the status
records it and the operator does not change it back. A person using the
equipment can therefore override the cluster. The operator does not act
on a flag or an ask that it finds in its first pass after a start.

### The status

The status is what the receiver last said, in the receiver's own
units, under `status.zones`. A Denon reports its volume as a number
from 0 to 98 in half steps. `volumeMax` is the last `MVMAX` line it
sent, recorded and never acted on. `InputSelected` is true while the
receiver reports the session's input. `Reachable`
is true only after a recent round trip, never on an open socket
alone: a half-open socket after a router reboot reads as live and
swallows writes.

## The level

Each value has one writer, and the layers meet only through
Kubernetes objects
([plan 77](../../plans/completed/77-the-media-bus-stops-at-media-operator.md)).
The media operator turns presses into levels. The equipment operator
is the only writer of what the receiver reports. It connects to no
message bus.

A press is a direction, not a level. The media operator reads each
volume key from the bus and moves a pending target one
`spec.volume.step` in the receiver's own units. With no target
pending, it moves from the receiver's reported level. It writes the
target into `status.session.volumeAsk` as an absolute level, at most
once for each pacing interval. The equipment operator sends each new
ask to the receiver, and when asks arrive faster than the receiver
takes them, it sends only the newest.

`spec.volume.max` is as loud as this room ever goes, and an ask never
moves the target above it. The ceiling is a fact the owner states. A
Denon reports an `MVMAX` line beside every volume, and that number
moves with the volume, so nothing reads it as a limit. Mute is the
receiver's own mute.

The media operator relays the receiver's report to the Player's
`volume` topic, divided by `spec.volume.max`, so the screens draw the
level. A report that arrives while no target is pending is a turn of
the receiver's knob. The media operator publishes it, and the next
press steps from it. `spec.volume.indicator` names who draws the
indicator: the Player's screens, which is the default, or the
receiver's own overlay on the TV.

mpv always plays at unity. When the receiver's `Reachable` condition
is not `True`, the media operator sends the next press to the unit's
`Sink` objects instead, through the same kind of ask. A dead receiver
degrades to the sinks, not to a deaf room.

## The Service front

Older Denon receivers accept one control connection on port 23, and
Home Assistant holds that connection in a house that runs it. An
AVR-X1700H measured on 2026-09-07 answered a second client in full
while Home Assistant held the first, so the first plan connects
directly and leaves Home Assistant alone. The front below is the
second plan, for receivers that enforce the one-client rule, and for
a house that wants one name for the receiver.

The operator owns the socket and makes a Service named for the
`Receiver`. The Service represents the receiver on the network.

Ports 8080, 60006, and 1255 pass straight through to the receiver's
own address: the Service has no selector, and an EndpointSlice the
operator writes points those ports at the receiver's IP. Port 23
points at the operator pod, which multiplexes. Every line a client
sends goes to the receiver. Every line the receiver sends goes to
every client and to the operator's own parser. Home Assistant points
at the Service and keeps its push updates, and the operator sees what
every client asks for, so the status reflects the whole house.

Only the Denon protocol needs the front. A protocol with no
one-client rule, such as a WiiM's HTTP API, makes no Service.

## The media operator's part

The media operator resolves a `Player`'s display to a node and a
monitor id, and looks for a `Receiver` input that matches both. It
applies the session for as long as the match holds. It sets `active`
while a `Play` stands on the unit, and `awake` while the unit's screen
is on. When the unit goes away, the session is lifted.

The media operator also writes the asks. A volume key becomes a
`volumeAsk`. A power key on the Player's `power` topic becomes a
`powerAsk`, which the equipment operator resolves against the room's
TV and receiver. A key that needs the unit's input becomes an
`inputAsk`, written only while `InputSelected` is not `True`. A TV
that reports a person picking the unit's input writes
`Television.status.screenAsk`, and the media operator relays it to
the Player's `power` topic.

A `Player` with no matching `Receiver` sets its level through its
`Sink` objects. A Bluetooth speaker carries no monitor id, so it never
matches a `Receiver`.

## The plans

The first two plans of this design were the `Receiver` with the
`denon` protocol, the session, and the level path, and the Service
front. The first is built. The Service front is not built, and plans
02 and 04 keep the number 03 for it. No document exists under that
number. Each later plan has its own number in this directory:

- [01](completed/01-prometheus-metrics.md), the Prometheus metrics.
- [02](completed/02-denon-driver.md), the Denon driver and the full
  receiver mirror.
- [04](completed/04-declarative-settings-and-bus.md), the declarative
  settings. Plan 77 removed its bus controller.
- [05](completed/05-the-operator-on-the-host-network.md), the operator
  on the host network.
- [06](completed/06-wiim-driver.md), the WiiM driver.
- [07](completed/07-receiver-http-and-tv-wake.md), the receiver's HTTP
  interface and the TV wake.
- [08](completed/08-the-wiim-event-path.md), the WiiM's event path.
- [09](completed/09-cec.md), the `CECBus` and the `Television` over
  HDMI-CEC.
- [10](completed/10-the-watches-use-client-go.md), the watches on
  client-go.
- [11](completed/11-one-operator-holds-the-lease.md), leader election.
- [12](completed/12-network-discovery-can-be-turned-off.md), network
  discovery that can be turned off.
- [13](13-every-setting-a-receiver-exposes.md), every setting a
  receiver exposes. Not built.

[Plan 77](../../plans/completed/77-the-media-bus-stops-at-media-operator.md),
at the top of the repository, moved the level, power, and input asks
from the bus to the `Receiver`'s status.

## What this design leaves for later

- A room with picture on the receiver and sound on a Bluetooth
  speaker. Plan 77 gives a `Sink` its own session and `volumeAsk`, but
  it does not cover this room. `volumeDevices` in
  `media-operator/volumedevices.go` chooses the `Receiver` whenever the
  unit's screen matches a reachable one, and the sinks only otherwise.
  So in this room a volume press moves the receiver, and the speaker
  that plays the sound does not change. Power and input still belong
  to the receiver, which the display finds. The level would have to
  come from the sinks: two lookups instead of one.
- HEOS as a second protocol block on a Denon, on port 1255, for music.
- A `Projector` kind. PJLink would be the first protocol that is a
  standard and not a brand.
- Equipment reached over a serial port or an IR blaster, which needs
  a pod on a node and not only a Deployment.
