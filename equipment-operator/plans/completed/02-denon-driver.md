# The Denon driver and the full receiver mirror

Plan 02. Built and drilled on the house cluster on 2026-09-19.

## The problem

The first plan built what one room needed: power on, input select,
volume, mute, and the level path. It reads five lines from the
receiver and ignores the rest. The AVR-X1700H in this house answers
about thirty more queries and volunteers many more events, so the
cluster cannot see the second zone, the sound mode it reads but never
sets, or any of the receiver's own settings.

The Denon code is also in the root package beside the controller,
so a second protocol such as a WiiM has nowhere to live. This plan
makes the protocol a driver with its own directory, makes the parser
cover every line the house's receiver emits, and makes the resource
carry the receiver's whole state.

## The design

### A driver is a directory

A protocol is a package under the repository root. `denon/` is the
first. It owns the socket, the command vocabulary, the line parser,
and the receiver's own units. Its `AGENTS.md` holds the protocol
references.

The controller talks to a protocol through one interface, defined in
`equipment/` because that is the package that consumes it. The
interface returns a state in the controller's own units and takes
commands in those units. `denon/` imports `equipment/`, never the
other way around, and `main` wires the two together. A second driver
is one more package and one more line in the wiring.

```
equipment/          the driver contract and the shared state model
  state.go          State, ZoneState, Driver
denon/              the Denon driver
  AGENTS.md         the protocol references and the model notes
  client.go         the socket, the queries, the heartbeat, the backoff
  protocol.go       commands out, lines in
  state.go          the Denon fields and their own units
  level.go          half-step and display-unit conversion
  testdata/         one captured transcript per model
main.go             package main: settings, wiring, serve
```

The controller, the session, the bus, the metrics, and the status
stay in the root package, which is the `Receiver` domain. The plan
moves files, not behavior.

### The shared state model

`equipment.State` is what any driver can report. It carries the
reachability verdict and one entry per zone.

```go
type Power string // "on", "standby", "off"

type ZoneState struct {
    Power     Power
    Input     string
    SoundMode string
    Mute      bool
    Volume    int // the driver's smallest step, -1 when unknown
    VolumeMax int // the driver's smallest step, -1 when unknown
    Sleep     int // minutes, -1 when unknown, 0 is off
}

type State struct {
    Reachable ConditionStatus
    Zones     map[string]ZoneState
}
```

A driver reports volume as a count of its smallest step and states
how many of those steps make one display unit. The Denon reports
half steps and states two per unit, so the bus ceiling and the press
step stay in the units a person reads, and the arithmetic in
`level.go` never learns the protocol.

The interface is what the controller calls. The zone arguments keep
a command directed at one zone, and a single-zone driver ignores the
name.

```go
type Driver interface {
    Run(ctx context.Context)
    State() State
    VolumeResolution() int
    ProtocolStatus() json.RawMessage

    SetPower(zone string, on bool)
    SetInput(zone, input string)
    SetVolume(zone string, volume int)
    SetMute(zone string, muted bool)
    SetSoundMode(zone, mode string)
}
```

The driver's own fields, such as eco and the tone trims, are not part
of this model. The driver assembles them into one JSON snapshot, and
the controller writes that snapshot under `status.denon`. A second
driver writes `status.wiim` and the model does not change.

### The mirror

The parser covers every line the AVR-X1700H emits, and a line the
parser does not know is still ignored. The status reports the state
the receiver last sent, grouped by what it describes.

