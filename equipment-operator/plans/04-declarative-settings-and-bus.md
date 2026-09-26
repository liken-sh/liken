# Declarative settings and the bus controller

Plan 04.

## The problem

The Receiver today reports the Denon's settings and drives five things:
power, input, volume, mute, and sound mode. Every setting the driver
reads stays read-only. Eco, the dimmer, the tone trims, the Audyssey
fields, and the rest reach `status.denon`, and nothing writes them back.
Nothing writes them because no workflow set them, and this repository does not add a public field
that nothing uses. The cluster can observe the receiver, but it cannot
configure it.

This plan makes the Receiver a declarative configurer and a bus
controller of the receiver. Every setting the driver can read becomes
declarable in `spec.denon.settings` and settable over a bus settings
topic. One-shot actions that are not settings go over a bus commands
topic. The controls that every receiver has stay at the Receiver level:
power, volume, inputs, and the two topics. The Denon-only vocabulary
stays under `spec.denon`.

## Design

### Common fields and protocol fields

This CRD is for Receivers, so every Receiver is assumed to have audio,
video, and inputs. `power`, `volume`, and `inputs` are first-class
common fields, outside the Denon vocabulary. The split matches plan 02.
A second protocol needs the same power, volume, and inputs, and shares
no vocabulary with Denon. So the denon protocol block holds only the
wire connection and the settings vocabulary that names Denon's own
fields.

The connection and the settings belong in the same block.
`spec.denon.address` is how the driver connects to the receiver, and
`spec.denon.settings` is what it sends after it connects. A WiiM gets
its own protocol block and its own settings vocabulary, and the
Receiver-common fields do not change.

### The driver settings model

`denon.Settings` is the exported type: nested family structs, one per
family the driver already parses (system, tone, audyssey, audio,
channelVolumes). Optional scalars are pointers, so a key that is not
declared differs from a key set to zero.

* `Settings()` returns what the receiver last reported.
* `ApplySettings()` sends the wire command for every declared field.
  The caller calls it only when a value changed.
* `Set(id, value)` and `Do(id, args)` translate a bus message into a
  command.

An id table maps each stable setting id to its parser, its command
builder, and its value type. So all families use the same code path,
and a new family is new rows in the table.

Each family gets its own file, so later families can be added in
parallel without editing a shared struct. The families the driver
parses today come first; the rest come in the phases below.

### The bus

The operator runs one worker, the unit, for each Receiver. Each unit
has two topics, named in `spec.settingsTopic` and
`spec.commandsTopic`:

* settings, a message `{"setting": "tone.bass", "value": 3}`, where the
  id names the family and the field and the value is in display units.
* commands, a message `{"command": "quick.3"}`, where the command names
  a one-shot action that is not a setting. The one generic action so
  far is `input.ensure`: make sure the session's player is on the input
  the session names. A program sends the request in player terms, and
  the receiver resolves the input from the session it holds. So no
  input name is sent on the bus, and a receiver that is already on that
  input gets no command. A button press on a controller triggers the
  request. The media operator reads every button press on the
  controller that drives the unit and publishes this command. So a
  press on the remote switches the receiver back to the session's
  input. This needs no timer, and it does not override a change a
  person makes at the receiver.

The bus moves from the media session to the unit. Today each session
opens its own `Bus` and holds it for the life of the Play. So a
receiver with no session has no bus, and settings have no path to it.
The unit owns one bus that stays open across Plays, subscribes to the
two topics for as long as the Receiver exists, and gives the session
the connection it already has.

### One writer for each setting key

A setting key has exactly one writer.

* The operator enforces a key that the manifest declares. When the
  settings block changes, the operator re-sends every declared field,
  so a value declared in the spec replaces a change made at the
  receiver. A pass with no change to the block sends nothing. Flux owns
  the key, and the operator never writes it back.
* The operator owns a key that a bus message writes. The operator
  applies the value and writes it back into `spec.denon.settings` at
  the leaf. It uses server-side apply under its own field manager at
  leaf granularity, so a write to one key does not claim the keys
  around it.
* A key that is both declared and written over the bus is a
  misconfiguration. The operator's forced write replaces the value on
  each pass, and Flux reverts the leaf on its next sync.

