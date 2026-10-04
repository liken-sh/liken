## On the bus

The `players` tree describes the equipment, with or without a
running `Play`. [The media bus](/docs/reference/bus/) gives the rules
every topic follows and lists every writer and reader of each.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's name, activity, parts, and power mode |
| `players/{namespace}/{name}/volume` | the operator | yes | the listening level |
| `players/{namespace}/{name}/volume/commands` | any program that is not a remote | no | an ask for a step or a mute |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel desire |
| `players/{namespace}/{name}/commands` | the playback pod | no | a command for the idle pod |
| `players/{namespace}/{name}/power` | the idle pod and the operator | no | an ask for the room's power, or for the screen |

### `status`

What a screen would show about one unit: its name, what it is
doing, the `Play` it runs, and its parts with the link and the
battery level of each. It also states where a power press goes.
The operator is the only writer, so an idle pod that just started
draws the live state the broker already holds, with no request to
the operator. The operator republishes only when the payload changes,
and it clears the topic with an empty payload when the `Player` is
deleted.

    {
      "displayName": "Studio Lab",
      "activity": "Playing",
      "play": {"name": "evening-film", "title": "An Evening Film",
               "displayAlive": {"status": "False", "reason": "Restarting",
                                "message": "the display restarted 2 times: Error (exit code 1)"}},
      "components": [
        {"name": "Portable Screen", "kind": "display"},
        {"name": "Built-in Speakers", "kind": "sink"},
        {"name": "Studio Controller", "kind": "remote", "connected": true,
         "battery": 62, "focused": true}
      ],
      "power": "room"
    }

