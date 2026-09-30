## On the bus

The `plays` tree contains one run's commands, report, and availability.
[The media bus](/docs/reference/bus/) gives the rules
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
| `play` | plays, and leaves a film that plays playing |
| `hold` | pauses, and leaves a paused film paused |
| `stop` | ends the run, the way the display's exit ends it |
| `seek` | moves the playhead by `amount` seconds |
| `chapter` | jumps by `amount` chapters |
| `volume` | steps the unit's level by `amount` |
| `mute` | toggles the unit's muted flag |
| `unmute` | clears the unit's muted flag |
| `subtitles` | cycles the subtitle track |
| `audio` | cycles the audio track |
| `info` | shows the file name and position for a few seconds |
| `up`, `down`, `left`, `right`, `select`, `back` | drive the on-screen display |
| `home` | asks the unit's client for its home page, then ends the run |
| `power` | asks the unit's client to do what power does between films, then ends the run |
| `power-off` | asks the unit's client to turn the room off, then ends the run |

`play`, `hold`, `stop`, `unmute`, and `power-off` set the state they
name, so a second one changes nothing. The playback pod binds them to
the names the kernel's `rc-cec` keymap gives a TV remote's
deterministic functions (HDMI-CEC 1.3a, CEC 13.13.3): `KEY_PLAYCD`,
`KEY_PAUSECD`, `KEY_STOPCD`, `KEY_UNMUTE`, and `KEY_SLEEP`. The keymap
also gives the Pause-Play Function and the Mute Function the names
`KEY_PLAYPAUSE` and `KEY_MUTE`, which a Bluetooth remote's play and
mute buttons send too, so those two stay toggles. A `Keymap` row on
the `Remote` that holds the CEC adapter's input device can name the
Pause-Play Function `KEY_PAUSECD`. No key name mutes without a
toggle, so the Mute Function stays one.

A `volume`, `mute`, or `unmute` command changes no player directly: the pod
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
      "subtitleLanguage": "eng",
      "pod": "5f0c7a52-8e1d-4c3b-9a27-2d6b1e4f8c90"
    }

`item` counts from 1 in spec order. `duration` is empty until the
player has read the item's header, and the two language fields are
absent while no track of that kind plays. The language values are
the track's own tags as the file carries them, for Matroska the
three-letter ISO 639-2 codes, whatever form the preference used. The
`ended` field appears when the run is over and remains set in every later report of
the same run. The pod takes seconds to terminate, so the operator
reads this mark and returns the unit to idle at once instead of
waiting out the pod.

`pod` is the UID of the playback pod that sent the report. A run's pod
can stop while the `Play` goes on: the operator recreates it after an
edit to the `Player`, resumes the run after the player crashes, or
replaces a pod that something else deleted, such as an eviction. The
new pod has the same name, and the old pod reports the `ended` field
when it stops. The operator refuses a report from a pod that its pod
watch shows deleting, from a pod other than the one the watch shows
standing, and from a pod it knows is gone or replaced. So the old pod's
ending does not move the unit to `Idle`, and the new pod's ending is
marked and labeled on its own. While the watch shows no pod for the
run, the operator takes a report from any pod it does not know is gone,
because the new pod can report before the watch shows it.

The operator takes a report with no `pod` field as the run's, so a pod
that runs an older sidecar image still reports. Two such pods of one
run look the same to the operator, so the limits above do not apply to
them.

The operator folds each report into the `Play`'s Kubernetes status,
so a program that only needs the current position can read either
one.

Two writers clear the topic. The pod clears it with an empty retained
payload when its run ends cleanly. The operator clears it as well, which
is what a pod that died uncleanly needs, and it does so on its
finalizer: it adds `media.liken.sh/bus-topics` to every `Play`, and
when the `Play` is deleted it deletes the pod, waits for the pod to be
gone, publishes an empty retained payload on `status` and on
`availability`, and only then takes its finalizer off. So the `Play` is
never gone while its topics remain, and a deleted `Play` leaves no
report on the broker.

### `availability`

`online` or `offline`, a space, and the pod's UID, retained, the
[availability](/docs/reference/bus/#availability) signal for the
report above. For example, `offline 5f0c7a52-8e1d-4c3b-9a27-2d6b1e4f8c90`.
The pod names this topic as its MQTT Last Will with `offline` and its
UID as the payload, publishes `online` and its UID once it connects,
and publishes `offline` and its UID itself when its run ends cleanly.

The broker publishes a dead pod's Last Will only when the pod's
keepalive runs out, which can be after the run's new pod is online.
The operator takes an availability on the same terms as a report, so
a late `offline` from an old pod does not drop the new pod's report.
It takes the word with no UID as the run's. An operator that has just
started knows no pod as gone until its pod watch has read the cluster,
so the retained `offline` of an old pod can drop the report it read.
The new pod reports again within a second.

The operator clears this topic with an empty retained payload on the
same terms as `status`: on its finalizer, once the `Play` is deleted and
its pod is gone.
