---
title: Map a new controller
weight: 20
---

# Map a new controller

This guide writes the `Keymap` for a controller this project has
never seen. At the end, the controller's codes are known, its
`Keymap` compiles, and `status.unbound` on its `Remote` is empty.

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

A `Remote` does not need a `Keymap` to exist. Declare it with the
device alone, using your cluster's class for controllers:

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
    remote:     action: <one of audio, back, chapter, cycle-focus, down, info, left, mute, pause, right, seek, select, subtitles, up, volume>

The two indented lines are a `Keymap` entry: paste it under
`spec.buttons`, or under `spec.axes` for a hat direction, and
replace the action line with one word from its list. Only the press
earns an entry; a release or a repeat logs a line that states so,
because a `Keymap` binds the press alone.

If the controller has modes, press every button in every mode. A
combined remote can emit different codes for one button per mode: an
air-mouse shell emits `BTN_LEFT` for its OK button in mouse mode and
`KEY_ENTER` in keys mode. Bind every name a button emits, so the
button works whichever mode the shell wakes in.

## Read the codes you did not press

    kubectl get remote den-remote -o yaml

`status.unbound` lists every code the controller declares that no
`Keymap` binds yet, buttons you pressed and buttons you did not.
Each entry carries the code, its evdev name, and its event type.

## Write the Keymap

Collect the entries into a `Keymap` for the model, name the actions,
and apply it:

    kubectl apply -f - <<EOF
    apiVersion: media.liken.sh/v1alpha1
    kind: Keymap
    metadata:
      name: handheld-remote
    spec:
      buttons:
        - press: KEY_PLAYPAUSE
          action: pause
        - press: KEY_VOLUMEUP
          action: volume
          amount: 5
    EOF

Then name it on the `Remote` and turn discovery off:

    kubectl patch remote den-remote --type=merge \
      -p '{"spec":{"keymap":"handheld-remote","discovery":false}}'

Watch `status.unbound` shrink as the `Keymap` grows. It stays after
discovery ends, so it always answers which of the controller's
buttons do nothing yet.
