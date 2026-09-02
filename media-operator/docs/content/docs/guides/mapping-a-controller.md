---
title: Map a new controller
weight: 20
---

# Map a new controller

A controller works before it has a `Keymap`. The standing pod passes
every `KEY_*` code through under the kernel's own name, turns the hat
axes into the arrows, and reads a gamepad's south and east buttons as
enter and back. This guide finds the codes a controller emits and
writes a `Keymap` row only for a control the base gets wrong or
leaves out. At the end, every button the controller has does what the
plastic says.

You need:

* The operator and its bus, from the
  [install](/docs/guides/install/).
* The [`bluetooth-operator`](https://bluetooth.liken.sh), with the
  controller paired to a machine. An infrared or CEC remote has no
  device to claim, because no hardware operator publishes one.
* `kubectl` access to the namespace.

The controller keeps working while you map it. Discovery changes
what the pod logs, not what it publishes, so a controller in
discovery still drives its unit.

## Declare the Remote

A `Remote` needs no `Keymap` to work. Declare it with the device
alone, using your cluster's class for controllers. The arrows, OK,
back, the volume keys, and every other control the controller reports
under a kernel name already work:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Remote
    metadata:
      name: den-remote
      namespace: house
    spec:
      device:
        class: bluetooth-input
    EOF

## Turn on discovery

    kubectl patch remote den-remote --type=merge \
      -p '{"spec":{"discovery":true}}'

The patch replaces the standing pod, which drops controller input
for a few seconds. The new pod stays `Pending` until the controller
connects for the first time, so press a button to wake it.

## Press every button

Follow the pod's log and press each button in turn:

    kubectl logs -f den-remote-remote

The pod first logs one verdict line per input node, then each press:

    remote: event3 "Handheld Remote" keep: 58 key codes, no hat axes
    remote: event3 "Handheld Remote" EV_KEY (1) KEY_PLAYPAUSE (164) press (1)
    remote:   - press: KEY_PLAYPAUSE   # code 164
    remote:     key: <a KEY_* name, or none>

The two indented lines are a `Keymap` row: paste it under
`spec.buttons`, or under `spec.axes` for a hat direction, and replace
the key line with the kernel name the control should report, or with
`none` to drop the control. A control that already reports the right
name needs no row. `KEY_PLAYPAUSE` above pauses a film as it is. Only
the press earns a row; a release or a repeat logs a line that states
so.

If the controller has modes, press every button in every mode. A
combined remote can emit different codes for one button per mode: an
air-mouse shell emits `BTN_LEFT` for its OK button in mouse mode and
`KEY_ENTER` in keys mode. `KEY_ENTER` passes on its own. `BTN_LEFT`
is a mouse button and means nothing to a screen, so it needs a row
that makes it `KEY_ENTER`.

## Read the codes you did not press

    kubectl get remote den-remote -o yaml

`status.unbound` lists every declared control that the base and the
`Keymap` together map to nothing: a hat axis with no row, a control a
row set to `none`, and a code the kernel gives no name. A declared
`KEY_*` code passes as itself, so it is never on the list, and a
keyboard remote starts with an empty list. Each entry carries the
code, its evdev name, and its event type.

## Write the Keymap

Collect the rows into a `Keymap` for the model and apply it. This one
renames the air-mouse click and gives the shell's escape key nothing
to do:

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

A row on a gamepad button that a person holds, a bumper that seeks
for example, adds a `repeat` block, because a gamepad never
autorepeats in the kernel:

    - press: BTN_TR
      key: KEY_FASTFORWARD
      repeat:
        delay: 400ms
        interval: 250ms

Then name it on the `Remote` and turn discovery off:

    kubectl patch remote den-remote --type=merge \
      -p '{"spec":{"keymap":"handheld-remote","discovery":false}}'

`status.unbound` stays after discovery ends, so it always answers
which of the controller's controls do nothing. On a keyboard remote
it holds only the controls you dropped with `none`.
