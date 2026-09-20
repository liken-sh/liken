# Declarative settings and the bus controller

Plan 04.

## The problem

The Receiver today reports the Denon's settings and drives five things:
power, input, volume, mute, and sound mode. Every setting the driver
reads stays read-only. Eco, the dimmer, the tone trims, the Audyssey
fields, and the rest reach `status.denon` and nothing writes them back,
because no workflow set them and a public field with no consumer is a
promise this repository does not keep. The cluster cannot configure the
receiver, only observe it.

This plan makes the Receiver a declarative configurer and a bus
controller of the receiver. Every setting the driver can read becomes
declarable in `spec.denon.settings` and settable over a bus settings
topic. One-shot actions that are not settings go over a bus commands
topic. The receiver-common controls stay at the Receiver level: power,
volume, inputs, and the two topics. The Denon-only vocabulary stays
under `spec.denon`.

## The design

### The split

This CRD is for Receivers, so audio, video, and inputs are assumed.
`power`, `volume`, and `inputs` are first-class common fields, not
Denon vocabulary. The split mirrors plan 02: a second protocol needs
the same power, volume, and inputs with no shared vocabulary, and the
denon protocol block owns only the wire connection and the settings
vocabulary that names Denon's own fields.

The connection and the settings travel together. `spec.denon.address`
is how the driver reaches the receiver, and `spec.denon.settings` is
what it says once it does, so both live in the protocol block. A
WiiM gets its own protocol block and its own settings vocabulary, and
the Receiver-common fields do not change.

### The driver settings model

`denon.Settings` is the exported shape: nested family structs, one per
family the driver already parses (system, tone, audyssey, audio,
channelVolumes). Optional scalars are pointers, so a key that is not
declared differs from a key set to zero. `Settings()` returns what the
receiver last reported, `ApplySettings()` sends the wire command for
every declared field and leaves the caller to apply only a change, and
`Set(id, value)` and `Do(id, args)` translate a bus message into a
command. A table-driven id table maps a stable setting id to its parse,
its command builder, and its value type, so the families stay uniform
and a new family is a table.

Each family gets its own file, so later families are added in
parallel without editing a shared struct. The families the driver
parses today land first; the rest land in the phases below.

### The bus

Two topics per receiver unit, named in `spec.settingsTopic` and
`spec.commandsTopic`:

* settings, a message `{"setting": "tone.bass", "value": 3}`, where the
  id names the family and the field and the value is in display units.
* commands, a message `{"command": "quick.3"}`, where the command names
  a one-shot action that is not a setting. The one generic action so
  far is `input.ensure`: make sure the session's player is on the input
  the session names. A program asks in player terms and the receiver
  resolves the input from the session it holds, so no input name
  crosses the bus, and a room already on that input is sent nothing.
  A controller press is the ask: the media operator reads every press
  on the controller that drives the unit and publishes this command,
  so picking up the remote restores the room without a timer and
  without fighting a hand on the receiver.

The bus moves from the media session to the receiver unit. Today each
session opens its own `Bus` and holds it for the life of the Play, so
a receiver with no session has no bus and settings have no path. The
unit owns one bus that outlives any single Play, subscribes to the two
topics for as long as the Receiver exists, and hands the session the
connection it already has.

### The one-writer invariant

A setting key has exactly one writer.

* A manifest-declared key is enforced. When the settings block changes,
  the operator re-sends every declared field on purpose, so a value
  declared in the spec is authoritative over a change made at the
  receiver. A pass with no change to the block sends nothing. Flux owns
  the key, and the operator never writes it back.
* A bus-written key is operator-owned. The operator applies the value
  and writes it back into `spec.denon.settings` at the leaf, with
  server-side apply under its own field manager at leaf granularity, so
  a write to one key does not claim the keys around it.
* A key that is both declared and bus-written is a misconfiguration:
  the operator's forced write wins each round, and Flux reverts the
  leaf on its next sync.

