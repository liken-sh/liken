## Turning a Player off and on

`spec.power` turns the `Player` and its equipment off and on. It is
`On` or `Off`, and an absent field is `On`. `media-operator` writes it
for a remote's power key and for a `Play` that starts on a `Player`
that is off, and you can write it with `kubectl`:

    kubectl patch player studio --type merge -p '{"spec":{"power":"Off"}}'
    kubectl patch player studio --type merge -p '{"spec":{"power":"On"}}'

The last write wins, whichever wrote it. The operator writes the field
by server-side apply under the field manager `media-operator-power`,
which owns `spec.power` alone, so a GitOps copy of the `Player` that
leaves the field out never takes it back.

When `spec.power` turns `Off`, the operator:

* writes the action `off` into the `Receiver`'s
  `status.session.powerAsk`, and the equipment operator puts the
  receiver and the TV behind it in standby;
* applies the override `{power: Off}` to the screen's `Display`,
  whatever the idle screen's `offMode`;
* publishes `sleep` on the [`power`](#power) topic, and the idle
  screen lowers its shade;
* publishes `stop` on the commands topic of each `Play` that stands on
  the unit, and the run ends the way the remote's stop key ends it.

While the `Player` is `Off`, the `Receiver` session's `awake` flag
stays false and the override stays on the `Display`, whatever the idle
screen states.

When `spec.power` turns `On`, the operator lifts the override and
publishes `wake` on the `power` topic. The idle screen wakes and states
the `on` desire, the session's `awake` flag turns true, and the
equipment operator wakes the TV and the receiver and selects the unit's
input. A `Player` with no `Receiver`, such as a single monitor, does
the same without the receiver.

The remote's keys move `spec.power` while the remote's
[focus mark](/docs/reference/remotes/#focus-and-focuscycle) names this
`Player`. Each acts on the press, never on a repeat:

| Key | What it does |
|---|---|
| `KEY_POWER`, `KEY_POWER2` | Turns a `Player` that is on off, and one that is off on. |
| `KEY_SLEEP` | Turns the `Player` off, and leaves one that is off as it is. |
| `KEY_WAKEUP` | Turns the `Player` on, and leaves one that is on as it is. |
| any other key | Turns a `Player` that is off on, and does nothing else. |

A `Play` that starts on a `Player` that is off turns it on first. The
operator writes `On` with the same apply a key press sends, before it
wakes the `Receiver` session and creates the playback pod, and the
next pass runs the change to `On` as above. When the API server refuses
the apply, the `Play` still starts, and the equipment stays asleep
until `spec.power` turns `On`.

The kernel's `rc-cec` keymap gives a TV remote's Power Off Function
and Power On Function the names `KEY_SLEEP` and `KEY_WAKEUP`.
HDMI-CEC 1.3a, CEC 13.13.3, says each one puts the device in the
state it names and keeps it there when a person presses it again, so
neither is a toggle. To make a controller's power button turn the
`Player` off and never on, map it to `KEY_SLEEP` with a `Keymap` row.

The idle timers are a softer off. After `fadeAfterSeconds` the screen
draws a shade, and after `offAfterSeconds` the panel goes dark with the
`offMode` override and the session's `awake` flag turns false. The
equipment operator then hands the TV's input back and leaves the TV on.
`spec.power` stays `On` through the timers, and the next key lights
the screen again.

`status.power` holds the value the operator last applied, and the
operator acts when `spec.power` differs from it, so a change made while
the operator was down takes effect when it starts. A `Player` whose
status holds no value adopts its `spec.power` and asks the hardware for
nothing. The `PowerApplied` condition says what the last change asked
of which hardware. It is `False` with the reason `ReceiverAskFailed`
or `DisplayWriteFailed` when a write failed, and each pass asks for
the value `spec.power` holds until every write lands.

## Events

The operator and `media-api` post Kubernetes `Event`s on the `Player`.
`kubectl describe player` prints them. The API server deletes an
`Event` one hour after its last write, so the conditions and the logs
hold the facts after that.

| Reason | Type | When |
|---|---|---|
| `Present`, `PanelAway`, a `Display`'s own reason | Normal | The `Screen` condition changed. The message is the `Display`'s. |
| `NoDisplay`, `NotReported` | Warning | The `Screen` condition became `Unknown`: no `Display` names the remembered monitor, or the `Display` reports no `Connected` condition. |
| `Superseded`, `Retired` | Normal | The operator deleted one of the unit's `Play`s. The message names the `Play`, and the [Plays](/docs/reference/plays/) page gives each reason. |
| `ReceiverWriteFailed` | Warning | Writing the session on the unit's `Receiver` failed. Each pass writes it again, and posts nothing more until it lands. |
| `ReceiverWriteRecovered` | Normal | The session write landed after one or more failures. |
| `Applied` | Normal | `spec.power` changed, and every write it needed landed. The message names what the operator asked of which hardware. |
| `ReceiverAskFailed`, `DisplayWriteFailed` | Warning | A write that a change of `spec.power` needed failed. Each pass asks again until it lands. |
| `Captured` | Normal | `media-api` returned bytes of the unit. The message names the caller, the aspect, and the media type. The same caller who takes the same capture again within ten minutes adds to the count of one `Event`. |

## On the bus

The `players` tree describes the equipment, with or without a
running `Play`. [The media bus](/docs/reference/bus/) gives the rules
every topic follows and lists every writer and reader of each.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's name, activity, and parts |
| `players/{namespace}/{name}/volume` | the operator | yes | the listening level |
| `players/{namespace}/{name}/volume/commands` | any program that is not a remote | no | an ask for a step or a mute |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel desire |
| `players/{namespace}/{name}/commands` | the playback pod | no | a command for the idle pod |
| `players/{namespace}/{name}/power` | the operator | no | a wake or a sleep for the screen |

### `status`

What a screen would show about one unit: its name, what it is
doing, the `Play` it runs, and its parts with the link and the
battery level of each. The operator is the only writer, so an idle pod that just started
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
      ]
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

The unit's listening level, its muted flag, and who draws the volume
indicator:

    {"level": 0.63, "muted": false}
    {"level": 0.63, "muted": false, "indicator": "receiver"}

The level is a fraction of the device's `max`, from 0.0 to 1.0, so a
screen draws one scale for every unit. `level` and `muted` are always
written, so a reader never needs a default for either key.

`indicator` is absent unless the device that sets the level now is a
`Receiver` whose `spec.volume.indicator` is `Receiver`. That receiver
shows its own volume overlay on the TV, so the field is `"receiver"`,
and each screen tracks the level and the muted flag and draws no bar.
An absent field means the screens draw the bar. When the unit falls
back to its sinks, the field is absent again.

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
  restarts, unless the broker already holds that level;
* the level again when only `indicator` changes, so the retained
  message holds the current value.

When a unit has several sinks, each one steps in its own units, and
the topic carries the first one in `spec.sinks` order. `mpv` plays at
unity and sets no level of its own.

A client reads `status` for the name, the activity, and the parts,
and `volume` for the level. A retained message that the broker
delivers when the client subscribes sets the level and draws nothing.
Each live message draws the indicator, except a message whose
`indicator` is `"receiver"`, and a message that differs from the last
one only in `indicator`. Each of those sets the level and draws
nothing. A client publishes no level
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
ask as a press of the home key. A power key during a film reaches the
operator and not the sidecar, and the operator stops the run when the
`Player` turns off ([Turning a Player off and on](#turning-a-player-off-and-on)).

| Message | Writer | What it says |
|---|---|---|
| `{"action": "play-next", "request": {...}}` | the playback pod | A person took the up-next offer. `request` is the `Play`'s `spec.next.request`. |
| `{"action": "home"}` | the playback pod | A person pressed home during a film. The `Play` ends after it, and the client reads the ask as a press of the home key. |

### `power`

The asks the operator publishes for the unit's screen. Each message is
an ask, so none is retained. The operator names the topic in
`status.idle.bus.powerTopic` for every unit, and the screen client is
its one reader. A client publishes nothing here, and it handles no
power key: the operator reads `KEY_POWER`, `KEY_POWER2`, `KEY_SLEEP`,
and `KEY_WAKEUP` from the remote's own `events` topic and moves
`spec.power` ([Turning a Player off and on](#turning-a-player-off-and-on)).

The operator publishes `sleep` when `spec.power` turns `Off`, and
`wake` when it turns `On`. It also relays the two asks when the room's
TV speaks over HDMI-CEC. The equipment operator's CEC node workload
writes each ask into the `Television`'s `status.screenAsk`, and the
operator relays each new one once. It relays nothing for an ask that
the `Television` already held when the operator started, and nothing
for a unit whose screen is wired through no `Receiver`.

| Message | When | What the client does |
|---|---|---|
| `{"action": "wake"}` | `spec.power` turned `On`, or a person picked this unit's input in the TV's source menu while the screen sleeps. | Wakes the screen and states the `on` desire, the way a press on a sleeping screen does. |
| `{"action": "sleep"}` | `spec.power` turned `Off`, or the TV went to standby while the screen is awake. | Brings the shade down and states the `off` desire at once, while the unit plays nothing. A film goes on. |

A client that drops a `sleep` during a film leaves no hardware on: the
operator stops the `Play` on the same change, and the panel and the
equipment follow `spec.power` whatever the client states. A client
ignores every other action.
