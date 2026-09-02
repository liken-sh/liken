---
title: Remotes
weight: 30
toc: true
---

<!-- Generated from deploy/remotes-crd.yaml by crdref. Do not edit. -->

A `Remote` is one physical controller: the device it is and, where
its model needs one, the [`Keymap`](/docs/reference/keymaps/) for its
model. The base table already gives a `Remote` with no `Keymap` the
arrows, OK, back, and every `KEY_*` code the device emits, and a
`Keymap` corrects a device the base gets wrong. It names no
player. A `Player` names the `Remote`s it owns through
`spec.remotes`, so the unit that owns a controller is the one that
lists it, and one controller can drive several units.

    apiVersion: media.liken.sh/v1alpha1
    kind: Remote
    metadata:
      name: den-pad
      namespace: den
    spec:
      device:
        class: gamepad
        selector: device.attributes["bluetooth.liken.sh"].address == "04:4A:5B:11:22:33"
      keymap: dualsense

One physical controller, selected by its device and mapped by the base table and, where its model needs one, by its Keymap. A Player names the Remotes it owns; the Remote names no player.

## spec

The controller, and the Keymap for its model where its model needs one.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--device"></span>`device` | [object](#specdevice) | yes | The controller itself, selected out of the devices the hardware operators publish. There are no parameters: nothing prepares an input device, and its nodes are read as they are. |
| <span id="spec--keymap"></span>`keymap` | string | no | The Keymap for this controller's model, by name. A Keymap is cluster-scoped, so the name carries no namespace. The field is optional and rarely needed: the base already passes every KEY_* code and turns the hats into the arrows, so a Keymap is for a device the kernel names wrongly. A device maps one way on every unit, as it does under hwdb. |
| <span id="spec--discovery"></span>`discovery` | boolean | no | The teaching mode for unknown hardware. The standing pod keeps every input node the claim delivered and logs each event the way a Keymap names it, so a person presses every button and reads the codes out of the pod log. The pod folds and publishes keys in discovery exactly as it does outside it, so a controller a person maps still drives its unit. Turning the mode on or off replaces the standing pod, which drops controller input for a few seconds. |

### spec.device

The controller itself, selected out of the devices the hardware operators publish. There are no parameters: nothing prepares an input device, and its nodes are read as they are.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdevice--class"></span>`class` | string | yes | The DeviceClass the claim allocates through. Consumer classes are the cluster owner's vocabulary, so the name is whatever this cluster calls its controllers. |
| <span id="specdevice--selector"></span>`selector` | string | no | A CEL expression over device.attributes that picks this one controller, such as a match on its address. Omit it, and the class alone chooses. |

## status

