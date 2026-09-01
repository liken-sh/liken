---
title: Remotes
weight: 30
toc: true
---

<!-- Generated from deploy/remotes-crd.yaml by crdref. Do not edit. -->

A `Remote` is one physical controller: the device it is and the
[`Keymap`](/docs/reference/keymaps/) for its model. It names no
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

One physical controller, selected by its device and mapped by its Keymap. A Player names the Remotes it owns; the Remote names no player.

## spec

The controller and the Keymap for its model.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--device"></span>`device` | [object](#specdevice) | yes | The controller itself, selected out of the devices the hardware operators publish. There are no parameters: nothing prepares an input device, and its nodes are read as they are. |
| <span id="spec--keymap"></span>`keymap` | string | no | The Keymap for this controller's model, by name. A Keymap is cluster-scoped, so the name carries no namespace. A Player entry in spec.remotes may override it per unit, so one controller can map two ways on two units. The field is optional, because a person mapping unknown hardware has no Keymap yet: declare the Remote, run discovery, then write the Keymap the log teaches. |
| <span id="spec--discovery"></span>`discovery` | boolean | no | The teaching mode for unknown hardware. The standing pod keeps every input node the claim delivered and logs each event the way a Keymap names it, so a person presses every button and reads the codes out of the pod log. Events still publish to the bus, so a controller in discovery still drives its unit. Turning the mode on or off replaces the standing pod, which drops controller input for a few seconds. |

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
controller's claim, reads its evdev nodes directly, and publishes
to the bus. The claim tolerates the `bluetooth.liken.sh/disconnected`
taint with no time limit, so a controller that sleeps keeps its
allocation and the pod keeps running. It does not tolerate
`bluetooth.liken.sh/no-input-node`, so the pod stays `Pending` until
the controller first connects, then keeps running through every later sleep.

## Status

The operator writes two facts on a `Remote`. `status.player` is the
`Player` its focus mark names now. `status.unbound` is the gap:
every code the controller declares that its `Keymap` does not bind,
which empties as the `Keymap` grows. `kubectl get remotes` shows
each controller's `Keymap`, the unit it drives, and its age. A
`Remote` whose `Keymap` does not compile shows the failure on the
`Play` that uses it.

## On the bus

Each `Remote` owns one branch of the [bus](/docs/reference/bus/)
topic tree, `remotes/<namespace>/<name>/`, under the cluster's topic
base.

| topic          | writer       | retained | carries                       |
|----------------|--------------|----------|-------------------------------|
| `events`       | the `Remote`'s pod | no       | one evdev event               |
| `presence`     | the `Remote`'s pod | yes      | `{"connected": true}`         |
| `codes`        | the `Remote`'s pod | yes      | the declared code set         |
| `availability` | the `Remote`'s pod | yes      | `online` or `offline`         |
| `focus`        | operator     | yes      | the name of the `Player` it drives |
| `focus/cycle`  | the focus holder | no   | a request to advance focus    |

### events

The `Remote`'s pod publishes each event as the controller's own evdev
numbers, untranslated:

    {"type": 1, "code": 304, "value": 1}

`type` 1 is `EV_KEY` and `type` 3 is `EV_ABS`. `code` 304 is
`BTN_SOUTH`, and `value` 1 is the press, 0 the release. The pod
publishes only the events a `Keymap` can bind: every key code, and
the two hat axes. The keymap stays off this topic, so one `Remote`
can feed two players that map it differently. A press is an event
and not a state, so the topic is not retained and a subscriber that
joins later reads no stale press.

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
the `Keymap`'s bindings from the set and reports the gap as
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
gates on the mark. A `Play`'s translator acts only when the mark
names the `Player` its film runs on. An idle unit's sidecar acts
only when the mark names that `Player` itself, and the idle screen
draws a small hexagon beside the focused controller in its parts
list.

The operator moves the marks. When a `Play` starts on a `Player`,
each of that unit's controllers is marked to it, so the controller
in a person's hand drives the film they just started. A mark that
names a deleted `Player`, or a `Player` that no longer lists the
controller, moves to the first bound `Player` by name. A `Play`
that finishes moves no mark: the unit stays focused and shows its
idle screen.

A press bound to `cycle-focus` publishes on `focus/cycle`. Only the
holder of focus publishes it, the translator during a film and the
idle sidecar between films. The operator reads the request and
advances the mark to the next bound `Player` by name, wrapping the
last back to the first. A controller bound to one unit wraps to
the same `Player`, and the operator republishes the mark, which
the idle screen answers with a pulse of its hexagon.
