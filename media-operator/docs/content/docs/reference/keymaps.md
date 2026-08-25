---
title: Keymaps
weight: 40
toc: true
---

<!-- Generated from deploy/keymaps-crd.yaml by docs/crdref. Do not edit. -->

A `Keymap` is one controller model's table from buttons and axes to
named actions, written once per model and shared by every
[`Remote`](/docs/reference/remotes/) of that model. It is
cluster-scoped, the way a `DeviceClass` and a `StorageClass` are,
because one model's table is the same in every namespace. A `Remote` in
any namespace names it without a namespace qualifier.

The left side of the table uses evdev's names, because every Linux
controller driver reports the south face button as `BTN_SOUTH`,
whatever is printed on the button. The right side names what a
person means, `pause` or `seek`, never an mpv command, so a
different player program can implement the same table later.

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
`Keymap` must bind at least one entry across the two lists.

One controller model's table from buttons and axes to named actions. Write one Keymap per model, and share it through every Remote of that model.

## spec

The table itself, in two lists: buttons for key presses, and axes for the d-pad's hat directions.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--buttons"></span>`buttons` | [\[\]object](#specbuttons) | no | Button entries. Each one answers the press. A held button fires once unless it names a repeat, and the release stops a repeat the press started. |
| <span id="spec--axes"></span>`axes` | [\[\]object](#specaxes) | no | Axis entries. A gamepad's d-pad arrives as the two hat axes rather than as buttons, so each d-pad direction is one entry here. |

### spec.buttons[]

Button entries. Each one answers the press. A held button fires once unless it names a repeat, and the release stops a repeat the press started.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttons--press"></span>`press` | string | yes | The button, by its evdev key name, such as BTN_SOUTH. Every Linux controller driver reports the same position under the same name, so the name works across models. The names this operator accepts: BTN_SOUTH, BTN_EAST, BTN_C, BTN_NORTH, BTN_WEST, BTN_Z, BTN_TL, BTN_TR, BTN_TL2, BTN_TR2, BTN_SELECT, BTN_START, BTN_MODE, BTN_THUMBL, and BTN_THUMBR. |
| <span id="specbuttons--action"></span>`action` | string | yes | What the press does, in this operator's vocabulary. The words name what a person means, never a player program's own command. Most actions command the player. The navigation actions, up, down, left, right, select, and back, drive the on-screen display. cycle-focus switches which unit a shared controller holds. One of: `pause`, `mute`, `seek`, `volume`, `chapter`, `subtitles`, `audio`, `info`, `cycle-focus`, `up`, `down`, `left`, `right`, `select`, `back`. |
| <span id="specbuttons--amount"></span>`amount` | integer | no | How far the action moves: seconds for seek, a step for volume and chapter. The sign is the direction, so one action serves both bumpers. Exactly these three actions take an amount; the rest refuse one. |
| <span id="specbuttons--repeat"></span>`repeat` | [object](#specbuttonsrepeat) | no | When present, the action repeats while the button is held. The player pod fires it on the press, waits the delay, then re-fires it every interval until the release. Omit it and the button fires once per press. A repeat works on any action, so a held seek scrubs and a held volume ramps. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release. |

#### spec.buttons[].repeat

When present, the action repeats while the button is held. The player pod fires it on the press, waits the delay, then re-fires it every interval until the release. Omit it and the button fires once per press. A repeat works on any action, so a held seek scrubs and a held volume ramps. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttonsrepeat--delay"></span>`delay` | string | no | How long to hold before the repeat starts, as a duration like 400ms. A tap shorter than this does not repeat. Defaults to 400ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specbuttonsrepeat--interval"></span>`interval` | string | no | How often to re-run the action while the button is held, as a duration like 300ms. Defaults to 300ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

### spec.axes[]

Axis entries. A gamepad's d-pad arrives as the two hat axes rather than as buttons, so each d-pad direction is one entry here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxes--axis"></span>`axis` | string | yes | One of the two hat axes, X across and Y down. The analog sticks are not bindable: a resting thumb reports hundreds of times a second, and no action takes an analog value. One of: `ABS_HAT0X`, `ABS_HAT0Y`. |
| <span id="specaxes--value"></span>`value` | integer | yes | Which direction of the axis this entry binds. The hat reports -1 and 1 as its two presses and 0 as the release, and the release stops a repeat the press started. One of: `-1`, `1`. |
| <span id="specaxes--action"></span>`action` | string | yes | What the press does, in this operator's vocabulary. The words name what a person means, never a player program's own command. Most actions command the player. The navigation actions, up, down, left, right, select, and back, drive the on-screen display. cycle-focus switches which unit a shared controller holds. One of: `pause`, `mute`, `seek`, `volume`, `chapter`, `subtitles`, `audio`, `info`, `cycle-focus`, `up`, `down`, `left`, `right`, `select`, `back`. |
| <span id="specaxes--amount"></span>`amount` | integer | no | How far the action moves: seconds for seek, a step for volume and chapter. The sign is the direction. Exactly these three actions take an amount; the rest refuse one. |
| <span id="specaxes--repeat"></span>`repeat` | [object](#specaxesrepeat) | no | When present, the action repeats while the hat direction is held. The player pod fires it on the press, waits the delay, then re-fires it every interval until the release. Omit it and the direction fires once per press. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release. |

#### spec.axes[].repeat

When present, the action repeats while the hat direction is held. The player pod fires it on the press, waits the delay, then re-fires it every interval until the release. Omit it and the direction fires once per press. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxesrepeat--delay"></span>`delay` | string | no | How long to hold before the repeat starts, as a duration like 400ms. A tap shorter than this does not repeat. Defaults to 400ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specaxesrepeat--interval"></span>`interval` | string | no | How often to re-run the action while the hat direction is held, as a duration like 300ms. Defaults to 300ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

## No status

Nothing reports on a `Keymap`, so it has no status subresource, and
`kubectl get keymaps` shows each table's age. The operator compiles
a table before it creates any pod. A `press` or `action` name
outside the vocabulary above fails the `Play` that uses the table,
and the reason appears on that `Play`'s status.

## On the bus

Each `Keymap` owns one retained topic on the
[bus](/docs/reference/bus/), under the cluster's topic base:

    keymaps/<name>

The topic drops the namespace segment because a `Keymap` is
cluster-scoped. The operator is the only writer. It compiles the
table's names down to numbers and publishes the whole table as one
JSON array, retained, so a translator reads the current table the
instant it connects, and a `Keymap` edit reaches a running
translator with no pod restart. The example above compiles to:

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
