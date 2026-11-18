---
title: Map a new controller
weight: 20
description: "Declare a Remote, find the codes a controller emits with discovery, and write a Keymap row for each button the base mapping gets wrong. Use when adding a paired controller to a Player, or when a controller's button does the wrong thing or nothing."
---

# Map a new controller

A controller works before it has a `Keymap`. The `Remote`'s pod passes
every `KEY_*` code through with the kernel's name for it, turns the hat
axes into the arrow keys, and maps a gamepad's south and east buttons to
enter and back. In this guide you find the codes that your controller
sends, and write a `Keymap` row only for the buttons that this default
mapping gets wrong or leaves out. At the end, every button on the
controller does what its label says.

You need:

* The operator and its bus, from the
  [install](/docs/guides/install/).
* The [`bluetooth-operator`](https://liken.sh/bluetooth/), with the
  controller paired to a machine. A TV remote sends its buttons through
  a USB CEC adapter instead. `liken` publishes the adapter's input
  device with the `subsystem` attribute `input`, and the kernel names
  the remote's buttons with its `rc-cec` keymap. The adapter passes
  them on only while its `CECBus` has `spec.mode: Control`. You cannot claim an
  infrared remote, because no hardware operator publishes it as a
  device.
* `kubectl` access to the namespace.

Discovery changes only what the `Remote`'s pod logs, not what it
publishes, so the controller keeps controlling its `Player` while you
map it.

## Declare the Remote

Declare the `Remote` with only the device, using your cluster's device
class for controllers, here `bluetooth-input`. The arrow keys, OK,
back, the volume keys, and every other control that the controller
reports under a kernel name already work:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Remote
    metadata:
      name: den-remote
      namespace: media
    spec:
      device:
        class: bluetooth-input
    EOF

## Turn on discovery

    kubectl patch remote den-remote --type=merge \
      -p '{"spec":{"discovery":true}}'

The patch replaces the controller pod, so controller input stops for a
few seconds. The new pod stays `Pending` until the controller connects,
so press a button to wake the controller.

## Press every button

Follow the pod's log and press each button in turn:

    kubectl logs -f den-remote-remote

The pod logs one line for each input node of the controller, then a
line when the controller connects, then the lines for each press:

    remote: event3 "Handheld Remote" keep: 58 key codes, no hat axes
    remote: controller connected on event3 "Handheld Remote": 58 key codes, no hat axes
    remote: event3 "Handheld Remote" EV_KEY (1) BTN_SOUTH (304) press (1)
    remote:   - press: BTN_SOUTH   # code 304
    remote:     key: <a KEY_* name, or none>
    remote: event3 "Handheld Remote": BTN_SOUTH (304) pressed, published KEY_ENTER to liken/media/remotes/media/den-remote/events

The two indented lines are a `Keymap` row. Paste it under
`spec.buttons`, or under `spec.axes` for a hat direction, and replace
the value of `key` with the kernel name that the control should report,
or with `none` to drop the control.

The last line shows what the pod published for the press. Read it
before you write a row, because the base mapping may already do what
you want. In the example, the pod already publishes `KEY_ENTER` for
`BTN_SOUTH`, so that button needs no row. A release or a repeat logs
only the raw event line, with a note that a `Keymap` binds only the
press.

Some Bluetooth LE remotes, such as a T6, do not send the press that
woke them from sleep. Instead, `bluetooth-operator` writes a press of
`KEY_UNKNOWN` (code 240), so discovery logs that press each time such a
remote wakes. An X6 air-mouse remote does send the press that
woke it, so discovery logs the button itself. Do not map
`KEY_UNKNOWN`. A `Keymap` row on it acts on every wake, and on any real
button that the kernel also reports as `KEY_UNKNOWN`.

When the idle screen has dimmed, the press that wakes it also acts on
the screen, with two exceptions. When the panel is off, the press only
turns the screen on, because you cannot see what it would act on. The
panel is off after the idle screen's `offAfterSeconds` has passed, or after the
`Player` or the TV turned off. And a press that reaches the screen as
`KEY_BACK`, `KEY_ESC`, `KEY_EXIT`, or `KEY_BACKSPACE`, from the base
mapping or from a `Keymap` row, only wakes the screen. The idle screen
dims when it receives back, so without this rule the waking press would
dim the screen again.

If the controller has modes, press every button in every mode. A
combined remote can send a different code for the same button in each
mode. For example, an air-mouse remote sends `BTN_LEFT` for its OK
button in mouse mode, and `KEY_ENTER` in keys mode. `KEY_ENTER` works
without a row. `BTN_LEFT` is a mouse button, and a screen ignores it, so
it needs a row that maps it to `KEY_ENTER`.

## Read the codes you did not press

    kubectl get remote den-remote -o yaml

`status.unbound` lists every control that the controller declares and
that neither the base mapping nor the `Keymap` maps to a key: a hat
axis with no row, a control whose row sets `none`, and a code that the
kernel has no name for. A `KEY_*` code passes through as itself, so it
is never in the list, and a keyboard remote starts with an empty list.
Each entry has the code, its evdev name, and its event type.

## Write the Keymap

Collect the rows into a `Keymap` for the controller model and apply
it. This one maps the air-mouse click to `KEY_ENTER` and drops the
escape key:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: handheld-remote
    spec:
      buttons:
        - press: BTN_LEFT
          key: KEY_ENTER
        - press: KEY_ESC
          key: none
    EOF

When the pod has received the new key table, a press of the escape key
logs that the pod dropped it:

    remote: event3 "Handheld Remote": KEY_ESC (1) pressed, published nothing, because the key table maps it to none

For a gamepad button that you hold down, such as a bumper that seeks,
add a `repeat` block to the row, because the kernel does not autorepeat
gamepad buttons. The pod sends the press, waits for `delay`, and then
sends the key every `interval` until you release the button. The
defaults are `400ms` and `300ms`:

    - press: BTN_TR
      key: KEY_FASTFORWARD
      repeat:
        delay: 400ms
        interval: 250ms

Then set the `Keymap` on the `Remote` and turn discovery off:

    kubectl patch remote den-remote --type=merge \
      -p '{"spec":{"keymap":"handheld-remote","discovery":false}}'

`status.unbound` is kept up to date after discovery ends, so it still
shows which controls of the controller do nothing.