What the operator reports about this controller: the unit its presses reach now, and the declared codes its Keymap leaves unbound.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--player"></span>`player` | string | no | The Player this Remote's focus mark names now: the unit its presses reach, idle or playing. It is empty while no Player lists this Remote. |
| <span id="status--unbound"></span>`unbound` | [\[\]object](#statusunbound) | no | The gap, never the census: every code this controller declares that its Keymap does not bind. A controller whose Keymap binds every declared code reports nothing here, and the field is absent while no standing pod has reported. The list shrinks as the Keymap grows, so it measures a mapping's progress during discovery and stands as a completeness check after. |

### status.unbound[]

The gap, never the census: every code this controller declares that its Keymap does not bind. A controller whose Keymap binds every declared code reports nothing here, and the field is absent while no standing pod has reported. The list shrinks as the Keymap grows, so it measures a mapping's progress during discovery and stands as a completeness check after.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusunbound--code"></span>`code` | integer | yes | The raw evdev code, the number the controller reports on the wire. |
| <span id="statusunbound--name"></span>`name` | string | no | The evdev name for the code, which is the name a Keymap binds it by. It is empty when the kernel gives the code no name, and such a code cannot be bound. |
| <span id="statusunbound--type"></span>`type` | string | yes | Which event type carries the code: key for a button, abs for a hat axis. One of: `key`, `abs`. |

## The Remote's pod

The operator reconciles one pod for every `Remote` in the
cluster, whether or not a `Player` names it. The pod holds the
controller's claim, reads its evdev nodes directly, folds the base
table with the `Remote`'s `Keymap`, publishes each event under the
kernel's key name, and synthesises the repeat stream for a control
that does not autorepeat. That work runs beside the device because
that is where hwdb runs on any Linux machine, and one pod then serves
every consumer at once.

The claim tolerates the `bluetooth.liken.sh/disconnected`
taint with no time limit, so a controller that sleeps keeps its
allocation and the pod keeps running. It does not tolerate
`bluetooth.liken.sh/no-input-node`, so the pod stays `Pending` until
the controller first connects, then keeps running through every later sleep.

## Status

The operator writes two facts on a `Remote`. `status.player` is the
`Player` its focus mark names now. `status.unbound` is the gap: every
declared control that the base table and the `Keymap` together map to
nothing, or map to `none`. A declared `KEY_*` code passes as itself,
so it is never unbound, and a keyboard remote starts with an empty
list. `kubectl get remotes` shows each controller's `Keymap`, the unit
it drives, and its age. A `Keymap` that does not compile is logged by
the operator, and the `Remote` keeps its last good table.

## On the bus

Each `Remote` owns one branch of the [bus](/docs/reference/bus/)
topic tree, `remotes/<namespace>/<name>/`, under the cluster's topic
base.

| topic          | writer       | retained | carries                       |
|----------------|--------------|----------|-------------------------------|
| `events`       | the `Remote`'s pod | no       | one key event                 |
| `keys`         | operator     | yes      | the controller's key table    |
| `presence`     | the `Remote`'s pod | yes      | `{"connected": true}`         |
| `codes`        | the `Remote`'s pod | yes      | the declared code set         |
| `availability` | the `Remote`'s pod | yes      | `online` or `offline`         |
| `focus`        | operator     | yes      | the name of the `Player` it drives |
| `focus/cycle`  | the focus holder | no   | a request to advance focus    |

### events

The `Remote`'s pod publishes each event under the kernel's name for
the control, after it folded the base table with the `Keymap`:

    {"key": "KEY_UP", "value": 1}

`value` is the kernel's: 0 is the release, 1 the press, and 2 the
autorepeat. A keyboard's own autorepeat passes through, and the pod
synthesises value 2 for a gamepad button or a hat with a `repeat`
block. A control the folded table maps to nothing is not published. A
press is an event and not a state, so the topic is not retained and a
subscriber that joins later reads no stale press.

### keys

The controller's key table, as the operator compiled it: the base
folded with the `Remote`'s `Keymap`, one row per control, with the
evdev type, code, and value on the left and the key name on the
right, and the repeat delay and interval in milliseconds where a row
repeats:

    [{"type": 1, "code": 304, "value": 1, "key": "KEY_ENTER"},
     {"type": 3, "code": 17, "value": -1, "key": "KEY_UP",
      "repeatDelay": 400, "repeatInterval": 250}]

The operator is the only writer, and the topic is retained, so the pod
reads the current table the instant it connects and a `Keymap` edit
reaches it with no pod restart. The operator republishes only when the
table changes, and it clears the topic with an empty payload when the
`Remote` is deleted.

### presence

Whether the controller's event nodes are open right now:

    {"connected": true}

The `Remote`'s pod reads the controller's evdev nodes directly:
they open when the controller connects and vanish when it sleeps,
so presence comes straight from the device. The topic is retained, so the
operator reads the current value the instant it subscribes, and it
folds the flag into the [`Player` status](/docs/reference/players/)
it publishes.

### codes

The codes the controller declares, read from its nodes' capability
bitmaps at every node open:

    {"keys": [304, 305], "axes": [16, 17]}

The set is complete with no button pressed, because the bitmaps
state every code a node can report. The topic is retained, because
a declared set is a state and not an event, and the pod clears it
with an empty payload when the nodes vanish. The operator subtracts
the folded table from the set and reports the gap as
`status.unbound` on the `Remote`.

### availability

The `Remote`'s pod names this topic as its MQTT Last Will, with
`offline` as the payload, and publishes `online` once it connects.
When the pod dies, the broker writes `offline`, so the retained
presence a dead pod left behind does not read as a connected
controller.

### focus and focus/cycle

The focus mark is the plain name of the `Player` this controller
drives now, as bytes, not JSON. The operator is the only writer,
and the topic is retained, so a press reaches its unit even while
the operator is down. Every reader of the controller's presses
gates on the mark. The playback pod's command sidecar acts only when
the mark names the `Player` its film runs on. An idle unit's sidecar
acts only when the mark names that `Player` itself, and the idle
screen draws a small hexagon beside the focused controller in its
parts list.

The operator moves the marks. When a `Play` starts on a `Player`,
each of that unit's controllers is marked to it, so the controller
in a person's hand drives the film they just started. A mark that
names a deleted `Player`, or a `Player` that no longer lists the
controller, moves to the first bound `Player` by name. A `Play`
that finishes moves no mark: the unit stays focused and shows its
idle screen.

A press of `KEY_CYCLEWINDOWS` publishes on `focus/cycle`. Only the
holder of focus publishes it, the playback pod's command sidecar
during a film and the idle screen client between films. The operator
reads the request and advances the mark to the next bound `Player`
by name, wrapping the last back to the first. A controller bound to
one unit wraps to the same `Player`, and the operator republishes the
mark, which the idle screen answers with a pulse of its hexagon.
