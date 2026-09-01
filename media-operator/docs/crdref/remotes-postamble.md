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