The rule is necessary because of how Flux applies. Flux uses
server-side apply, so Flux owns each field a manifest declares. If the
operator also wrote that field back, the two field managers would
conflict over one key on every reconcile. The operator writes a
bus-written key back so the spec stays accurate. But the key must be
one that Flux never declared, and the write is scoped to that leaf, so
the two managers never own the same key. The operator records a bus
write into the spec when it accepts the write into its queue. So the
spec is the desired state, and it does not mirror the hardware.

### `session.awake`

`awake` is the media session's existing flag. The media operator writes
it into `session`. It follows the panel power state that the idle
client requests on the bus, and the power button on the remote changes
that request. A change from false to true runs the one-shot power-on and
input selection. This is the wake job.

The new declarative `spec.power` overlaps it. `spec.power` states the
power state the receiver should stay in, while `awake` is a transient
wake event. The two agree in the common case, and `awake` keeps the
wake job. When a `Play` starts, it wakes the receiver and selects the
input. When a person turns the receiver off at its own button, a
declared `power` does not turn it back on at the next reconcile. The
exact interaction of a declared `power` and a wake stays open until the
wake path and the declarative path are both live.

### Zones 2 and 3

Every receiver has zones, like power and inputs, so the zones go in a
top-level `spec.zones` block beside `spec.power` and `spec.inputs`,
not under `spec.denon`. Only `zone2` and `zone3` are valid keys, so no
other key is a valid input and the driver can serve every zone the
schema accepts. Each entry declares the zone's power, input, volume,
mute, and sleep. The operator enforces a declared value when that
zone's block changes, and a pass with no change sends nothing. The main
zone has no entry: `spec.power` and `spec.session` drive it.

## Phases

1. The mechanism, plus the families the driver already parses: the
   `Settings` model, the id table, the bus move to the unit, the
   write-back for bus-written keys, declarative settings for system,
   tone, audyssey, audio, and channelVolumes, and the declared
   controls of the zones other than the main zone. This phase is
   built.

## Future families

The protocol names four families that the driver does not parse. The
house's AVR-X1700H responds to none of them, so no hardware can test a
parser. This plan documents them and does not build them. Each one is
added the same way: a family type on `denon.Settings`, a
`denon/settings_<family>.go` that holds its specs and command builders,
the matching CRD block, and fake-receiver coverage. A family is a new
file and one line in the assembly, so the mechanism does not change to
add one.

- **The tuner** (`TF`, `TM`, `TP`): the band, the frequency, a preset,
  and the tuner's transport.
- **The network player and HEOS** (`NS`, `NSA`, `NSE`): the source, the
  now-playing state, and transport. Transport is a command, not a
  setting.
- **The video controls** (`VSASP`, `VSMONI`): the aspect and the
  monitor out. The house's receiver responds to none of the port-23
  commands for these controls. The same controls are button codes on
  the HTTP interface that plan 07 records.
- **The trigger outputs** (`TR`): the twelve-volt triggers.

`denon/AGENTS.md` lists the commands the parser ignores.

## Verification

The mechanism is tested on the house's AVR-X1700H, which responds to
the settings the driver already parses. A declared setting reaches the
receiver. A bus write reaches the receiver and is written back to
`spec.denon.settings`. A key in both is reported. The drill runs on the
house cluster the same way as plan 02's drill.

The tuner, network/HEOS, video, and trigger families get parsers,
command builders, and fake-receiver coverage that emulates the
documented protocol. They get no transcript from real hardware, because
this house's AVR-X1700H responds to none of them. Each family stays
marked unproven until a model that supports it responds. A transcript
from a real model marks it proven. The fake-receiver test only checks
the message format and does not prove a family.

Two values inside the parsed families are unproven for the same reason.
No published source confirms which of `PSDEL` and `PSDELAY` carries the
audio delay. The transcript does not show the sign of a non-zero LFE
level outside its cut-only range. The tests check only the zero values
and the range.

## What this leaves for later

* Whether a declared `spec.power` re-asserts across a wake.
* The Service front from plan 00 remains plan 03, and this plan does
  not build it.

The `soundMode` question is settled. It stays on `spec.inputs`, where
plan 02 put it, because the mode changes with the input the session
already selects.
