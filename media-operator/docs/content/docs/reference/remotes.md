---
title: Remotes
weight: 30
toc: true
---

# Remotes

A `Remote` is one physical controller: the device it is and the
[`Keymap`](/docs/reference/keymaps/) for its model. It names no
player. A `Player` names the `Remote`s it owns through
`spec.remotes`, so the unit that owns a controller is the one that
lists it, and one controller can drive several units.

## The spec

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

`spec.device` selects the controller the way a `Player` selects a
display: out of the devices the hardware operators publish.

- `device.class` names the
  [`DeviceClass`](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
  the claim allocates through. Consumer classes are the cluster
  owner's vocabulary, so the name is whatever this cluster calls its
  controllers.
- `device.selector` is a CEL expression over `device.attributes`
  that picks this one controller, such as a match on its address.
  Omit it, and the class alone chooses.

There is no `parameters` field, because nothing prepares an input
device the way a codec prepares an audio sink. Its nodes are read as
they are.

`spec.keymap` names the `Keymap` for this controller's model. A
`Keymap` is cluster-scoped, so the name carries no namespace. A
`Player` entry in `spec.remotes` may override it per unit, so one
controller can map two ways on two units.

## The Remote's pod

The operator reconciles one pod for every `Remote` in the
cluster, whether or not a `Player` names it. The pod holds the
controller's claim, reads its evdev nodes directly, and publishes
to the bus. The claim tolerates the `bluetooth.liken.sh/disconnected`
taint with no time limit, so a controller that sleeps keeps its
allocation and the pod keeps running. It does not tolerate
`bluetooth.liken.sh/no-input-node`, so the pod stays `Pending` until
the controller first connects, then keeps running through every later sleep.

## No status

The operator reports nothing on a `Remote`, so there is no status
subresource. `kubectl get remotes` shows each controller's `Keymap`
and its age. A `Remote` whose `Keymap` does not compile shows the
failure on the `Play` that uses it.

## On the bus

Each `Remote` owns one branch of the [bus](/docs/reference/bus/)
topic tree, `remotes/<namespace>/<name>/`, under the cluster's topic
base.

| topic          | writer       | retained | carries                       |
|----------------|--------------|----------|-------------------------------|
| `events`       | the `Remote`'s pod | no       | one evdev event               |
| `presence`     | the `Remote`'s pod | yes      | `{"connected": true}`         |
| `availability` | the `Remote`'s pod | yes      | `online` or `offline`         |
| `focus`        | operator     | yes      | the name of the owning `Play` |
| `focus/cycle`  | translator   | no       | a request to advance focus    |

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

### availability

The `Remote`'s pod names this topic as its MQTT Last Will, with
`offline` as the payload, and publishes `online` once it connects.
When the pod dies, the broker writes `offline`, so the retained
presence a dead pod left behind does not read as a connected
controller.

### focus and focus/cycle

The focus mark is the plain name of the `Play` that owns this
controller now, as bytes, not JSON. The operator is the only writer,
and the topic is retained, so a press reaches its `Play` even while
the operator is down. Each translator for the controller gates on
the mark: it acts on a press only when the mark names its own
`Play`, and it drops every other press.

A press bound to `cycle-focus` publishes on `focus/cycle`. Only the
translator that holds focus publishes it, and the operator reads it
and advances the mark to the next unit. The operator also moves a
mark off a `Play` that finished, so a controller with a live `Play`
always has a translator that acts.
