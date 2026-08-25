## On the bus

The `plays` tree carries one run's commands, its report, and its
availability. See [the media bus](/docs/reference/bus/) for the
rules every topic follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | no | one named command |
| `plays/{namespace}/{name}/status` | the playback pod | yes | the run's report |
| `plays/{namespace}/{name}/availability` | the playback pod | yes | `online` or `offline` |

### `commands`

The topic any program publishes to drive the run. A
translator sidecar, a phone, and a Home Assistant integration all
reach the run the same way: publish one JSON command, and the
playback pod applies it.

    {"action": "seek", "amount": -30}

`action` names a word from the vocabulary below. `amount` belongs
only to the three actions that move by one, and its sign is the
direction: seconds for `seek`, a step for `volume` and `chapter`.

| Action | What it does |
|---|---|
| `pause` | toggles pause |
| `seek` | moves the playhead by `amount` seconds |
| `chapter` | jumps by `amount` chapters |
| `volume` | steps the unit's level by `amount` |
| `mute` | toggles the unit's muted flag |
| `subtitles` | cycles the subtitle track |
| `audio` | cycles the audio track |
| `info` | shows the file name and position for a few seconds |
| `up`, `down`, `left`, `right`, `select`, `back` | drive the on-screen display |

A `volume` or `mute` command changes no player directly: the pod
computes the unit's next state and publishes it on the
[Player's volume topic](/docs/reference/players/#volume), and every
pod for the unit applies what that topic delivers. An action this
build has no case for does nothing, so a command from a newer
program has no effect rather than a crash. `cycle-focus`, the one
[Keymap](/docs/reference/keymaps/) action that never travels here,
becomes a focus cycle request on the
[Remote's tree](/docs/reference/remotes/) instead.

### `status`

The run's report, as the playback pod reads it from the player. The
pod publishes it on every change, and every few seconds while the
position advances. It is retained, so a restarted operator reads a
running `Play`'s place back from the broker.

    {
      "paused": false,
      "item": 1,
      "position": "0:41:22",
      "duration": "1:58:03",
      "audioLanguage": "eng",
      "subtitleLanguage": "eng"
    }

`item` counts from 1 in spec order. `duration` is empty until the
player has read the item's header, and the two language fields are
absent while no track of that kind plays. The language values are
the track's own tags as the file carries them, for Matroska the
three-letter ISO 639-2 codes, whatever form the preference used. One more field, `ended`,
appears when the run is over and stays set in every later report of
the same run. The pod takes seconds to terminate, so the operator
reads this mark and returns the unit to idle at once instead of
waiting out the pod.

The operator folds each report into the `Play`'s Kubernetes status,
so a program that only needs the current position can read either
one.

### `availability`

`online` or `offline`, retained. The pod names this topic as its
MQTT Last Will with `offline` as the payload, and publishes `online`
once it connects, so a retained status a killed pod left behind does
not read as a live run.
