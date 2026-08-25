---
title: Keymaps
weight: 40
toc: true
---

# Keymaps

A `Keymap` is one controller model's table from buttons and axes to
named actions, written once per model and shared by every
[`Remote`](/docs/reference/remotes/) of that model. It is
cluster-scoped, the way a `DeviceClass` and a `StorageClass` are,
because one model's table is the same in every room. A `Remote` in
any namespace names it without a namespace qualifier.

The left side of the table uses evdev's names, because every Linux
controller driver reports the south face button as `BTN_SOUTH`,
whatever the glyph on the plastic. The right side names what a
person means, `pause` or `seek`, never an mpv command, so a
different player program can implement the same table later.

## The spec

    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: dualsense
    spec:
      buttons:
        - press: BTN_SOUTH
          action: pause
        - press: BTN_TR
          action: seek
          amount: 30
          repeat:
            delay: 400ms
            interval: 300ms
      axes:
        - axis: ABS_HAT0X
          value: 1
          action: right

Buttons and axes are separate lists because they bind differently: a
button is a press, and an axis entry names a direction as well. A
`Keymap` must bind at least one entry across the two lists, because
a table that binds nothing would answer no press.

### Buttons

`press` is the button, by its evdev key name. The names this
operator accepts: `BTN_SOUTH`, `BTN_EAST`, `BTN_C`, `BTN_NORTH`,
`BTN_WEST`, `BTN_Z`, `BTN_TL`, `BTN_TR`, `BTN_TL2`, `BTN_TR2`,
`BTN_SELECT`, `BTN_START`, `BTN_MODE`, `BTN_THUMBL`, and
`BTN_THUMBR`.

### Axes

A gamepad's d-pad arrives as the two hat axes rather than as
buttons, so each d-pad direction is one entry here. `axis` is `ABS_HAT0X`,
across, or `ABS_HAT0Y`, down. `value` is `-1` or `1`, the direction
this entry binds; the hat reports 0 as the release. The analog
sticks are not bindable: a resting thumb reports hundreds of times a
second, and no action takes an analog value.

### Actions

`action` is what the press does, in this operator's vocabulary:
`pause`, `mute`, `seek`, `volume`, `chapter`, `subtitles`, `audio`,
`info`, `cycle-focus`, `up`, `down`, `left`, `right`, `select`, and
`back`. Most actions command the player. The navigation actions,
`up`, `down`, `left`, `right`, `select`, and `back`, drive the
on-screen display. `cycle-focus` switches which unit a shared
controller holds.

`amount` is how far an action moves: seconds for `seek`, a step for
`volume` and `chapter`. The sign is the direction, so one action
serves both bumpers. Exactly the three actions that move take an
amount; the rest refuse one. The CRD states this rule in CEL, and
the operator checks it again before it builds a pod.

### Repeat

A binding with a `repeat` block repeats while the control is held:
the translator publishes the command on the press, waits `delay`,
then re-publishes every `interval` until the release. Both fields
are durations, like `400ms` or `1s`. `delay` defaults to 400ms, long
enough that a tap does not repeat, and `interval` defaults to 300ms.
A binding with no `repeat` block fires once per press, whatever the
action is. A repeat works on any action, so a held `seek` scrubs and
a held `volume` ramps. One synthesized repeat is capped at 30
seconds, because a controller that sleeps mid-hold publishes no
release.

## No status

A `Keymap` is a table a person writes and nothing reports on, so
there is no status subresource, and `kubectl get keymaps` shows
nothing but each table's age. The compile runs in the operator,
before any object is created, so a name that means nothing fails the
`Play` with a message instead of crash-looping a sidecar.

## On the bus

Each `Keymap` owns one retained topic on the
[bus](/docs/reference/bus/), under the cluster's topic base:

    keymaps/<name>

The topic drops the namespace segment because a `Keymap` is
cluster-scoped. The operator is the only writer. It compiles the
table's names down to numbers and publishes the whole table as one
JSON array, retained, so a translator reads the current table the
instant it connects, and a `Keymap` edit reaches a running film with
no pod restart. The example above compiles to:

    [{"type": 1, "code": 304, "value": 1, "action": "pause"},
     {"type": 1, "code": 311, "value": 1, "action": "seek", "amount": 30,
      "repeatDelay": 400, "repeatInterval": 300},
     {"type": 3, "code": 16, "value": 1, "action": "right"}]

Each row is one binding: an evdev event `type`, `code`, and `value`
on the left, an `action` and its `amount` on the right. A button
compiles to `EV_KEY` (type 1) with value 1, the press alone. An axis
compiles to `EV_ABS` (type 3) with the value the entry states.
`repeatDelay` and `repeatInterval` are milliseconds, and both are
absent on a binding that fires once. The translator matches numbers
and parses no name.

The operator republishes a topic only when the compiled table
differs from the last one it wrote, because a new subscriber reads
the retained value from the broker. A `Keymap` that does not compile
publishes nothing and leaves the last-good table in place, so a
broken edit does not empty a running translation, and the operator
logs the failure. When a `Keymap` is deleted, the operator clears the retained
value with an empty publish, so a deleted table leaves nothing
behind on the bus.
