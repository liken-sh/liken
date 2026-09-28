# Keys on the bus

Plan 24. The bus speaks evdev. A `Remote` works with no `Keymap`, a
`Keymap` normalises one odd device, and every consumer binds the
kernel's key names to what they mean there.

## The problem

Linux splits input in two layers. The kernel and udev's hwdb
normalise a device: a scancode becomes `KEY_VOLUMEUP`, per device,
and only odd hardware needs an entry. Then each program binds key
names to its own actions. Kodi, mpv, and GNOME each hold a table from
`KEY_UP` to what it means there.

The `Keymap` does both jobs in one object, in the operator. It turns
`KEY_UP` into `up` and `KEY_VOLUMEUP` into `volume` by five, with a
repeat cadence beside each, and a `Remote` with no `Keymap` does
nothing at all. So a keyboard remote costs fifteen rows before one
button works, and every row restates what the kernel already said.
The action words are a third vocabulary between the kernel's and the
consumers', and each consumer translates them once more: the display
script maps `select` to a script message and the media browser maps
it to `enter`.

## The contract

**The events topic carries keys.** The standing remote pod publishes
every event as the kernel names it, on the `Remote`'s events topic:

```json
{"key": "KEY_UP", "value": 1}
```

`value` is the kernel's: `0` release, `1` press, `2` autorepeat. The
name comes from the operator's own table of evdev names, so a
consumer holds no table of numbers. A code the table does not name
is not published.

**The remote pod normalises.** Normalisation runs in one place, the
standing pod beside the device, because that is where hwdb runs on
any Linux machine, and because one pod serves every consumer at once.
The pod folds two tables:

* The base, compiled into the operator. Every `KEY_*` name passes as
  itself. The hat axes become the arrows: `ABS_HAT0Y` at `-1` is
  `KEY_UP`, at `1` is `KEY_DOWN`, and `ABS_HAT0X` is `KEY_LEFT` and
  `KEY_RIGHT`. The gamepad's south face button is `KEY_ENTER` and its
  east face button is `KEY_BACK`, because every console reads them
  that way. Nothing else in the base binds a button.
* The `Remote`'s `Keymap`, when it names one. A row maps one control
  to one key name, or to `none`, which drops it. A row on an axis
  names the value as today. A `Keymap` row replaces the base row for
  the same control.

**Repeat is a device fact.** A keyboard autorepeats in the kernel, and
the pod passes its value `2` events through. A gamepad button and a
hat axis never autorepeat, so the pod synthesises value `2` for them:
the base hat rows repeat at 400 ms then 250 ms, and a `Keymap` row
states a `repeat` block to make any other control repeat. No consumer
repeats anything itself.

**Focus stays where it is.** The mark on the focus topic still names
the one `Player` a `Remote` drives, and every consumer reads it before
it acts. The operator still arbitrates the cycle. The key that asks
for a cycle is `KEY_CYCLEWINDOWS`: a consumer that reads it publishes
the cycle request and does nothing else with it.

**Consumers bind.** Each holds its own table from key names to what
it does, and the tables are the docs of that consumer.

* The playback pod's command sidecar reads the events topics of the
  `Player`'s `Remote`s directly, gated on focus, and the translator
  sidecar goes away. `KEY_PLAYPAUSE`, `KEY_PLAY`, `KEY_PAUSE`, and
  `KEY_PLAYCD` cycle pause. `KEY_REWIND` and `KEY_FASTFORWARD` seek
  ten seconds. `KEY_PREVIOUSSONG` and `KEY_NEXTSONG` step a chapter.
  `KEY_VOLUMEUP` and `KEY_VOLUMEDOWN` step the level by five on the
  volume topic, and `KEY_MUTE` mutes. `KEY_SUBTITLE`, `KEY_AUDIO`, and
  `KEY_INFO` keep their meanings. The arrows, `KEY_ENTER`, `KEY_OK`,
  `KEY_SELECT`, `KEY_KPENTER`, `KEY_BACK`, `KEY_ESC`, and `KEY_EXIT`
  reach the display script as the six navigation words it already
  answers. A seek, a chapter step, a volume step, and an arrow act on
  press and on repeat. Everything else acts on press alone.
* The idle command pod reads the same keys. The volume keys step the
  unit, `KEY_BACK` and its two synonyms bring the shade down under the
  operator's own controller, and under a delegate the navigation keys
  are forwarded to the client on the `Player`'s commands topic in the
  same form they arrived, `{"key": ..., "value": ...}`.
* The media browser, in `library-operator`, reads the forwarded keys
  and binds them to its own key names. It treats a repeat as another
  press.

**The amounts are the consumer's defaults.** Five for volume, ten
seconds for a seek, one for a chapter. They are not in any `Keymap`.
A cluster that wants other numbers gets them from a later
`MediaPreferences` tier, not from a per-controller table.

**The `Keymap` shrinks.** Its rows are `{control, key}` with an
optional `value` for an axis and an optional `repeat` block. The
action words, the amounts, and the per-unit override on a `Player`'s
remotes entry go away: a device maps one way, as it does under hwdb.
`Remote.status.unbound` keeps its meaning with the new tables: every
declared control that the base and the `Keymap` map to nothing.

**Reserved keys.** `KEY_HOMEPAGE`, `KEY_WWW`, `KEY_POWER`, and
`BTN_MODE` pass through the pod and no consumer in this operator acts
on them. They belong to a home surface this operator does not own.

## What the live keymaps become

The X6 keeps two rows: `BTN_LEFT` to `KEY_ENTER`, because its shell
sends a mouse click for OK in one mode, and `KEY_COMPOSE` to
`KEY_CYCLEWINDOWS`. Its arrows, volume rocker, mute, and back need no
row. The DualSense keeps its face-button and bumper rows as remaps to
`KEY_VOLUMEUP`, `KEY_REWIND`, `KEY_NEXTSONG`, and the rest, each with
the repeat block it has today, and drops its hat rows, which the base
covers.

## What was set aside

Keeping the action vocabulary on the bus and adding a base layer
under it. It removes the fifteen rows but keeps the third
vocabulary, and every new consumer would translate it again.

Normalising in each consumer. Precedence would live in four
sidecars, and a `Keymap` edit would have to reach all of them.

A per-unit `Keymap` override. One controller mapping two ways on two
units has no precedent in hwdb, and no room has asked for it.

## Proof

On `liken-1`: with the X6 `Keymap` reduced to its two rows, every
button on the front cluster does what it did before, on the idle
screen, in a film, and on the media browser, and a held arrow still
repeats. With the `Remote`'s `keymap` field removed, the arrows, OK
in keys mode, back, volume, and mute still work, and only the
air-mouse click and the cycle key stop. The DualSense drives a film
with its reduced `Keymap`. `status.unbound` on the X6 lists the
shell's keyboard no longer, because every key passes.

### The drill record

Released as 2026.09.02-001 with the library layer's browser on the same
tag, and rolled to `liken-1` on 2026-09-02 with the two cluster
`Keymap`s rewritten in the same apply: the X6 down to two rows, the
DualSense down to remaps with repeats. The retained key tables read
back off the bus at eight rows for the X6 and fourteen for the pad,
the base folded with each `Keymap`. Drilled on 2026-09-02 from the X6
under plan 25's clients: the arrows, select, and back drove the
browser from the kernel's key names, a film played from a chosen
cover, and back returned to the browser. The DualSense in a film and
`status.unbound` on the X6 were not checked by eye in this drill.
