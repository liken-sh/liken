## On the bus

The `plays` tree carries one run's commands, its report, and its
availability. [The media bus](/docs/reference/bus/) gives the rules
every topic follows and lists every writer and reader of each.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | no | one named command |
| `plays/{namespace}/{name}/status` | the playback pod | yes | the run's report |
| `plays/{namespace}/{name}/availability` | the playback pod | yes | `online` or `offline` |

### `commands`

The topic any program publishes to drive the run. A phone and a
Home Assistant integration reach the run the same way: publish one
JSON command, and the playback pod applies it. A controller does not
reach this topic. The playback pod's command sidecar reads the
`Remote`'s events topic itself and binds the key names there, so a
press becomes one of these commands inside the pod.

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
program has no effect rather than a crash. A focus cycle never
travels here: the key that asks for one, `KEY_CYCLEWINDOWS`, becomes
a request on the [Remote's tree](/docs/reference/remotes/) instead.

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

Two writers clear the topic. The pod clears it with an empty retained
payload when its run ends cleanly. The operator clears it as well, which
is what a pod that died uncleanly needs, and it does so on its
finalizer: it holds `media.liken.sh/bus-topics` on every `Play`, and
when the `Play` is deleted it deletes the pod, waits for the pod to be
gone, publishes an empty retained payload on `status` and on
`availability`, and only then takes its finalizer off. So the `Play` is
never gone while its topics still stand, and a deleted `Play` leaves no
report on the broker.

### `availability`

`online` or `offline`, retained, the
[availability](/docs/reference/bus/#availability) signal for the
report above. The pod names this topic as its MQTT Last Will with
`offline` as the payload, publishes `online` once it connects, and
publishes `offline` itself when its run ends cleanly.

The operator clears this topic with an empty retained payload on the
same terms as `status`: on its finalizer, once the `Play` is deleted and
its pod is gone.