```yaml
status:
  zones:
    main:
      power: on
      input: MPLAY
      soundMode: STEREO
      mute: false
      volume: "65"
      volumeMax: "98"
      sleep: 0
    zone2:
      power: on
      input: PHONO
      soundMode: STEREO
      mute: false
      volume: "90"
      sleep: 0
  denon:
    eco: auto
    dimmer: bright
    autoStandby: "off"
    audioInputMode: hdmi
    videoSelect: "off"
    bluetoothTransmitter: "off"
    speakerPreset: 1
    tone:
      control: false
      bass: 0
      treble: 0
    audyssey:
      multeq: reference
      dynamicEq: true
      referenceLevelOffset: 0
      dynamicVolume: "off"
      loudnessManagement: true
    audio:
      drc: "off"
      lfe: 0
      effect: 0
      delay: 0
      subwoofer: true
      restorer: "off"
      graphicEq: "off"
      headphoneEq: "off"
      speakerVirtualizer: true
      dialogEnhancer: "off"
    channelVolumes:
      FL: 0
      FR: 0
      C: 0
      SW: 0
  conditions:
    - type: Reachable
      status: "True"
```

The tone and channel numbers are in display units, so `50` from the
wire reads as `0`. A value the receiver has not sent is absent.

`status.denon` is the driver's own shape and the schema does not type
it field by field, because a second protocol reports a different shape
under its own name. The driver's tests pin it.

### The queries

The operator sends a small set on every connect and lets the rest
arrive as events. The set grows to cover the state the status
reports: `PW?`, `ZM?`, `Z2?`, `MV?`, `MU?`, `SI?`, `MS?`, `SLP?`,
`ECO?`, `DIM ?`, `STBY?`, `SD?`, `SV?`, `CV?`, and the `PS` queries
the AVR answers. A model that does not answer one ignores it, so the
query set is a union and not a requirement.

### The model's own inputs and modes

The operator cannot discover which machine feeds which input, and
that stays in `spec.inputs`. Reading the input names and the sound
modes a model offers, from `OPINF` and the `SI` and `MS` replies, is
an open problem: the `OPINF` bitmaps name capabilities by position
rather than by name, and no liken workflow matches on them yet.

### Control

An input may name the sound mode it wants when it is selected.
`spec.inputs[].soundMode` is optional, and when the session selects
that input the operator selects the mode in the same one-shot. This
is where the mode belongs: the mode travels with the input, and the
cluster owner already declares the input. A person who selects
another mode at the receiver's remote keeps it until the next
session selects the input again.

The zone stays the main zone. The receiver's own settings (eco,
dimmer, the tone trims, the Audyssey fields) are reported and not
set, because no liken workflow sets them yet, and a public field with
no consumer is a promise this repository does not keep. A future plan
adds declarative settings under `spec.denon` when something reads
them.

## Version coverage

The base commands (`PW`, `MV`, `MU`, `SI`, `MS`) are the same from
protocol Ver.4 through the current models. `ZM`, `Z2`, `Z3`, `ECO`,
`SLP`, and the `PS` families are newer and optional. Two rules carry
the versions:

* A query a model does not answer is not an error. The operator
  sends the union and folds what comes back.
* A line the parser does not know changes nothing.

The house's AVR-X1700H is the one model we can prove. Its answers are
captured as `denon/testdata/avr-x1700h.txt`, one line per query, and
a test folds that transcript into the state so a parser change is
measured against a real receiver. An older model adds a transcript
and no code.

## Proof

Failing tests first. The transcript folds into the expected state, the
session selects the input and the mode in one go, and the status
carries both zones and the settings.

The drill runs on the house cluster, where the receiver
is reachable. The cluster's Flux pins this repository at a release tag
and applies `deploy/`, and its `ImageUpdateAutomation` advances the pin
when a release is tagged, so a drill is a merge, a tag, and a read:
`kubectl get receiver living-room-denon -o yaml` shows both zones and
the `status.denon` settings. A change made at the receiver's own remote
must then move the status.

## What this leaves for later

* Declarative settings under `spec.denon` for eco, dimmer, the tone
  trims, and the Audyssey fields, when a workflow reads them.
* Zone 2 and zone 3 as session targets.
* HDMI output, video aspect, the tuner, the network player, the
  trigger outputs, and the speaker presets. The X1700H answers none
  of the first three on our probes, so they stay open problems for a
  model that has them.
* The Service front, from plan 00, is still plan 03.
* The second driver, a WiiM, and whatever its HTTP API reports.