The invariant matters because of how Flux applies. Flux applies with
server-side apply, so a field a manifest declares is a field Flux owns.
If the operator also wrote that field back, the two field managers
fight over one key and every reconcile is a conflict. A bus-written key
is written back so the declared state of the resource stays true, but
it must be a key Flux never declared, and the write is scoped to that
leaf so the two never own the same key. A bus write is recorded into
the spec on queue acceptance, so the spec is desired state and not a
mirror of the hardware.

### `session.awake`

`awake` is the media session's existing flag. The media operator writes
it into `session`; it follows the panel desire the idle client
publishes, which the room's power button drives; and a false to true
edge runs the power-on and input one-shot, which is the "wake the
room" job.

The new declarative `spec.power` overlaps it: `spec.power` states where
the receiver should sit, while `awake` is a transient wake event. The
two agree in the common case, and `awake` keeps the wake job. When a
`Play` starts it wakes the receiver and selects the input, and a person
who turns the receiver off at its own button is not fought by a
declared `power` that re-asserts on the next reconcile. The exact
interaction of a declared `power` and a wake is an open item until the
wake path and the declarative path are both live.

### Zones 2 and 3

The zones are receiver-common, like power and inputs, so they sit in a
top-level `spec.zones` block beside `spec.power` and `spec.inputs`,
rather than under `spec.denon`. Only `zone2` and `zone3` are valid
keys, so no other key is a valid input and the driver can serve every
zone the schema admits. Each entry declares the zone's power, input,
volume, mute, and sleep. A declared value is enforced when that zone's
block changes, and a pass with no change sends nothing. The main zone
has no entry: `spec.power` and `spec.session` drive it.

## Phases

1. The mechanism, plus the families the driver already parses: the
   `Settings` model, the id table, the bus move to the unit, the
   one-writer write-back, declarative settings for system, tone,
   audyssey, audio, and channelVolumes, and the non-main zones'
   declared controls. This is the built phase.

## Future families

Four families the protocol names are not parsed, because the house's
AVR-X1700H answers none of them and there is no hardware to prove a
parser. They are documented here and not built. Each lands as the same
shape: a family type on `denon.Settings`, a `denon/settings_<family>.go`
holding its specs and command builders, the matching CRD block, and
fake-receiver coverage. A family is a new file and one line in the
assembly, so the mechanism does not change to add one.

- **The tuner** (`TF`, `TM`, `TP`): the band, the frequency, a preset,
  and the tuner's transport.
- **The network player and HEOS** (`NS`, `NSA`, `NSE`): the source, the
  now-playing state, and transport. Transport is a command, not a
  setting.
- **The video controls** (`VSASP`, `VSMONI`): the aspect and the
  monitor out. The port-23 commands answer nothing on the house's
  receiver. The same controls are button codes on the HTTP interface
  plan 07 records.
- **The trigger outputs** (`TR`): the twelve-volt triggers.

Each stays unproven until a model that speaks it answers. A transcript
from that model is the proof; the fake-receiver test only pins the
shape. `denon/AGENTS.md` lists the commands the parser ignores.

## Verification

The mechanism is proved on the house's AVR-X1700H, which answers the
settings the driver already parses. A declared setting reaches the
receiver, a bus write reaches it and returns to `spec.denon.settings`,
and a key in both is reported. The drill runs on the house cluster the
way plan 02's did.

The tuner, network/HEOS, video, and trigger families get parsers,
command builders, and fake-receiver coverage that emulates the
documented protocol, but no transcript from real hardware: this house's
AVR-X1700H answers none of them. Each family stays marked unproven
until a model that speaks it answers. A transcript from a real model
turns it proven, and that is the proof, not the fake-receiver test.

Two values inside the parsed families are unproven for the same reason.
Which of `PSDEL` and `PSDELAY` carries the audio delay has no published
cross-check, and the sign of a non-zero LFE level beyond its cut-only
range is not in the transcript. Only the zero values and the range are
pinned.

## What this leaves for later

* Whether a declared `spec.power` re-asserts across a wake.
* The Service front from plan 00 remains plan 03, not built here.

The `soundMode` question is settled: it stays on `spec.inputs`, where
plan 02 put it, because the mode travels with the input the session
already selects.
