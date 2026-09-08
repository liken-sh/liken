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
Between runs, the operator holds one claim on the unit's display
for the idle screen. It claims the other devices only while a
[Play](/docs/reference/plays/) runs on it.

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

A Player is one named unit of equipment. A Play names a Player to run media on it, and the media operator turns the Player into device claims: the display's claim stands between runs, for the idle screen, and the other devices are claimed only while a Play runs.

## spec

The devices that form the unit, each selected from what a hardware operator publishes. All of a Player's devices must be reachable from one machine, because a Play becomes one pod and the scheduler places that pod on the machine that owns every claimed device. A Player whose devices span machines produces Plays that stay Pending. The spec must select a display or at least one sink; render alone plays nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--zone"></span>`zone` | string | no | The area this Player is in, such as living-room. A word for grouping and display; nothing acts on it yet. |
| <span id="spec--displayname"></span>`displayName` | string | no | The human name of this unit, the one the idle screen and later ambient surfaces show in place of the object name, such as Studio Lab. Omit it, and the idle screen falls back to the object name. |
| <span id="spec--display"></span>`display` | [object](#specdisplay) | no | The display this Player shows video on. Omit it for an audio-only Player. |
| <span id="spec--sinks"></span>`sinks` | [\[\]object](#specsinks) | no | The audio outputs this Player plays sound through. |
| <span id="spec--render"></span>`render` | [object](#specrender) | no | The GPU render node the player program decodes and draws with. Omit it only for an audio-only Player; mpv needs a GPU to put video on a display. |
| <span id="spec--remotes"></span>`remotes` | [\[\]object](#specremotes) | no | The controllers this unit owns, each naming a Remote in the same namespace. The Play's pod builds one translator sidecar per entry. |
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
| <span id="specdisplayparameters--values"></span>`values` | object | no | The parameters themselves. The driver defines them; this operator carries them. |

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

The controllers this unit owns, each naming a Remote in the same namespace. The Play's pod builds one translator sidecar per entry.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specremotes--name"></span>`name` | string | yes | The Remote this unit owns, by name, in this namespace. |
| <span id="specremotes--displayname"></span>`displayName` | string | no | The human name of this controller, the one the idle screen shows in its parts list, such as Studio Dualsense Controller. Omit it, and the idle screen falls back to name. |

### spec.idle

This unit's idle screen policy. Each field overrides the default MediaPreferences on its own.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--controller"></span>`controller` | string | no | The operator that draws this unit's idle screen, as a domain-qualified name. Two names belong to the media operator: media.liken.sh/idle-screen, which is the default and draws the idle screen this operator ships, and media.liken.sh/none, under which nothing draws an idle screen on this unit and no claim stands. Any other name hands the screen to the operator that answers to it, which reads status.idle for the claim to reference, the requests it carries, the two windows, and the bus it joins; image has no effect under such a name, because that operator brings its own pod. Omit it to inherit the default MediaPreferences. Pattern: `^[a-z0-9.-]+/[a-z0-9-]+$`. |
| <span id="specidle--image"></span>`image` | string | no | The container image that draws this unit's idle screen. The image starts with its own entrypoint and reads the unit's state from the bus. It holds the fade and off windows, the focus gate, the shade, the volume step, and the panel desire in its own process. Omit it to inherit the default from MediaPreferences. Where no tier names an image, the screen runs the idle client the media operator ships. |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds of quiet before the idle screen fades to black. Zero disables the automatic fade; omit it to inherit the default MediaPreferences. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds of quiet before the panel itself goes dark, at least fadeAfterSeconds. Zero or unset means the panel never goes dark on its own. The panel goes dark only where the cluster runs a display-operator that publishes a Display for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | Which override the off window applies to the screen's Display. The default, backlight, holds the panel at brightness zero, which still answers DDC. Power off stops some panels from answering DDC at all; state it only for a panel that woke from it in a drill. One of: `backlight`, `power`. |

## status

What plays on this Player now, written only by the media operator. It is derived from the Plays that name the Player, so it is empty until one does.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--activity"></span>`activity` | string | no | Whether the Player performs a run now. Playing is a Play running on it, Starting is a Play whose pod has not begun, and Idle is no Play at all. One of: `Playing`, `Starting`, `Idle`. |
| <span id="status--play"></span>`play` | string | no | The name of the Play on this Player, in the same namespace. Empty while the Player is Idle. |
| <span id="status--panel"></span>`panel` | string | no | What the screen's Display last observed: On, BacklightOff, or Off. Empty until a Display carries an observation for the unit's screen. |
| <span id="status--receiver"></span>`receiver` | [object](#statusreceiver) | no | The equipment this unit's cable lands on, matched from the machine the unit draws on and the monitor id of its screen. Absent for a unit that plays straight into its panel. |
| <span id="status--screen"></span>`screen` | [object](#statusscreen) | no | The last screen the idle claim resolved to. The operator keeps it while the panel is away, so it can still read the screen's Display and say why the idle pod waits. Absent until the claim has resolved once. |
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
| <span id="statusidle--claim"></span>`claim` | string | no | The standing ResourceClaim on this unit's screen, in the Player's namespace. The pod that draws references it by name in its resourceClaims. Empty under media.liken.sh/none, where no claim stands. |
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
| <span id="statusidlebus--volumetopic"></span>`volumeTopic` | string | no | The retained topic that carries the unit's level and its muted flag. Empty means the unit has no sinks: the client subscribes to no level, draws none, and publishes none. |
| <span id="statusidlebus--volumeownertopic"></span>`volumeOwnerTopic` | string | no | The retained topic that carries the owner mark for the unit's level, present whenever volumeTopic is. A non-empty payload means equipment owns the level, and the client then draws no level of its own and applies none. An empty payload means no owner holds it. |
| <span id="statusidlebus--commandstopic"></span>`commandsTopic` | string | no | The topic the operator publishes re-present on when a Play ends. The client maps a fresh surface when it arrives. The playback pod publishes play-next on the same topic when a person takes the up-next offer on the scrubber, and the client that wrote the Play reads that ask and starts the next work. A client publishes nothing here. |
| <span id="statusidlebus--paneltopic"></span>`panelTopic` | string | no | The retained topic a client states its panel desire on, as on or off. The client holds no API credentials, so the operator reads the desire here and overrides the screen's Display. |
| <span id="statusidlebus--remotes"></span>`remotes` | [\[\]object](#statusidlebusremotes) | no | The unit's controllers, one entry each, in spec.remotes order. That position is the index a focus moment carries, and it is the order the status topic lists the parts in. A unit with no controllers lists none. |

#### status.idle.bus.remotes[]

The unit's controllers, one entry each, in spec.remotes order. That position is the index a focus moment carries, and it is the order the status topic lists the parts in. A unit with no controllers lists none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidlebusremotes--events"></span>`events` | string | no | The topic this controller's key events arrive on, each under the kernel's name for the key. The client gates every press on the mark below. |
| <span id="statusidlebusremotes--focus"></span>`focus` | string | no | The retained topic that carries this controller's focus mark, the name of the Player it drives now. The client acts on a press only while the mark names this Player. The cycle topic is this one plus /cycle. |

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

The commands around the `Player`'s idle screen. They are not
retained ([why](/docs/reference/bus/#retained-state-and-events)), and
a controller never sends one directly. A client publishes nothing
here.

Two programs write here. The operator publishes `re-present` when a
`Play` ends. The playback pod's command sidecar publishes `play-next`
when a person takes the up-next offer on the scrubber, and its
`request` is the `Play`'s own `spec.next.request`, byte for byte. The
client that wrote the `Play` reads that ask and creates the next
`Play`.

| Message | Writer | What it says |
|---|---|---|
| `{"action": "re-present"}` | the operator | A `Play` ended. The idle screen client maps a fresh surface. |
| `{"action": "play-next", "request": {...}}` | the playback pod | A person took the up-next offer. `request` is the `Play`'s `spec.next.request`. |
