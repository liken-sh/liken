## Turning a Player off and on

`spec.power` is `On` or `Off`, and a `Player` with no value is `On`.
When it changes, `media-operator` switches the `Player`'s equipment off
or on to match. The operator writes the field when a person presses a
power key on a remote and when a `Play` starts on a `Player` that is
off. You can write it with `kubectl`:

    kubectl patch player studio --type merge -p '{"spec":{"power":"Off"}}'
    kubectl patch player studio --type merge -p '{"spec":{"power":"On"}}'

The most recent write wins. The operator writes the field with
server-side apply under the field manager `media-operator-power`, which
owns `spec.power` and nothing else. So a GitOps copy of the `Player`
that leaves the field out does not reset it, and a copy that sets the
field overwrites it.

When `spec.power` changes to `Off`, the operator:

* writes the action `off` into the `Receiver`'s
  `status.session.powerAsk`, and `equipment-operator` puts the receiver
  and the TV behind it in standby.
* applies the override `{power: Off}` to the screen's `Display`,
  whatever the idle screen's `offMode` is.
* publishes `sleep` on the [`power`](#power) topic, and the idle
  screen fades to black.
* publishes `stop` on the `commands` topic of each unfinished `Play`
  on the `Player`. Each `Play` ends as it does when someone presses the
  remote's stop key.

While `spec.power` is `Off`, the `Receiver` session's `awake` flag
stays false and the override stays on the `Display`, whatever the idle
screen asks for on the [`panel`](#panel) topic.

When `spec.power` changes to `On`, the operator removes the override
and publishes `wake` on the `power` topic. The idle screen wakes and
publishes `{"desire": "on"}` on the `panel` topic. The session's `awake` flag becomes true, and
`equipment-operator` turns on the TV and the receiver and selects the
`Player`'s input. A `Player` with no `Receiver`, such as a single
monitor, does the same without the receiver.

When the `Player` goes on, the `off` desire from when it went off is
still retained on the `panel` topic until the screen publishes a new
one. So the operator records the time it turned the `Player` on in
`status.poweredOnAt`, and ignores an `off` desire that arrived at that
time or earlier. The panel stays lit, and the `awake` flag does not
change until the screen publishes its next desire.

Power keys on a remote change `spec.power` of the `Player` that the
remote's [focus mark](/docs/reference/remotes/#focus-and-focuscycle)
names. The operator acts on a press and ignores a repeat:

| Key | What it does |
|---|---|
| `KEY_POWER`, `KEY_POWER2` | Turns the `Player` off when it is on, and on when it is off. |
| `KEY_SLEEP` | Turns the `Player` off. Does nothing when it is already off. |
| `KEY_WAKEUP` | Turns the `Player` on. Does nothing when it is already on. |
| any other key | Turns the `Player` on when it is off. Does nothing else. |

When a `Play` starts on a `Player` that is off, the operator writes
`spec.power: On` with the same apply that a key press uses, before it
updates the `Receiver` session and creates the playback pod. The next
pass turns the equipment on as described above. If the API server
rejects the apply, the `Play` still starts, but the equipment stays off
until `spec.power` becomes `On`.

The kernel's `rc-cec` keymap gives the names `KEY_SLEEP` and
`KEY_WAKEUP` to the Power Off Function and the Power On Function of a
TV remote. HDMI-CEC 1.3a, section 13.13.3, defines each one as setting
the device to a fixed state, so pressing it twice does the same as
pressing it once. To make a controller's power button turn the
`Player` off and never on, map the button to `KEY_SLEEP` with a
`Keymap` row.

The idle timers turn less off than `spec.power: Off` does. After
`fadeAfterSeconds` without a press, the idle screen fades to black.
After `offAfterSeconds`, the operator applies the `offMode` override to
the panel and the session's `awake` flag becomes false.
`equipment-operator` then releases the TV's input and leaves the TV on.
`spec.power` stays `On`, and the next key press lights the screen
again.

`status.power` is the value that the operator last applied. The
operator changes the equipment whenever `spec.power` differs from it,
so a change made while the operator was down takes effect when the
operator starts. When `status.power` is empty, the operator copies
`spec.power` into it and changes no equipment.

The `PowerApplied` condition reports the result of the last change, and
its message lists the equipment that the operator wrote to. When a
write fails, the condition is `False` with the reason
`ReceiverAskFailed` or `DisplayWriteFailed`, and the operator repeats
the writes on each pass until all of them succeed.

## Events

The operator and `media-api` post Kubernetes `Event`s on the `Player`,
and `kubectl describe player` prints them. The API server deletes an
`Event` one hour after its last write. After that, read the conditions
and the logs.

| Reason | Type | When |
|---|---|---|
| `Present`, `PanelAway`, or another reason from the `Display` | Normal | The `Screen` condition changed. The message comes from the `Display`. |
| `NoDisplay`, `NotReported` | Warning | The `Screen` condition became `Unknown`, because the `Display` for the screen does not exist (`NoDisplay`) or has no `Connected` condition (`NotReported`). |
| `PodRecreated` | Warning | The kubelet ended the idle pod that `media-operator` runs, for example for an eviction, so the operator deleted the pod and created it again. The message gives the phase, the reason, and the message that the kubelet wrote on the pod. When the kubelet ends each new pod soon after it starts, the operator waits before each next pod, 10 seconds at first and twice as long each time, to a limit of 5 minutes, and posts no further `Event` until a pod has run for longer than 5 minutes before the kubelet ended it. An edit to the `Player` that changes the idle pod replaces an ended pod at once, with no wait. When `spec.idle.controller` hands the idle screen to another operator, that operator replaces its own pod and posts its own `Event`s, such as the [media browser](https://liken.sh/library/docs/guides/browser/) of `library-operator`. |
| `Superseded`, `Retired` | Normal | The operator deleted a `Play` of this `Player`. The message names the `Play`. The [Plays](/docs/reference/plays/) page explains each reason. |
| `ReceiverWriteFailed` | Warning | Writing the session to the `Player`'s `Receiver` failed. The operator tries again on each pass and posts no further `Event` until a write succeeds. |
| `ReceiverWriteRecovered` | Normal | A session write succeeded after one or more failures. |
| `Applied` | Normal | `spec.power` changed and every write that the change needed succeeded. The message lists the equipment that the operator wrote to. |
| `ReceiverAskFailed`, `DisplayWriteFailed` | Warning | A write for a change of `spec.power` failed. The operator tries again on each pass. |
| `Captured` | Normal | `media-api` returned captured data from the `Player`. The message names the caller, what was captured, and the media type. When the same caller makes the same capture again within ten minutes, the count of the existing `Event` increases and no new `Event` appears. |

## On the bus

The `players` topics describe a `Player` whether or not a `Play` is
running on it. [The media bus](/docs/reference/bus/) gives the rules
that every topic follows and lists each topic's writers and readers.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the `Player`'s name, activity, and parts |
| `players/{namespace}/{name}/volume` | the operator | yes | the volume level |
| `players/{namespace}/{name}/volume/commands` | any program that is not a remote | no | a request to change the volume or the mute |
| `players/{namespace}/{name}/panel` | the idle screen | yes | whether the idle screen wants the panel on or off |
| `players/{namespace}/{name}/commands` | the playback pod | no | a command for the idle screen |
| `players/{namespace}/{name}/power` | the operator | no | a request to wake or sleep the idle screen |

### `status`

This topic carries what a screen shows about the `Player`: its name,
what it is doing, the `Play` that it runs, and its parts, with the link
state and battery level of each. The operator is the only writer. It
publishes only when the payload changes, and it clears the topic with
an empty payload when the `Player` is deleted. Because the message is
retained, a screen that has just started gets the current state from
the broker.

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

`activity` has the same values as `status.activity` of the `Player`.
`play` is present while a `Play` is starting or playing. Its `name` is
the name of the `Play` object in Kubernetes, and `title` is the line of
text that a screen draws. A component's `kind` is `display`, `sink`, or
`remote`. Only a remote has `connected`, because a wired screen has no
link that can drop. `battery` is the charge that the controller's
`Peripheral` reports, from 0 to 100, and the key is absent when the
device reports none. `focused` appears only on a remote whose
[focus mark](/docs/reference/remotes/#focus-and-focuscycle) names this
`Player`, and the idle screen draws a small hexagon beside that
controller in its list of parts.

`play.displayAlive` is the `Play`'s
[`DisplayAlive` condition](/docs/reference/plays/#statusconditions),
with its `status`, `reason`, and `message`, and it is absent while the
`Play` has no such condition. So a client learns that the display
crashed without a request to the API server, and can show the
`message` while `status` is `False`. The client needs to do nothing
else: when the display restarts more than twice in one `Play`, the
operator ends the `Play` and `activity` returns to `Idle`.

### `volume`

This topic carries the volume level, the mute state, and who draws the
volume indicator:

    {"level": 0.63, "muted": false}
    {"level": 0.63, "muted": false, "indicator": "receiver"}

`level` is a fraction of the device's `spec.volume.max`, from 0.0 to
1.0, so a screen draws the same scale for every `Player`. `level` and
`muted` are always present.

`indicator` is `"receiver"` when the device that sets the level is a
`Receiver` whose `spec.volume.indicator` is `Receiver`. That receiver
draws its own volume overlay on the TV, so the screens record the level
and the mute state and draw no bar. When `indicator` is absent, which
includes every time the sinks set the level, the screens draw the
bar.

The operator is the only writer. It sets the level on the device that
controls the volume, which is the
[`Receiver`](https://liken.sh/equipment/docs/reference/receivers/) that
the `Player`'s screen is connected to, while the `Receiver`'s
`Reachable` condition is `True`. Otherwise it is every
[`Sink`](https://liken.sh/audio/docs/reference/sinks/) in the `Player`'s
`status.sinks`. A press of `KEY_VOLUMEUP` or `KEY_VOLUMEDOWN` on a
remote whose focus mark names this `Player` moves the target level by
the device's `spec.volume.step`. A key that is held moves it once for
each repeat. `KEY_MUTE` toggles the mute, and `KEY_UNMUTE` clears it.
The target never goes above the device's `spec.volume.max`. The operator
writes the target into the device's `status.session.volumeAsk` at most
once every 100 ms, and the device's operator applies it.

The operator publishes on this topic in these cases:

* A key press moves the target. It publishes the target at once, so
  the indicator moves before the device reports the new level.
* The device reports a level while no target is pending, for example
  when someone turns the receiver's own knob.
* The device has not reported the target within one second, for example
  when the receiver's own limit is below the target. The operator
  publishes the level that the device reports.
* The operator has just connected to the broker, after the operator
  starts or the broker restarts. It waits two seconds for the broker's
  retained messages, then publishes the level of each `Player` that
  the broker did not send.
* Only `indicator` changed. The operator publishes the level again, so
  the retained message has the current `indicator`.

When the sinks set the level and a `Player` has several, each sink
steps in its own units, and the topic carries the level of the first
sink in `spec.sinks`. The operator never changes `mpv`'s own volume.

A client reads `status` for the name, the activity, and the parts, and
`volume` for the level. The retained message that the broker sends when
the client subscribes sets the level and draws no indicator. A live
message draws the indicator, with two exceptions: a message whose
`indicator` is `"receiver"`, and a message that differs from the
previous one only in `indicator`. Both set the level and draw nothing.
A client publishes no level on this topic and handles no volume keys.
The operator reads the volume keys from the remote's own `events` topic.

### `volume/commands`

A program that is not a remote changes the volume by publishing here.
The messages are not retained, because each one is an event. The
operator handles each message as it handles a key press:

| Message | What the operator does |
|---|---|
| `{"step": "up"}` | Raises the level one step, as `KEY_VOLUMEUP` does. |
| `{"step": "down"}` | Lowers the level one step, as `KEY_VOLUMEDOWN` does. |
| `{"mute": "toggle"}` | Toggles the mute, as `KEY_MUTE` does. |
| `{"mute": true}` | Mutes the `Player`. |
| `{"mute": false}` | Clears the mute, as `KEY_UNMUTE` does. |

A message with no `step` and no `mute` changes nothing, and the
operator logs that it ignored the message.

### `panel`

The idle screen publishes whether it wants the panel on or off:

    {"desire": "off"}

`desire` is `on` or `off`. The idle screen has no API credentials, so
the operator reads this topic and sets or removes `spec.override` on
the screen's `Display`.

When the idle screen starts, it reads the retained desire before it
publishes one, so a screen that restarts while the panel is off leaves
it off. After that, it publishes a desire only when a key press, a
starting `Play`, or the off timer changes what the panel should do.
When the screen reconnects to the broker, it publishes its current
desire again, so a broker that restarted has it again.

When no desire has been published, the operator writes no override, and
the `Receiver` session keeps its current `awake` flag. The panel's
actual state comes from the `Display`, which the operator copies into
`status.panel`. When the `Player`'s `spec.idle.controller` becomes
`media.liken.sh/none`, the operator clears the topic with an empty
payload, so a restarted operator does not act on the last desire of a
client that no longer runs.

### `commands`

This topic carries commands for the `Player`'s idle screen. The
messages are not retained
([why](/docs/reference/bus/#retained-state-and-events)). A controller
never publishes here directly, and the idle screen only reads.

The playback pod's command sidecar publishes `play-next` when a person
accepts the up-next offer on the scrubber. Its `request` is the `Play`'s
`spec.next.request`, byte for byte. The client that created the `Play`
reads the message and creates the next `Play`.

The sidecar also publishes `home` when a person presses the home key
during a film, just before the `Play` ends. That message names the
`Remote` whose press caused it. Any program can also publish `home`
here, or on the `Play`'s `commands` topic during a film, and that
message names no `Remote`. The client treats either message as a press
of the home key.

A power key during a film reaches the operator and not the sidecar. The
operator stops the `Play` when the `Player` goes off
([Turning a Player off and on](#turning-a-player-off-and-on)).

| Message | Writer | What it means |
|---|---|---|
| `{"action": "play-next", "request": {...}}` | the playback pod | A person accepted the up-next offer. `request` is the `Play`'s `spec.next.request`. |
| `{"action": "home", "remote": "<namespace>/<name>"}` | the playback pod | A person pressed home on that `Remote` during a film. The `Play` ends after this message, and the client handles it as a press of the home key. The operator already wrote the input request to the `Player`'s `Receiver` when it read the press, so it does nothing more. |
| `{"action": "home"}` | the playback pod, or any program | A program asked to go home. The client handles it as a press of the home key. When the `Player` has a `Receiver`, the operator writes an input request into the `Receiver`'s session: the receiver switches to the `Player`'s input, and the TV does too when it is on. |

### `power`

The operator publishes requests here for the `Player`'s idle screen,
and none is retained. The operator writes the topic's name into
`status.idle.bus.powerTopic` of every `Player`. The idle screen is the
only reader, and it handles no power keys. The operator reads `KEY_POWER`, `KEY_POWER2`,
`KEY_SLEEP`, and `KEY_WAKEUP` from the remote's own `events` topic and
changes `spec.power`
([Turning a Player off and on](#turning-a-player-off-and-on)).

The operator publishes `sleep` when `spec.power` changes to `Off` and
`wake` when it changes to `On`. It also relays the TV's own requests
when the TV sends them over HDMI-CEC. The CEC workload of
`equipment-operator` writes each request into the `Television`'s
`status.screenAsk`, and the operator publishes each new one once. It
publishes nothing for a request that was already in `status.screenAsk`
when the operator started, or for a `Player` whose screen is not
connected through a `Receiver`.

| Message | When | What the client does |
|---|---|---|
| `{"action": "wake"}` | `spec.power` changed to `On`, or a person chose this `Player`'s input in the TV's source menu while the screen was asleep. | Wakes the screen and publishes the `on` desire, as it does when a key is pressed on a sleeping screen. |
| `{"action": "sleep"}` | `spec.power` changed to `Off`, or the TV went to standby while the screen was awake. | Fades the screen to black and publishes the `off` desire at once, while the `Player` plays nothing. While a `Play` is running, it does nothing and the film continues. |

If a client ignores a `sleep` during a film, no equipment stays on. The
operator stops the `Play` for the same change, and the panel and the
equipment follow `spec.power` whatever the client publishes. A client
ignores every other action.
