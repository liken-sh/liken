---
title: Keymaps
weight: 40
toc: true
---

<!-- Generated from deploy/keymaps-crd.yaml by crdref. Do not edit. -->

A `Keymap` is one controller model's table from its odd controls to
the kernel key names they should report, written once per model and
shared by every [`Remote`](/docs/reference/remotes/) of that model. A
model needs one only where the base table gets it wrong. The base
passes every `KEY_*` code as itself, turns the hat axes into the
arrows, and reads a gamepad's south and east buttons as enter and
back, so a `Remote` with no `Keymap` already works.

It is cluster-scoped, the way a `DeviceClass` and a `StorageClass` are,
because one model's table is the same in every namespace. A `Remote` in
any namespace names it without a namespace qualifier.

Both sides of the table use evdev's names, because every Linux
controller driver reports the south face button as `BTN_SOUTH`,
whatever is printed on the button, and every consumer binds
`KEY_VOLUMEUP` the same way. A `Keymap` renames a control and nothing
more. The right side is a kernel key name, or `none` to drop the
control, and each consumer holds its own table from key names to what
they mean there.

    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: dualsense
    spec:
      buttons:
        - press: BTN_NORTH
          key: KEY_VOLUMEUP
          repeat:
            delay: 400ms
            interval: 150ms
        - press: BTN_TR
          key: KEY_FASTFORWARD
          repeat:
            delay: 400ms
            interval: 250ms
        - press: BTN_THUMBR
          key: none
      axes:
        - axis: ABS_HAT0X
          value: 1
          key: KEY_RIGHT

Buttons and axes are separate lists because they bind differently: a
button is a press, and an axis entry names a direction as well. A
`Keymap` must bind at least one entry across the two lists.

One controller model's table from its odd controls to the kernel key names they should report. Write one Keymap per model that needs one, and share it through every Remote of that model.

## spec

The table itself, in two lists: buttons for key presses, and axes for the hat directions.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--buttons"></span>`buttons` | [\[\]object](#specbuttons) | no | Button entries. Each one renames one control. A control with no entry reports the name the kernel gives it, and the release stops a repeat the press started. |
| <span id="spec--axes"></span>`axes` | [\[\]object](#specaxes) | no | Axis entries. A gamepad's d-pad arrives as the two hat axes rather than as buttons, so each d-pad direction is one entry here. |

### spec.buttons[]

Button entries. Each one renames one control. A control with no entry reports the name the kernel gives it, and the release stops a repeat the press started.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttons--press"></span>`press` | string | yes | The button, by its evdev key name: BTN_SOUTH on a gamepad, KEY_PLAYPAUSE on a media remote. Every Linux driver reports the same button under the same name, so the name works across models. Any name in the kernel's EV_KEY space is accepted; the pattern here catches a typo's shape, and the operator's compile is the gate on the name itself. A Remote in discovery logs the name of every code its controller reports, so the names come from the log, not from a vendor document. Pattern: `^(BTN\|KEY)_[A-Z0-9_]+$`. |
| <span id="specbuttons--key"></span>`key` | string | yes | The kernel key name this control reports instead, or none to drop it. Both sides are the kernel's own names. A consumer binds this name and not the control, and none is how a Keymap silences a control the base would otherwise pass. Pattern: `^((BTN\|KEY)_[A-Z0-9_]+\|none)$`. |
| <span id="specbuttons--repeat"></span>`repeat` | [object](#specbuttonsrepeat) | no | When present, the standing remote pod synthesises the repeat while the button is held: it publishes the press, waits the delay, then publishes a repeat every interval until the release. A gamepad button never autorepeats in the kernel, so it needs this block. A keyboard key autorepeats on its own and needs none. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release. |

#### spec.buttons[].repeat

When present, the standing remote pod synthesises the repeat while the button is held: it publishes the press, waits the delay, then publishes a repeat every interval until the release. A gamepad button never autorepeats in the kernel, so it needs this block. A keyboard key autorepeats on its own and needs none. One repeat is capped at 30 seconds, because a controller that sleeps mid-hold publishes no release.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttonsrepeat--delay"></span>`delay` | string | no | How long to hold before the repeat starts, as a duration like 400ms. A tap shorter than this does not repeat. Defaults to 400ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specbuttonsrepeat--interval"></span>`interval` | string | no | How often to publish the repeat while the button is held, as a duration like 300ms. Defaults to 300ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

### spec.axes[]

Axis entries. A gamepad's d-pad arrives as the two hat axes rather than as buttons, so each d-pad direction is one entry here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxes--axis"></span>`axis` | string | yes | One of the two hat axes, X across and Y down. The analog sticks are not bindable: a resting thumb reports hundreds of times a second, and no key name carries an analog value. One of: `ABS_HAT0X`, `ABS_HAT0Y`. |
| <span id="specaxes--value"></span>`value` | integer | yes | Which direction of the axis this entry binds. The hat reports -1 and 1 as its two presses and 0 as the release, and the release stops a repeat the press started. One of: `-1`, `1`. |
| <span id="specaxes--key"></span>`key` | string | yes | The kernel key name this direction reports, or none to drop it. The base already names both directions of both hats as the arrows, so an entry here is for a pad that means something else by them. Pattern: `^((BTN\|KEY)_[A-Z0-9_]+\|none)$`. |
| <span id="specaxes--repeat"></span>`repeat` | [object](#specaxesrepeat) | no | The same repeat block the buttons carry, for a hat direction. The base already repeats the four arrows, so an entry needs this only where it names a key of its own. |

#### spec.axes[].repeat

The same repeat block the buttons carry, for a hat direction. The base already repeats the four arrows, so an entry needs this only where it names a key of its own.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxesrepeat--delay"></span>`delay` | string | no | How long to hold before the repeat starts, as a duration like 400ms. Defaults to 400ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specaxesrepeat--interval"></span>`interval` | string | no | How often to publish the repeat while the direction is held, as a duration like 300ms. Defaults to 300ms. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

## No status

Nothing reports on a `Keymap`, so it has no status subresource, and
`kubectl get keymaps` shows each table's age. The operator compiles
each table on every pass. A `press`, `axis`, or `key` name that is not
an evdev name fails the compile, the operator logs the failure, and
every `Remote` that names the `Keymap` keeps the last good table.

## On the bus

A `Keymap` owns no topic of its own. The operator folds the base table
with the `Keymap` and publishes the result retained on the `keys`
topic of each `Remote` that names it, under the
[`Remote`'s tree](/docs/reference/remotes/). A `Keymap` that does not
compile publishes nothing and leaves the last good table on each of
those topics. A deleted `Keymap` leaves each of its `Remote`s on the
base alone, and the operator republishes their tables on the next
pass.
