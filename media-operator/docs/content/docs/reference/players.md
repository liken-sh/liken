---
title: Players
weight: 10
toc: true
---

<!-- Generated from deploy/players-crd.yaml by crdref. Do not edit. -->

A `Player` is one named unit of equipment: a lone speaker, a TV
with its built-in speakers, a TV with a receiver. The spec selects
the unit's devices out of what the hardware operators publish, with
the same CEL selectors a hand-written `ResourceClaim` would use.
Between runs, the operator keeps one claim on the unit's display for
the idle screen. It claims the other devices only while a
[Play](/docs/reference/plays/) runs on the unit.

The resource is namespaced, and everything a `Player` becomes is
created in its namespace: the claims, the playback pod, and the
`Play` that names it, so RBAC on the namespace covers the set.

    apiVersion: media.liken.sh/v1alpha1
    kind: Player
    metadata:
      name: studio
      namespace: media
    spec:
      zone: studio
      displayName: Studio Lab
      display:
        class: display
        displayName: Portable Screen
      render:
        class: gpu-render
      sinks:
        - class: audio-output
          displayName: Built-in Speakers
      remotes:
        - name: studio-gamepad
          displayName: Studio Controller
      idle:
        fadeAfterSeconds: 600

The class names here are the cluster's own vocabulary: consumer
`DeviceClass` objects are yours to create, and each hardware
operator's manual gives the YAML for its class.

A Player is one named unit of equipment. A Play names a Player to run media on it. The media operator retains the display claim between playback runs for the idle screen and claims the other devices only while a Play runs.

## spec

