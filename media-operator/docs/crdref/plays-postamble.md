## Test patterns

A test pattern gives the display a known picture under it, so a check
of the OSD, the scrims, or the color does not depend on a scene in a
film. The player image carries each pattern as a ten-minute file with a
chapter each minute, so the scrubber, the chapter marks, and the time
left behave as they do for a film. A pattern needs no storage and no
network.

    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: white
    spec:
      players: [theater]
      items:
        - uri: pattern://white

| Pattern | What it shows |
|---|---|
| `white` | a white screen, the worst case for the dark scrims behind the OSD |
| `bars` | the SMPTE HD color bars, which show a color or a range error |
| `speakers` | a walk of a 7.1 layout: pink noise from one speaker at a time, named on the screen |

`speakers` plays pink noise for 5 seconds from each speaker in turn:
front left, center, front right, side right, back right, back left, side
left, and then the subwoofer, whose noise stops at 120 Hz. The screen
names the speaker that should play, and each speaker is a chapter, so a
chapter skip moves to the next one. The file is 7.1, so a system with
fewer speakers plays a downmix, and the walk shows where each missing
speaker's channel lands. The walk is 40 seconds long and carries the
one frame `1280x720`.

The URI can name a frame after the pattern, as in
`pattern://bars/1920x1080`. `white` and `bars` carry every frame below:

| Frame | Shape | What it matches |
|---|---|---|
| `1280x720` | 16:9 | a 720p screen |
| `1920x1080` | 16:9 | a 1080p screen |
| `3840x2160` | 16:9 | a 4K screen |
| `2560x1080` | 21:9 | a wide 1080-row screen |
| `3840x1600` | 2.4:1 | a wide 1600-row screen |
| `1920x804` | 2.39:1 | a scope film, which letterboxes on a 16:9 screen |

A URI with no frame takes the frame of the `Player`'s screen: the
largest frame of the screen's shape that fits on the compositor's
canvas, which the screen's `Display` reports. A screen whose shape
matches no frame, or a `Player` whose screen is not known yet, plays
`1920x1080`, and mpv scales it. The operator chooses the frame when it
creates the playback pod, so a `Display` that changes mode later does
not change the run.

## Events

The operator posts a Kubernetes `Event` on the `Play` for each phase
it moves to, for each playback pod it creates again, and for the delete
that ends the `Play`. `kubectl describe play` prints them. The API
server deletes an `Event` one hour after its last write, so the phase,
the conditions, and the operator's log hold the facts after that.

| Reason | Type | When |
|---|---|---|
| `PlaybackStarted` | Normal | The phase moved to `Running`. |
| `PlaybackFinished` | Normal | The phase moved to `Finished`. The message gives the item and the position. |
| `PlaybackFailed` | Warning | The phase moved to `Failed` because the playback pod failed. The message is the pod's own. |
| `InvalidSpec` | Warning | The phase moved to `Failed` because the `Play` can never run as written: it names no `Player`, an item does not resolve, or a `Remote` the pod needs does not exist. |
| `PodRecreated` | Warning or Normal | The operator created the playback pod again. A pod that failed or is gone is a Warning, and a spec edit that reshaped the pod is Normal. The message gives the position the new pod starts at. |
| `ResumeBackoff` | Warning | The playback pod failed again soon after a recreate. The message gives the count and the wait before the next recreate. |
| `Restarting`, `Running` | Warning, Normal | The `DisplayAlive` condition changed, with the condition's message. |
| `Superseded` | Normal | The operator deleted the `Play`, because a newer `Play` on the same `Player` replaces it. |
| `Retired` | Normal | The operator deleted the `Play`, because its `ttlSecondsAfterFinished` passed after it finished. |

`Superseded` and `Retired` also go on the `Player`, because the
`Play`'s own `Event`s leave `kubectl describe` with it.

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
only to the two actions that move by one, and its sign is the
direction: seconds for `seek`, and chapters for `chapter`.

| Action | What it does |
|---|---|
| `pause` | toggles pause |
| `play` | plays, and leaves a film that plays playing |
| `hold` | pauses, and leaves a paused film paused |
| `stop` | ends the run, the way the display's exit ends it |
| `seek` | moves the playhead by `amount` seconds |
| `chapter` | jumps by `amount` chapters |
| `subtitles` | cycles the subtitle track |
| `audio` | cycles the audio track |
| `info` | shows the file name and position for a few seconds |
| `up`, `down`, `left`, `right`, `select`, `back` | drive the on-screen display |
| `home` | asks the unit's client for its home page, then ends the run |
| `power` | asks the unit's client to do what power does between films, then ends the run |
| `power-off` | asks the unit's client to turn the room off, then ends the run |

Two parts of the on-screen display answer these actions. When an item
names an `appearances` file and the on-screen display is up, playing
or paused, the display draws a row of cards above the skip control and
the chip, one for each credited person in the scene, and `up`, `left`,
and `right` move the focus along it. When an item's `marks` place the
playhead inside an intro or a recap, or inside the credits before a
post-credits scene, the display offers a skip control, and `select`
takes it. A skip to a post-credits scene takes `select` only while the
on-screen display is up. The display never skips on its own.

`play`, `hold`, `stop`, and `power-off` set the state they name, so a
second one changes nothing. The playback pod binds them to the names
the kernel's `rc-cec` keymap gives a TV remote's deterministic
functions (HDMI-CEC 1.3a, CEC 13.13.3): `KEY_PLAYCD`, `KEY_PAUSECD`,
`KEY_STOPCD`, and `KEY_SLEEP`. The keymap also gives the Pause-Play
Function the name `KEY_PLAYPAUSE`, which a Bluetooth remote's play
button sends too, so it stays a toggle. A `Keymap` row on the
`Remote` that holds the CEC adapter's input device can name the
Pause-Play Function `KEY_PAUSECD`.

The level belongs to the unit and not to the run, so this topic
carries no volume action. The operator reads the volume keys from the
`Remote`'s events topic, and a program that is not a remote asks on
the [Player's `volume/commands` topic](/docs/reference/players/#volumecommands).
An action this build has no case for does nothing, so a command from a
newer program has no effect rather than a crash. A focus cycle never
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
