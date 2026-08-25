# The player owns the volume

Plan 10. The listening level becomes `Player` state: one retained
message on the bus that every pod for that unit follows, settable from
any of the unit's screens, and drawn by a `liken`-owned indicator in
place of `mpv`'s built-in text. It builds on the display of
[plan 07](07-the-player-draws-its-own-display.md) and the bus
contract of [plan 12](12-the-idle-screen-reads-the-bus.md).
It builds before [plan 11](../11-the-music-experience.md): the indicator
is the small piece, and the music layout is the large one.

## The problem

The volume today is a property of one `mpv` process. It starts at the
default on every `Play`, and it dies with the pod, so a person sets the
level again on every film. A press also shows `mpv`'s own text, because
`volume` and `mute` still carry `osd-auto`, so one action breaks the
display's voice that plan 07 established everywhere else.

The idle screen makes the gap sharper. A person who wants the room
quiet before they choose media has nowhere to say so: the idle pod's
`mpv` plays no audio, so there is no level to set until a `Play` runs.

## The topic is the authority

The level and the muted flag live in one retained message on
`<base>/players/<namespace>/<name>/volume`, beside the unit's
`commands`, `status`, and `panel` kinds. The payload is the pair
`{level, muted}`. The topic drives everything: a press publishes the
new state, and every pod for that `Player` subscribes and applies the
state to its own `mpv`. The play pod hears the sound change, and the
idle pod's `mpv` just tracks the property so its display can draw it.

One rule keeps the loop stable: only a press or the operator publishes,
and an observer only draws. The display watches `mpv`'s `volume` and
`muted` properties to render the indicator, and it must never publish
what it observed, or a held button becomes its own echo.

The level runs 0 to 100, and 100 is unity, `mpv`'s 0 dB and its own
default. The cap stays at 100 because the sinks sit at unity on the
audio side, and a software gain above unity only distorts.

## A press publishes

The command sidecars stop driving `mpv`'s volume directly. A `volume`
step or a `mute` press computes the next state from the last message on
the topic and publishes it, retained. The application to `mpv` happens
on the subscription like any other change, so the pod that pressed and
the pod that only listened run the same code path.

Muted state survives the same way the level does. A muted `Player`
stays muted across plays until a press unmutes it, and the indicator's
glyph is what says so.

## The operator seeds and writes through

When the topic holds nothing for a `Player`, the operator publishes
`{level: 100, muted: false}`, retained, on the unit's reconcile. The
seed keeps the state always readable off the broker, and a duplicate
seed from a racing pass publishes the same value, so the race settles
itself.

A `Play` may carry a starting state, and the operator writes it
through: it publishes the `Play`'s value, retained, before the pod
exists. The override becomes the `Player`'s new state, and everything
downstream is the normal path. A `Play` that carries nothing starts at
whatever the topic already holds.

## Any screen with speakers

The idle screen publishes and draws through the same path as the play
pod, so a person can set the level before they choose media. The gate
is the `Player`'s `sinks`: a unit with no speakers neither draws the
indicator nor accepts a volume press, because there is no level to
mean anything.

## The indicator

`volume` and `mute` move to `no-osd`, so `mpv` draws nothing, and the
display draws the level itself: a short bar with the level and a
speaker glyph, drawn through `theme`, so it matches the scrubber and
fades with the rest of the OSD. The glyph carries the muted state, so
`mute` reads on the same element with no second one. The placement
settles when the slice is built. A low centered bar and a readout
under the clock are the two candidates.

The indicator appears alone, and it leaves after the same idle window
the scrubber waits out, on a fade and a timer of its own, so a volume
press summons no other element of the OSD. The show trigger is the
sidecar, not the property: after the sidecar applies a bus message to
`mpv` it sends a `volume-changed` script message, except for the
first message of a bus session, which is the broker's retained
catch-up. The display records the properties and draws only on the
message, so a pod that restores the level at start pops no indicator.
The indicator is not a focus stop. `up` and `down` do not land on it,
because volume is a quick repeated action, not a control a viewer
walks to.

## Set aside for this slice

* **The level in `kubectl get`.** The operator can fold the topic into
  the `Player`'s status as a read-only echo, the way it folds the
  panel state. The bus topic stays the authority either way, so the
  fold is a follow-on.
* **A household default level.** A starting level on
  `MediaPreferences`, resolved like the languages of plan 08, is a
  follow-on. The seed at unity is the only default this slice knows.
* **Volume shared across players.** This slice owns one `Player`'s
  level. A level shared across several units, and the unity-volume
  path the audio operator runs, are the larger A/V design and stay
  set aside.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. The drill checks each claim:

* On the idle screen, a volume press shows the `liken` bar and writes
  the retained message, read back with `mosquitto_sub`.
* A film starts at the level the idle screen set, with no press.
* Raising and lowering the volume during the film shows the `liken`
  bar, not `mpv`'s text, and the bar tracks the level.
* `mute` shows the muted state on the same indicator, and the state
  survives into the next `Play`.
* A `Play` that carries a starting level plays at it, and the topic
  holds the written-through value.
* The indicator fades out on idle, and it does not pop at the pod's
  start.