The devices that form the unit, each selected from what a hardware operator publishes. All of a Player's devices must be reachable from one machine, because a Play becomes one pod and the scheduler places that pod on the machine that owns every claimed device. A Player whose devices span machines produces Plays that stay Pending. The spec must select a display or at least one sink; render alone plays nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--zone"></span>`zone` | string | no | The area this Player is in, such as living-room. A word for grouping and display; nothing acts on it yet. |
| <span id="spec--displayname"></span>`displayName` | string | no | The human name of this unit, the one the idle screen and later ambient surfaces show in place of the object name, such as Studio Lab. Omit it, and the idle screen falls back to the object name. |
| <span id="spec--display"></span>`display` | [object](#specdisplay) | no | The display this Player shows video on. Omit it for an audio-only Player. |
| <span id="spec--sinks"></span>`sinks` | [\[\]object](#specsinks) | no | The audio outputs this Player plays sound through. |
| <span id="spec--render"></span>`render` | [object](#specrender) | no | The GPU render node the player program decodes and draws with. Omit it only for an audio-only Player; mpv needs a GPU to put video on a display. |
| <span id="spec--remotes"></span>`remotes` | [\[\]object](#specremotes) | no | The controllers this unit owns, each naming a Remote in the same namespace. Each Remote runs a standing pod of its own, and the Play's command sidecar reads the events every entry publishes. |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | A per-Player override of the audio language order; omit it to inherit the default MediaPreferences. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | A per-Player override of the subtitle language order; omit it to inherit the default MediaPreferences. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | A per-Player override of when subtitles show; omit it to inherit the default MediaPreferences. One of: `on`, `off`, `auto`. |
| <span id="spec--idle"></span>`idle` | [object](#specidle) | no | This unit's idle screen policy. Each field overrides the default MediaPreferences on its own. |

### spec.display

The display this Player shows video on. Omit it for an audio-only Player.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdisplay--class"></span>`class` | string | yes | The DeviceClass the claim allocates through. Consumer classes are the cluster owner's vocabulary; each hardware operator's manual gives the YAML for its class. |
| <span id="specdisplay--displayname"></span>`displayName` | string | no | The human name of this selection, the one the idle screen shows in its parts list, such as Portable Screen. Omit it, and the idle screen falls back to the DeviceClass name. |
| <span id="specdisplay--selector"></span>`selector` | string | no | A CEL expression over device.attributes, the same expression a hand-written claim would carry. Omitted, the class alone chooses, which fits a class that already names one kind of device. |
| <span id="specdisplay--parameters"></span>`parameters` | [object](#specdisplayparameters) | no | Opaque configuration for the driver that prepares the device, carried onto the claim unread. The display operator's manual documents its parameters, such as mode and brightness. |

#### spec.display.parameters

Opaque configuration for the driver that prepares the device, carried onto the claim unread. The display operator's manual documents its parameters, such as mode and brightness.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdisplayparameters--driver"></span>`driver` | string | yes | The driver the parameters are for, such as display.liken.sh. |
| <span id="specdisplayparameters--values"></span>`values` | object | no | The parameters themselves. The driver defines them, and this operator copies them to the claim. |

### spec.sinks[]

The audio outputs this Player plays sound through.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsinks--class"></span>`class` | string | yes | The DeviceClass the claim allocates through. Consumer classes are the cluster owner's vocabulary; each hardware operator's manual gives the YAML for its class. |
| <span id="specsinks--displayname"></span>`displayName` | string | no | The human name of this selection, the one the idle screen shows in its parts list, such as Built-in Speakers. Omit it, and the idle screen falls back to the DeviceClass name. |
| <span id="specsinks--selector"></span>`selector` | string | no | A CEL expression over device.attributes, the same expression a hand-written claim would carry. |
| <span id="specsinks--parameters"></span>`parameters` | [object](#specsinksparameters) | no | Opaque configuration for the driver that prepares the device, carried onto the claim unread. The audio operator's manual documents its parameters, such as codec. |

#### spec.sinks[].parameters

Opaque configuration for the driver that prepares the device, carried onto the claim unread. The audio operator's manual documents its parameters, such as codec.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsinksparameters--driver"></span>`driver` | string | yes | The driver the parameters are for, such as audio.liken.sh. |
| <span id="specsinksparameters--values"></span>`values` | object | no | The parameters themselves. The driver defines them; this operator carries them. |

### spec.render

The GPU render node the player program decodes and draws with. Omit it only for an audio-only Player; mpv needs a GPU to put video on a display.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specrender--class"></span>`class` | string | yes | The DeviceClass the claim allocates through. A render class usually needs no selector, because it already names one kind of device. |
| <span id="specrender--displayname"></span>`displayName` | string | no | The human name of this selection, the one the idle screen shows in its parts list. Omit it, and the idle screen falls back to the DeviceClass name. |
| <span id="specrender--selector"></span>`selector` | string | no | A CEL expression over device.attributes, for a machine with more than one GPU. |

### spec.remotes[]

The controllers this unit owns, each naming a Remote in the same namespace. Each Remote runs a standing pod of its own, and the Play's command sidecar reads the events every entry publishes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specremotes--name"></span>`name` | string | yes | The Remote this unit owns, by name, in this namespace. |
| <span id="specremotes--displayname"></span>`displayName` | string | no | The human name of this controller, the one the idle screen shows in its parts list, such as Studio Dualsense Controller. Omit it, and the idle screen falls back to name. |

### spec.idle

This unit's idle screen policy. Each field overrides the default MediaPreferences on its own.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--controller"></span>`controller` | string | no | The operator that draws this unit's idle screen, as a domain-qualified name. Two names belong to the media operator: media.liken.sh/idle-screen, which is the default and draws the idle screen this operator ships, and media.liken.sh/none, under which nothing draws an idle screen on this unit and no claim exists. Any other name hands the screen to the operator that handles it, which reads status.idle for the claim to reference, the requests it carries, the two windows, and the bus it joins; image has no effect under such a name, because that operator brings its own pod. Omit it to inherit the default MediaPreferences. Pattern: `^[a-z0-9.-]+/[a-z0-9-]+$`. |
| <span id="specidle--image"></span>`image` | string | no | The container image that draws this unit's idle screen. The image starts with its own entrypoint and reads the unit's state from the bus. It implements the fade and off windows, the focus gate, the shade, the volume indicator, and the panel desire in its own process. Omit it to inherit the default from MediaPreferences. Where no tier names an image, the screen runs the idle client the media operator ships. |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds of quiet before the idle screen fades to black. Zero disables the automatic fade; omit it to inherit the default MediaPreferences. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds of quiet before the panel itself goes dark, at least fadeAfterSeconds. Zero or unset means the panel never goes dark on its own. The panel goes dark only where the cluster runs a display-operator that publishes a Display for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | Which override the off window applies to the screen's Display. The default, backlight, holds the panel at brightness zero, which still answers DDC. Power off stops some panels from answering DDC at all; state it only for a panel that woke from it in a drill. One of: `backlight`, `power`. |

## status

What plays on this Player now, written only by the media operator. It is derived from the Plays that name the Player, so it is empty until one does.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--activity"></span>`activity` | string | no | Whether the Player performs a run now. Playing is a Play running on it, Starting is a Play whose pod has not begun, and Idle is no Play at all. One of: `Playing`, `Starting`, `Idle`. |
| <span id="status--play"></span>`play` | string | no | The name of the Play on this Player, in the same namespace. Empty while the Player is Idle. |
| <span id="status--panel"></span>`panel` | string | no | What the screen's Display last observed: On, BacklightOff, or Off. Empty until a Display reports an observation for the unit's screen. |
| <span id="status--receiver"></span>`receiver` | [object](#statusreceiver) | no | The equipment this unit's cable lands on, matched from the machine the unit draws on and the monitor id of its screen. Absent for a unit that plays straight into its panel. |
| <span id="status--screen"></span>`screen` | [object](#statusscreen) | no | The last screen the idle claim resolved to. The operator keeps it while the panel is away, so it can still read the screen's Display and say why the idle pod waits. Absent until the claim has resolved once. |
| <span id="status--sinks"></span>`sinks` | [\[\]object](#statussinks) | no | The Sink each spec.sinks selection resolved to, in spec order. The operator writes the list from an allocated playback claim and keeps it after the Play retires, the way it keeps status.screen. A tap through these names still needs a running Play. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The unit's conditions. There is one, Screen, and it is absent until the idle claim has resolved once. True with reason Present means the screen's Display reports a panel on its connector. False with reason PanelAway means the Display reports no panel there: the monitor shows another input, the idle claim has deallocated, and the idle pod parks Pending until the panel returns. That park is by design, and an alert on Pending pods can read this reason to stay quiet for it. False with any other reason carries the Display's own reason through. Unknown with reason NoDisplay means no Display carries the remembered monitor id, and Unknown with reason NotReported means the Display carries no Connected condition. The message is the Display's own, and lastTransitionTime moves only when the status does. |
| <span id="status--idle"></span>`idle` | [object](#statusidle) | no | What draws this unit's idle screen, and everything the operator that draws it needs. A delegate wires its client from this block alone, and it sets MEDIA_PLAYER_NAME on that client to the Player's metadata.name, the value every focus mark holds. The block is absent for a Player that drives no screen and where the cluster names no display-draw class. A delegate reads this block and never the spec, because the spec may inherit its controller from the default MediaPreferences, and only the media operator resolves the tiers. |

### status.receiver

The equipment this unit's cable lands on, matched from the machine the unit draws on and the monitor id of its screen. Absent for a unit that plays straight into its panel.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreceiver--name"></span>`name` | string | no | The name of the matched Receiver. |
| <span id="statusreceiver--input"></span>`input` | string | no | The input of that Receiver this unit's cable lands on. |
| <span id="statusreceiver--reachable"></span>`reachable` | string | no | The status of the Receiver's Reachable condition, empty until it carries one. |

### status.screen

The last screen the idle claim resolved to. The operator keeps it while the panel is away, so it can still read the screen's Display and say why the idle pod waits. Absent until the claim has resolved once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusscreen--node"></span>`node` | string | no | The machine that publishes the screen's draw device. |
| <span id="statusscreen--monitor"></span>`monitor` | string | no | The monitor id of the screen, which is the name of its Display. |

### status.sinks[]

The Sink each spec.sinks selection resolved to, in spec order. The operator writes the list from an allocated playback claim and keeps it after the Play retires, the way it keeps status.screen. A tap through these names still needs a running Play.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussinks--request"></span>`request` | string | no | The claim's request name, audio0 for the first sink the spec states. |
| <span id="statussinks--name"></span>`name` | string | no | The name of the Sink object, which is the audio operator's own device name for that endpoint. |

### status.conditions[]

The unit's conditions. There is one, Screen, and it is absent until the idle claim has resolved once. True with reason Present means the screen's Display reports a panel on its connector. False with reason PanelAway means the Display reports no panel there: the monitor shows another input, the idle claim has deallocated, and the idle pod parks Pending until the panel returns. That park is by design, and an alert on Pending pods can read this reason to stay quiet for it. False with any other reason carries the Display's own reason through. Unknown with reason NoDisplay means no Display carries the remembered monitor id, and Unknown with reason NotReported means the Display carries no Connected condition. The message is the Display's own, and lastTransitionTime moves only when the status does.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, Screen. |
| <span id="statusconditions--status"></span>`status` | string | yes | The condition's status. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One word for the status: Present, PanelAway, NoDisplay, NotReported, or the Display's own reason. |
| <span id="statusconditions--message"></span>`message` | string | no | The Display's message for the condition, which names the connector. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed, so a reader can tell how long the unit has waited. |

### status.idle

What draws this unit's idle screen, and everything the operator that draws it needs. A delegate wires its client from this block alone, and it sets MEDIA_PLAYER_NAME on that client to the Player's metadata.name, the value every focus mark holds. The block is absent for a Player that drives no screen and where the cluster names no display-draw class. A delegate reads this block and never the spec, because the spec may inherit its controller from the default MediaPreferences, and only the media operator resolves the tiers.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidle--controller"></span>`controller` | string | no | The resolved controller name, after spec.idle.controller, the default MediaPreferences, and the built-in media.liken.sh/idle-screen resolve in that order. |
| <span id="statusidle--claim"></span>`claim` | string | no | The standing ResourceClaim on this unit's screen, in the Player's namespace. The pod that draws references it by name in its resourceClaims. Empty under media.liken.sh/none, where no claim exists. |
| <span id="statusidle--requests"></span>`requests` | []string | no | The claim's request names, in claim order: draw, and render where the Player states a render node. The container that draws states one resources.claims entry per name. Empty under media.liken.sh/none. |
| <span id="statusidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | The resolved seconds of quiet before the screen fades to black. Zero means the screen never fades on its own. The client that draws holds this timer, so the field is always written: zero is a policy, and an absent field is not one. |
| <span id="statusidle--offafterseconds"></span>`offAfterSeconds` | integer | no | The resolved seconds of quiet before the panel goes dark, at least fadeAfterSeconds. Zero means the panel never goes dark on its own. It is always written, for the reason fadeAfterSeconds is. |
| <span id="statusidle--bus"></span>`bus` | [object](#statusidlebus) | no | The bus facts a delegate's client reads. With the two windows above, this block is the whole contract a delegate wires its client from. It is present under every controller but media.liken.sh/none. |

#### status.idle.bus

The bus facts a delegate's client reads. With the two windows above, this block is the whole contract a delegate wires its client from. It is present under every controller but media.liken.sh/none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidlebus--address"></span>`address` | string | no | The broker, as host:port. It is the address the operator itself connects to. |
| <span id="statusidlebus--statustopic"></span>`statusTopic` | string | no | The retained topic that carries the unit's presentable state: its name, its activity, the Play it runs, and its parts. A client reads it on subscribe and asks for nothing. |
| <span id="statusidlebus--volumetopic"></span>`volumeTopic` | string | no | The retained topic that carries the unit's level, as {"level": 0.63, "muted": false}, where the level is the fraction of the device's max. The media operator is its only writer. The client draws the indicator for each live message and publishes no level. A volume key reaches the media operator from the controller's events topic, and a client that asks for a step publishes on this topic plus /commands. Empty means the unit has no sinks: the client subscribes to no level and draws none. |
| <span id="statusidlebus--commandstopic"></span>`commandsTopic` | string | no | The topic the playback pod publishes play-next on when a person takes the up-next offer on the scrubber. The client that wrote the Play reads that ask and starts the next work. When a Play ends, the client's own surface is on the screen again and the retained status is the cue, so nothing is published here for it. The pod also publishes home when a person presses home during a film, just before the Play ends, and the client reads that ask as a press of the home key. |
| <span id="statusidlebus--paneltopic"></span>`panelTopic` | string | no | The retained topic a client states its panel desire on, as on or off. The client holds no API credentials, so the operator reads the desire here and overrides the screen's Display. |
| <span id="statusidlebus--powertopic"></span>`powerTopic` | string | no | The topic a power press on this unit publishes an ask on while the unit's status topic states the power mode room: toggle for KEY_POWER and KEY_POWER2, off for KEY_SLEEP, and on for KEY_WAKEUP, the names the kernel gives a TV remote's Power Off Function and Power On Function. The media operator writes each ask into the Receiver's session, and the equipment operator turns the room off or on. The media operator also publishes wake here when a person picks the unit's input in the TV's source menu while the screen sleeps, and sleep when the TV goes to standby, and the client wakes or sleeps the screen. Every unit carries the topic, wired through a Receiver or not, so a delegate's pod stays the same when a Receiver is wired or removed. In the power mode screen, a power press reaches the client, which lowers the shade. |
| <span id="statusidlebus--remotes"></span>`remotes` | [\[\]object](#statusidlebusremotes) | no | The unit's controllers, one entry each, in spec.remotes order. That position is the index a focus moment carries, and it is the order the status topic lists the parts in. A unit with no controllers lists none. |

#### status.idle.bus.remotes[]

The unit's controllers, one entry each, in spec.remotes order. That position is the index a focus moment carries, and it is the order the status topic lists the parts in. A unit with no controllers lists none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidlebusremotes--events"></span>`events` | string | no | The topic this controller's key events arrive on, each under the kernel's name for the key. The client gates every press on the mark below. |
| <span id="statusidlebusremotes--focus"></span>`focus` | string | no | The retained topic that carries this controller's focus mark, the name of the Player it drives now. The client acts on a press only while the mark names this Player. The cycle topic is this one plus /cycle. |

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
| `Captured` | Normal | `media-api` returned bytes of the unit. The message names the caller, the aspect, and the media type. The same caller who takes the same capture again within ten minutes adds to the count of one `Event`. |

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
