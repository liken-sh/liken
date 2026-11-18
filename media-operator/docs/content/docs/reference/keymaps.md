---
title: Keymaps
weight: 40
toc: true
---

<!-- Generated from deploy/keymaps-crd.yaml by crdref. Do not edit. -->

# `Keymap`

A `Keymap` changes the key name that a controller's buttons and hat
axes send. You write one for each controller model, and each
[`Remote`](/docs/reference/remotes/) of that model refers to it. You
need one only when the base table gets a control wrong. The base table
sends every `KEY_*` code as itself, turns the hat axes into the arrow
keys, and maps a gamepad's south and east buttons to `KEY_ENTER` and
`KEY_BACK`. So a `Remote` with no `Keymap` already works.

A `Keymap` is cluster-scoped, like a `DeviceClass`, because a model's
buttons are the same in every namespace. A `Remote` in any namespace
refers to it by name alone.

Every binding uses evdev names. `press` or `axis` is the control, and
`key` is the key name that the control sends. Linux controller drivers
report the south face button as `BTN_SOUTH`, whatever is printed on
the button. A key of `none` makes the control send nothing. A `Keymap`
only renames controls. The program that reads the key decides what it
does. For example, `media-operator` turns each `KEY_VOLUMEUP` into one
step up of the `Player`'s volume.

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

An axis binding also needs a direction, `value`, so axes have their own
list. A `Keymap` must have at least one entry in `buttons` or `axes`.

The key names that a controller model reports in place of the names the kernel gives its controls. A Remote uses a Keymap when its `spec.keymap` names it. Write one only for a model whose controls the default mapping gets wrong.

## spec

The entries: `buttons` for buttons and keys, and `axes` for the directions of a d-pad. Set at least one of them.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--buttons"></span>`buttons` | [\[\]object](#specbuttons) | no | Button entries. Each entry sets the key name that one button reports. A button with no entry keeps its default mapping. |
| <span id="spec--axes"></span>`axes` | [\[\]object](#specaxes) | no | D-pad entries. A gamepad's d-pad reports two hat axes, not buttons. Each entry sets the key name that one direction of a hat axis reports. |

### spec.buttons[]

Button entries. Each entry sets the key name that one button reports. A button with no entry keeps its default mapping.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttons--press"></span>`press` | string | yes | The button's evdev name as the kernel reports it, such as `BTN_SOUTH` on a gamepad or `KEY_PLAYPAUSE` on a media remote. The operator refuses the Keymap when it does not know the name. To find a button's name, set `spec.discovery: true` on the Remote, press the button, and read the name in the log of the Remote's pod. Pattern: `^(BTN\|KEY)_[A-Z0-9_]+$`. |
| <span id="specbuttons--key"></span>`key` | string | yes | The evdev key name that the button reports instead, such as `KEY_ENTER`, or `none` to drop the button's presses. Pattern: `^((BTN\|KEY)_[A-Z0-9_]+\|none)$`. |
| <span id="specbuttons--repeat"></span>`repeat` | [object](#specbuttonsrepeat) | no | Repeats the key while the button is held. The Remote's pod sends the press, waits for `delay`, and then sends the key every `interval` until the button is released. Set it for a gamepad button, because the kernel does not repeat gamepad buttons. A keyboard key repeats without it. The pod stops a repeat 30 seconds after it starts, because a controller that goes to sleep while a button is held sends no release. The pod limits `delay` and `interval` to 60 seconds each. |

#### spec.buttons[].repeat

Repeats the key while the button is held. The Remote's pod sends the press, waits for `delay`, and then sends the key every `interval` until the button is released. Set it for a gamepad button, because the kernel does not repeat gamepad buttons. A keyboard key repeats without it. The pod stops a repeat 30 seconds after it starts, because a controller that goes to sleep while a button is held sends no release. The pod limits `delay` and `interval` to 60 seconds each.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specbuttonsrepeat--delay"></span>`delay` | string | no | How long the button must be held before the repeat starts, as a duration such as `400ms`. The default is `400ms`. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specbuttonsrepeat--interval"></span>`interval` | string | no | The time between repeats, as a duration such as `300ms`. The default is `300ms`. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

### spec.axes[]

D-pad entries. A gamepad's d-pad reports two hat axes, not buttons. Each entry sets the key name that one direction of a hat axis reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxes--axis"></span>`axis` | string | yes | The hat axis: `ABS_HAT0X` for left and right, or `ABS_HAT0Y` for up and down. Analog sticks cannot be mapped to keys. One of: `ABS_HAT0X`, `ABS_HAT0Y`. |
| <span id="specaxes--value"></span>`value` | integer | yes | The direction: `-1` for left or up, `1` for right or down. Releasing the d-pad back to the center stops a repeat. One of: `-1`, `1`. |
| <span id="specaxes--key"></span>`key` | string | yes | The evdev key name that this direction reports, or `none` to drop it. The default mapping already turns the d-pad into the arrow keys, so add an entry only when a direction should report another key. Pattern: `^((BTN\|KEY)_[A-Z0-9_]+\|none)$`. |
| <span id="specaxes--repeat"></span>`repeat` | [object](#specaxesrepeat) | no | Repeats the key while the d-pad is held in this direction, the same way as a button's `repeat`. An entry replaces the default mapping of its direction, and the default's repeat with it, so set this block when the direction should still repeat. |

#### spec.axes[].repeat

Repeats the key while the d-pad is held in this direction, the same way as a button's `repeat`. An entry replaces the default mapping of its direction, and the default's repeat with it, so set this block when the direction should still repeat.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specaxesrepeat--delay"></span>`delay` | string | no | How long the direction must be held before the repeat starts, as a duration such as `400ms`. The default is `400ms`. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |
| <span id="specaxesrepeat--interval"></span>`interval` | string | no | The time between repeats, as a duration such as `300ms`. The default is `300ms`. Pattern: `^[0-9]+(\.[0-9]+)?(ms\|s)$`. |

## No status

A `Keymap` has no status, so `kubectl get keymaps` shows only each
`Keymap`'s age. On each pass, the operator builds the key table of each
`Remote` from the base table and the `Remote`'s `Keymap`. When a
`press` or `key` name is not one the operator knows, or a repeat time
does not parse, the operator logs the error and posts a
`KeymapRefused` Warning `Event` on each `Remote` that uses the
`Keymap`. Those `Remote`s keep their last good table.

## On the bus

A `Keymap` has no topic of its own. The operator publishes each
`Remote`'s key table, retained, on that `Remote`'s `keys` topic, in the
[`Remote`'s topic tree](/docs/reference/remotes/). When a `Keymap` has
an error, the operator publishes nothing, so the broker keeps the last
good table. When you delete a `Keymap`, the operator publishes the base
table alone to each `Remote` that used it, on the next pass.
