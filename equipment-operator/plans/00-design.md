# The equipment-operator design

## The problem

A liken machine plays a film through a cable. On the far end of that
cable sits equipment the machine does not own: an A/V receiver, a
television, a projector, an amplifier. The machine sees some of it
through the cable. An HDMI receiver sends an EDID, so the display
operator and the audio operator both publish it under one monitor id.
The cable carries no control. The receiver's volume, its power, and
which input it shows come from its own remote or from a control
socket on the network. Intel HDMI ports have no CEC, so a liken box
has no in-band way to reach any of it.

The first case is a Denon AVR in a living room. The room's remote
should turn the receiver's volume, not a software gain in mpv, and a
`Play` should wake the receiver and select the right input.

## The boundary

`equipment` is anything a `Player` needs to reach the room and that
liken does not host. A receiver, a television, a projector, an
amplifier, an HDMI switch. Each is a kind under `equipment.liken.sh`,
and each kind has one-of protocol blocks, the shape `MetadataProvider`
uses in `library-operator`. The first kind is `Receiver` and the first
protocol is `denon`.

The operator is not a second Home Assistant. Lights, blinds, a
thermostat, and a motorized screen stay with Home Assistant, which
reacts to the media bus. A network stream target, such as a Chromecast
or a Sonos, is not equipment either: equipment sits on the cable and
never receives the stream over the network.

The name says nothing about the wire on purpose. A receiver with an
RS-232 port, or a television behind an IR blaster, is the same
equipment over a different link. This design builds none of that, and
it chooses no shape that forbids it.

## The `Receiver`

`Receiver` is a cluster-scoped CRD. It holds three things: how to
reach the equipment, how it is wired to liken machines, and the
session a `Player` currently holds on it.

```yaml
apiVersion: equipment.liken.sh/v1alpha1
kind: Receiver
metadata:
  name: living-room-denon
spec:
  denon:
    address: living-room-denon.home.example
  inputs:
    - name: GAME
      machine: utility1
      monitor: don-0070-denon-avr
  session:
    player: media/living-room
    input: GAME
    volumeTopic: liken/media/players/media/living-room/volume
status:
  power: "on"
  input: GAME
  volume: "55.5"
  volumeMax: "80"
  mute: false
  soundMode: MULTI CH IN
  service: living-room-denon
  conditions:
    - type: Reachable
      status: "True"
      reason: Connected
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

`spec.session` is the media operator's block. It applies it with
server-side apply under its own field manager, the way it applies
`spec.override` on a `Display` today, and it lifts it when the unit
goes away. The cluster owner never writes it.

A session means: power the receiver on, select the input, and own the
level from the named volume topic. Power and input are one-shots. The
operator sends them when the session appears or changes, and never
holds them. If a person selects another input on the receiver's own
remote, the status records it and nothing fights back. A hand on the
equipment outranks the cluster.

### The status

The status is what the receiver last said, in the receiver's own
units. A Denon reports its volume as a number from 0 to 98 in half
steps, and `volumeMax` is the limit set in the receiver's own setup
menu, which the Denon reports after every volume reply. `Reachable`
is true only after a recent round trip, never on an open socket
alone: a half-open socket after a router reboot reads as live and
swallows writes.

## The level

The media bus already carries the room's level. A press publishes the
next state, retained, on the `Player`'s volume topic, and every pod
for the unit applies what it reads. mpv is one subscriber of that
state and not its owner. The receiver is a second subscriber.

While a session stands, the operator subscribes to the volume topic
and sends the receiver a master volume for each state it reads. The
scale is 0 to 100 on the bus, and 100 means as loud as this room ever
goes: the operator maps 100 onto `volumeMax`, so the ceiling is set
on the receiver and read from it. Mute is the receiver's own mute.

The operator also publishes an owner mark, retained, on the topic
`<volumeTopic>/owner`, and registers an MQTT last will that clears
it. While the mark stands, mpv starts at unity, applies no level, and
draws no volume indicator. If the operator dies, the will clears the
mark and mpv takes the level back. A dead receiver degrades to
software gain, not to a deaf room.

One rule bends. The volume topic's rule is that no observer writes
back what it saw. While the mark stands, the receiver is the level's
owner and not an observer. When a person turns the receiver's knob,
the operator publishes the new position back to the topic, so the
retained state stays true and the next press steps from where the
knob left it.

## The Service front

Older Denon receivers accept one control connection on port 23, and
Home Assistant holds that connection in a house that runs it. An
AVR-X1700H measured on 2026-09-07 answered a second client in full
while Home Assistant held the first, so the first plan connects
directly and leaves Home Assistant alone. The front below is the
second plan, for receivers that enforce the one-client rule, and for
a house that wants one name for the receiver.

The operator owns the socket and makes a Service, named for the
`Receiver`, that stands in for the receiver on the network.

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
monitor id, as it does today, and looks for a `Receiver` input that
matches both. When a `Play` starts on that `Player`, it applies the
session before it creates the pod, and it hands the pod the owner
mark's topic. The command sidecar subscribes to the mark and gates
mpv on it. When the unit goes away, the session is lifted.

A `Player` with no matching `Receiver` works as it does today. A
Bluetooth speaker carries no monitor id, so it never matches.

## The plans

1. The `Receiver` with the `denon` protocol, the session, and the
   level path. Proved on the living room.
2. The Service front.

## What this design leaves for later

- A room with picture on the receiver and sound on a Bluetooth
  speaker. The level owner then comes from the sinks and power and
  input from the display, two lookups instead of one.
- The idle client draws the level today. It should honor the owner
  mark the way the sidecar does.
- HEOS as a second protocol block, on port 1255, for music.
- `Television` and `Projector` kinds. PJLink would be the first
  protocol that is a standard and not a brand.
- Equipment reached over a serial port or an IR blaster, which needs
  a pod on a node and not only a Deployment.
