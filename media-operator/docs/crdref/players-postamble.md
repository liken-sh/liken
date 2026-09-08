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
| `players/{namespace}/{name}/commands` | the operator | no | a command for the idle pod |

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
      "play": {"name": "evening-film", "title": "An Evening Film"},
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

### `volume`

The unit's listening level and its muted flag:

    {"level": 40, "muted": false}

Both fields are always written, so a reader never needs a default
for a missing key. The level runs 0 to 100, and 100 is unity, the
player's own default and the cap. A published level outside 0 to 100
is clamped to the range.

Three writers publish here. The operator writes unity when it seeds
a unit the broker holds no level for, and it writes a `Play`'s
`spec.volume` over the unit's current state before it creates the
pod. The pod that handles a `volume` or `mute` press writes the next
state back. That pod is the playback pod's command sidecar during a
film and the idle screen client between films, and each computes the
next state from the last message the topic delivered. The
[equipment operator](https://equipment.liken.sh/docs/reference/receivers/)'s
receiver session writes the receiver's true level here whenever its
mark on `volume/owner` stands: the position it adopts when the
session starts, and the position the receiver reports after a press
or a turn of its own knob.

Every pod for the unit subscribes and applies what it reads, so the
unit plays at the one level the topic holds. While the owner mark
stands, no pod applies the level: each holds `mpv` at unity, volume
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
`mpv` at unity while the mark stands and applies the level the topic
last delivered when the mark clears. It re-applies the held state
once `mpv`'s socket opens, because the broker delivers the retained
mark and level within milliseconds of the subscribe and `mpv` opens
its socket seconds later. The operator builds a playback pod with no
`--volume` flag while the mark stands, so `mpv` starts at its own
default, unity. A delegate's idle screen client reads the topic from
`status.idle.bus.volumeOwnerTopic` and draws no level of its own
while the mark stands.

### `panel`

The desire the idle screen client states for the unit's screen:

    {"desire": "off"}

The two desires are `on` and `off`. The client holds no API
credentials and writes no hardware, so the operator reads this topic
and applies or lifts `spec.override` on the screen's `Display`. What
the panel actually shows comes back the other way, from the
`Display`'s observed state into `status.panel`. The operator clears
the topic with an empty payload when the unit's controller becomes
`media.liken.sh/none`, so a restarted operator does not read the last
desire of a client that no longer runs.

### `commands`

The one display command around the `Player`'s idle screen. It is not
retained ([why](/docs/reference/bus/#retained-state-and-events)), and
a controller never sends it directly. The operator is the only
writer, and a client publishes nothing here.

| Message | Writer | What it says |
|---|---|---|
| `{"action": "re-present"}` | the operator | A `Play` ended. The idle screen client maps a fresh surface. |
