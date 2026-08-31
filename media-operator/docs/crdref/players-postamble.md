## On the bus

The `players` tree describes the equipment, with or without a
running `Play`. See [the media bus](/docs/reference/bus/) for the
rules every topic follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's name, activity, and parts |
| `players/{namespace}/{name}/volume` | the operator and the pods | yes | the listening level |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel desire |
| `players/{namespace}/{name}/screen` | the idle pod | no | what the idle screen shows next |
| `players/{namespace}/{name}/commands` | the operator | no | a command for the idle pod |

### `status`

What a screen would show about one unit: its name, what it is
doing, the `Play` it runs, and its parts with the presence of each.
The operator is the only writer, so an idle pod that just started
draws the live state the broker already holds, with no request to
the operator.

    {
      "displayName": "Studio Lab",
      "activity": "Playing",
      "play": {"name": "dune", "title": "Dune"},
      "components": [
        {"name": "Portable Screen", "kind": "display"},
        {"name": "Built-in Speakers", "kind": "sink"},
        {"name": "Studio Controller", "kind": "remote", "connected": true, "focused": true}
      ]
    }

`activity` is the same word the Kubernetes status carries. `play` is
present while a run starts or plays: `name` is the object a person
finds with `kubectl`, and `title` is the one line a screen draws. A
component's `kind` is `display`, `sink`, or `remote`, and only a
remote carries `connected`, because a wired screen reports no
presence. `focused` appears on the one remote whose
[focus mark](/docs/reference/remotes/) names this `Player`, and the
idle screen draws a small hexagon beside that controller in its
parts list. Every other component omits the key.

### `volume`

The unit's listening level and its muted flag:

    {"level": 40, "muted": false}

Both fields are always written, so a reader never needs a default
for a missing key. The level runs 0 to 100, and 100 is unity, the
player's own default and the cap. Every pod for the unit subscribes
and applies what it reads, so the unit plays at the one level the
topic holds. The operator writes it when it seeds a unit or
applies a `Play`'s starting volume, and the pod that handles a
`volume` or `mute` press writes the result back. A published level
outside 0 to 100 is clamped to the range.

### `panel`

The desire the idle sidecar states for the unit's screen:

    {"desire": "off"}

The two desires are `on` and `off`. The sidecar holds no API
credentials and writes no hardware, so the operator reads this topic
and applies or lifts `spec.override` on the screen's `Display`. What
the panel actually shows comes back the other way, from the
`Display`'s observed state into `status.panel`.

### `screen`

What the idle sidecar decided for the unit's screen, one moment per
message:

    {"event": "focus", "remote": 1}

The sidecar holds the quiet window, the keymaps, and the focus gate,
and these four moments are what it decides. The idle client reads
them and draws them, whichever client
[`spec.idle.image`](#specidle--image) named.

| `event` | What it says |
|---|---|
| `sleep` | The quiet window ran out, so the shade comes down. |
| `wake` | A press or a starting `Play` came, so the shade goes up. |
| `focus` | A live mark named this `Player`. `remote` is the controller's index in `spec.remotes` order. |
| `present` | A `Play` ended, and the screen is the idle client's again. |

Only `focus` carries `remote`. Nothing on this topic is retained: a
retained moment would replay a press to a client that restarted.

The unit's own state stays on the retained topics above. A client
reads `status` for the name, the activity, and the parts, and
`volume` for the level. The first `volume` message of a session is
the broker's retained catch-up, which sets the level and shows no
indicator, and every message after it is a press.

### `commands`

The operator's channel to the `Player`'s idle pod. It carries
`{"action": "re-present"}` when a `Play` ends, and the idle sidecar
states the `present` moment on [`screen`](#screen). A controller
never sends it, and it carries none of the media actions a
[Play's commands topic](/docs/reference/plays/#commands) accepts.