`activity` is the same word the Kubernetes status carries. `play` is
present while a run starts or plays: `name` is the object a person
finds with `kubectl`, and `title` is the one line a screen draws. A
component's `kind` is `display`, `sink`, or `remote`, and only a
remote carries `connected`, because a wired screen has no link that
can go down. `battery` is the charge the controller's `Peripheral`
reports, from 0 to 100, and a device that reports none omits the key.
`focused` appears on the one remote whose
[focus mark](/docs/reference/remotes/#focus-and-focuscycle) names
this `Player`, and the idle screen draws a small hexagon beside that
controller in its parts list. Every other component omits the key.

`power` is `room` while the unit's screen is wired through a
`Receiver`: a power press is then an ask on the
[`power`](#power) topic, and the equipment operator turns the room
off or on. It is `screen` while the screen is wired to no
`Receiver`: the client handles a power press itself and lowers its
shade, and it ignores `wake` and `sleep` on the `power` topic. A
client reads the field at each press, so a `Receiver` that is wired
or removed changes where the next press goes with no pod restart.
The operator omits the field for a unit it has not matched against
the `Receiver`s yet: after the operator starts or the `Player` is
created, until the operator's first pass reaches the unit. That pass
can take seconds on a busy API server. A client keeps the mode it last
read through such a message. A client that has read no mode treats a
non-empty power topic as `room`, the rule an operator that predates
the field follows.

The field reads `screen` for a wired unit while the unit's idle claim
is unallocated and its screen does not resolve, for example after a
restart that finds the claim with no allocation. It reads `room` again
from the pass after the claim is allocated. A short gap in a screen
that already resolved keeps `room` for up to 90 seconds.

A client that draws the idle screen must read this field to decide
where a power press goes. The power topic in
`status.idle.bus.powerTopic` is set for every unit, so a client that
reads the topic's presence as the sign of a `Receiver` treats every
unit as `room`. Upgrade `library-operator` together with this
operator, because an older media browser follows that rule. When this
operator is upgraded, the idle pod of each unit with no `Receiver` is
replaced once, because its environment gains the power topic.

`play.displayAlive` is the run's
[`DisplayAlive` condition](/docs/reference/plays/#statusconditions)
with its `status`, `reason`, and `message`, and the key is absent
while the run reports none. The operator derives it from the playback
pod, so a client reads a crashed display off the topic it already
holds for the unit and sends no request to the API server. A client
that draws the unit's state can show the `message` while `status` is
`False`. It needs no repair of its own: once the display has
restarted more than twice in one run, the operator ends the run, and
this topic reads `Idle` as it does after any film.

### `volume`

The unit's listening level and its muted flag:

    {"level": 0.63, "muted": false}

The level is a fraction of the device's `max`, from 0.0 to 1.0, so a
screen draws one scale for every unit. Both fields are always
written, so a reader never needs a default for a missing key.

The operator is the only writer. It sets the room's level through the
device that sets it: the
[`Receiver`](https://liken.sh/equipment/docs/reference/receivers/) the
unit's screen is wired into, while its `Reachable` condition is
`True`, and otherwise every
[`Sink`](https://liken.sh/audio/docs/reference/sinks/) in the
`Player`'s `status.sinks`. A press of `KEY_VOLUMEUP` or
`KEY_VOLUMEDOWN`, from a remote whose focus mark names this `Player`,
moves a target one step of the device's `spec.volume.step`. A held
key moves it once for each repeat. `KEY_MUTE` toggles the mute, and
`KEY_UNMUTE` clears it. The target never goes past the device's
`spec.volume.max`. The operator writes the target into the device's
`status.session.volumeAsk`, at most once every 100 ms, and the
device's operator applies it.

The operator publishes here:

* the target, as soon as a press moves it, so the indicator moves at
  bus speed;
* the level the device reports while no target is pending, such as a
  turn of a receiver's own knob;
* the level the device reports when it did not report the target
  within one second, as when a receiver's own limit is below the
  target;
* each unit's level once a broker session's retained values have had
  time to arrive, after the operator starts and after the broker
  restarts, unless the broker already holds that level.

When a unit has several sinks, each one steps in its own units, and
the topic carries the first one in `spec.sinks` order. `mpv` plays at
unity and sets no level of its own.

A client reads `status` for the name, the activity, and the parts,
and `volume` for the level. A retained message that the broker
delivers when the client subscribes sets the level and draws nothing.
Each live message draws the indicator. A client publishes no level
here, and it handles no volume key: the operator reads the keys from
the remote's own `events` topic.

### `volume/commands`

The asks for the unit's level from a program that is not a remote,
one segment below `volume`. They are not retained, because an ask is
an event. The operator treats each message the same as a press:

| Message | What the operator does |
|---|---|
| `{"step": "up"}` | Moves the level one step up, as `KEY_VOLUMEUP` does. |
| `{"step": "down"}` | Moves the level one step down, as `KEY_VOLUMEDOWN` does. |
| `{"mute": "toggle"}` | Toggles the mute, as `KEY_MUTE` does. |
| `{"mute": true}` | Mutes the unit. |
| `{"mute": false}` | Clears the mute, as `KEY_UNMUTE` does. |

A message with no step and no mute does nothing, and the operator's
log says so.

### `panel`

The desire the idle screen client states for the unit's screen:

    {"desire": "off"}

The two desires are `on` and `off`. The client holds no API
credentials and writes no hardware, so the operator reads this topic
and applies or lifts `spec.override` on the screen's `Display`.

A client that starts reads the retained desire before it states one,
and it states a desire only when a press, a starting `Play`, or the
off window changes the panel. A client that restarts in a dark room
keeps the room dark. On a reconnect, a client states again the desire
it holds, so a broker that restarted holds the desire again. The
operator reads no desire as no statement: it writes no override, and
the session on a receiver keeps the awake flag it carries. What
the panel actually shows comes back the other way, from the
`Display`'s observed state into `status.panel`. The operator clears
the topic with an empty payload when the unit's controller becomes
`media.liken.sh/none`, so a restarted operator does not read the last
desire of a client that no longer runs.

### `commands`

The commands around the `Player`'s idle screen. They are not
retained ([why](/docs/reference/bus/#retained-state-and-events)), and
a controller never sends one directly. A client publishes nothing
here.

One program writes here. The playback pod's command sidecar publishes
`play-next` when a person takes the up-next offer on the scrubber, and
its `request` is the `Play`'s own `spec.next.request`, byte for byte.
The client that wrote the `Play` reads that ask and creates the next
`Play`. The same sidecar publishes `home` when a person presses home
during a film, just before the `Play` ends, and the client reads that
ask as a press of the home key. It publishes `power` when a person
presses power during a film, also just before the `Play` ends. The
client holds that ask until this `Player`'s status reads `Idle`, and
then answers it as a power press between films: in the power mode
`room`, it publishes the toggle on the power topic, and in the mode
`screen`, it lowers the shade. The toggle waits for `Idle`
so that the equipment operator reads the room after the film ended. A
client that reads no `Idle` within 10 seconds drops the ask. The
sidecar publishes `power-off` in place of `power` for `KEY_SLEEP`, the
name the kernel gives a TV remote's Power Off Function, and the client
answers it the way it answers a press of `KEY_SLEEP`: `off` on the
power topic in the mode `room`, or the shade in the mode `screen`.

| Message | Writer | What it says |
|---|---|---|
| `{"action": "play-next", "request": {...}}` | the playback pod | A person took the up-next offer. `request` is the `Play`'s `spec.next.request`. |
| `{"action": "home"}` | the playback pod | A person pressed home during a film. The `Play` ends after it, and the client reads the ask as a press of the home key. |
| `{"action": "power"}` | the playback pod | A person pressed power during a film. The `Play` ends after it, and the client answers the ask as a power press once the status reads `Idle`. |
| `{"action": "power-off"}` | the playback pod | A person pressed a TV remote's Power Off Function during a film. The `Play` ends after it, and the client answers the ask as a press of `KEY_SLEEP` once the status reads `Idle`. |

### `power`

The room's power, for a unit whose screen is wired through a
`Receiver`. The operator names the topic in
`status.idle.bus.powerTopic` for every unit, so a delegate's pod stays
the same when a `Receiver` is wired or removed. The
[`status`](#status) topic's `power` field says whether the topic is in
use: `room` while a `Receiver` is wired, and `screen` while none is.
Each message is an ask, so none is retained.

The idle screen client publishes a power press between films here,
in the power mode `room`.
The operator writes each ask into the `Receiver`'s
`status.session.powerAsk`, and the equipment operator answers it:

| Message | Key | What the equipment operator does |
|---|---|---|
| `{"action": "toggle"}` | `KEY_POWER`, `KEY_POWER2` | Turns a room that is on off, and a room that is off on. |
| `{"action": "off"}` | `KEY_SLEEP` | Turns a room that is on off, and leaves a room that is off as it is. |
| `{"action": "on"}` | `KEY_WAKEUP` | Turns a room that is off on, and leaves a room that is on as it is. |

The kernel's `rc-cec` keymap gives a TV remote's Power Off Function
and Power On Function the names `KEY_SLEEP` and `KEY_WAKEUP`.
HDMI-CEC 1.3a, CEC 13.13.3, says each one puts the device in the
state it names and keeps it there when a person presses it again, so
neither is a toggle.

The operator publishes two asks for the screen here, when the room's
TV speaks over HDMI-CEC. The equipment operator's CEC node workload
writes each ask into the `Television`'s `status.screenAsk`, and the
operator relays each new one once. It relays nothing for an ask that
the `Television` already held when the operator started, and nothing
for a unit whose screen is wired through no `Receiver`. A client in
the power mode `screen` ignores both asks.

| Message | When | What the client does |
|---|---|---|
| `{"action": "wake"}` | A person picked this unit's input in the TV's source menu while the screen sleeps. | Wakes the screen and states the `on` desire, the way a press on a sleeping screen does. |
| `{"action": "sleep"}` | The TV went to standby while the screen is awake. | Brings the shade down and states the `off` desire at once, while the unit plays nothing. A film goes on. |

Each program ignores the actions it does not answer, so a client that
reads its own ask come back changes nothing.
