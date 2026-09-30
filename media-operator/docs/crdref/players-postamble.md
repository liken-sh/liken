## On the bus

The `players` tree describes the equipment, with or without a
running `Play`. [The media bus](/docs/reference/bus/) gives the rules
every topic follows and lists every writer and reader of each.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's name, activity, and parts |
| `players/{namespace}/{name}/volume` | the operator, the pod that handles a press, and the equipment operator | yes | the listening level |
| `players/{namespace}/{name}/volume/owner` | the equipment operator | yes | who applies the level |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel desire |
| `players/{namespace}/{name}/commands` | the playback pod | no | a command for the idle pod |
| `players/{namespace}/{name}/power` | the idle pod and the equipment operator | no | an ask for the room's power, or for the screen |

### `status`

What a screen would show about one unit: its name, what it is
doing, the `Play` it runs, and its parts with the link and the
battery level of each.
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

The unit's listening level and its muted flag:

    {"level": 40, "muted": false}

Both fields are always written, so a reader never needs a default
for a missing key. The level runs 0 to 100, and 100 is unity, the
player's own default and the cap. A published level outside 0 to 100
is clamped to the range.

Three writers publish here. The operator writes unity when it seeds
a unit the broker holds no level for, it writes the level it holds
again after a broker restart, and it writes a `Play`'s
`spec.volume` over the unit's current state before it creates the
pod. The pod that handles a `volume` or `mute` press writes the next
state back. That pod is the playback pod's command sidecar during a
film and the idle screen client between films, and each computes the
next state from the last message the topic delivered. The
[equipment operator](https://liken.sh/equipment/docs/reference/receivers/)'s
receiver session writes the receiver's true level here whenever its
mark on `volume/owner` is non-empty: the position it adopts when the
session starts, and the position the receiver reports after a press
or a turn of its own knob.

The seed waits out the broker's retained catch-up after each connect,
and it skips a unit while a `Play` stands on it. The playback pod's
command sidecar holds the level the room hears, and it publishes that
level again on each reconnect, so a broker restart does not move a
playing film to unity. Between films the operator holds the level:
after a broker restart, it publishes the level it last read for each
unit the new session delivers none for, unless equipment owns that
level. An operator and a broker that restart together hold no level,
so the seed writes unity, and the next `Play` starts there.

Every pod for the unit subscribes and applies what it reads, so the
unit plays at the one level the topic holds. While the owner mark is
non-empty, no pod applies the level: each holds `mpv` at unity, volume
100 and unmuted, and the equipment applies the level instead. A
press still publishes the next state here, and the equipment reads
it as a press and moves one step in its direction.

A client reads `status` for the name, the activity, and the parts,
and `volume` for the level. The first `volume` message of a session
is the broker's retained catch-up, which sets the level and shows no
indicator, and every message after it is a press.

### `volume/owner`

The mark that says equipment applies the unit's level, one segment
below `volume`:

    {"owner": "receiver/den-receiver"}

The equipment operator's receiver session is the only writer. It
publishes the mark, retained, on every fresh broker session while it
holds a receiver for the unit, and it clears the mark with an empty
retained payload when the session stops. The topic is the session's
MQTT Last Will, with an empty retained payload, so a session that
dies without a clean disconnect clears its own mark.

A non-empty payload means equipment owns the level. An empty payload,
or no message at all, means no owner holds it and the pods apply the
level themselves. The value inside names the owner and nothing reads
it: every reader tests only whether the payload is empty.

Three readers subscribe. The playback pod's command sidecar holds
`mpv` at unity while the mark is non-empty and applies the level the topic
last delivered when the mark clears. It re-applies the held state
once `mpv`'s socket opens, because the broker delivers the retained
mark and level within milliseconds of the subscribe and `mpv` opens
its socket seconds later. The operator builds a playback pod with no
`--volume` flag while the mark is non-empty, so `mpv` starts at its own
default, unity. A delegate's idle screen client reads the topic from
`status.idle.bus.volumeOwnerTopic` and draws no level of its own
while the mark is non-empty.

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
then answers it as a power press between films: on a unit wired
through a `Receiver`, it publishes the toggle on the power topic, and
on a unit with none, it lowers the shade. The toggle waits for `Idle`
so that the equipment operator reads the room after the film ended. A
client that reads no `Idle` within 10 seconds drops the ask. The
sidecar publishes `power-off` in place of `power` for `KEY_SLEEP`, the
name the kernel gives a TV remote's Power Off Function, and the client
answers it the way it answers a press of `KEY_SLEEP`: `off` on the
power topic, or the shade on a unit with no `Receiver`.

| Message | Writer | What it says |
|---|---|---|
| `{"action": "play-next", "request": {...}}` | the playback pod | A person took the up-next offer. `request` is the `Play`'s `spec.next.request`. |
| `{"action": "home"}` | the playback pod | A person pressed home during a film. The `Play` ends after it, and the client reads the ask as a press of the home key. |
| `{"action": "power"}` | the playback pod | A person pressed power during a film. The `Play` ends after it, and the client answers the ask as a power press once the status reads `Idle`. |
| `{"action": "power-off"}` | the playback pod | A person pressed a TV remote's Power Off Function during a film. The `Play` ends after it, and the client answers the ask as a press of `KEY_SLEEP` once the status reads `Idle`. |

### `power`

The room's power, for a unit whose screen is wired through a
`Receiver`. The operator names the topic in the session it applies on
the `Receiver`, and in `status.idle.bus.powerTopic`, only for such a
unit. Each message is an ask, so none is retained.

The idle screen client publishes a power press between films here,
and the equipment operator answers it:

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

The equipment operator publishes two asks for the screen here, when
the room's TV speaks over HDMI-CEC:

| Message | When | What the client does |
|---|---|---|
| `{"action": "wake"}` | A person picked this unit's input in the TV's source menu while the screen sleeps. | Wakes the screen and states the `on` desire, the way a press on a sleeping screen does. |
| `{"action": "sleep"}` | The TV went to standby while the screen is awake. | Brings the shade down and states the `off` desire at once, while the unit plays nothing. A film goes on. |

Each program ignores the actions it does not answer, so a client that
reads its own ask come back changes nothing.
