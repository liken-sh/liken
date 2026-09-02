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
| <span id="status--idle"></span>`idle` | [object](#statusidle) | no | What draws this unit's idle screen, and everything the operator that draws it needs. A delegate wires its client from this block alone, and it sets MEDIA_PLAYER_NAME on that client to the Player's metadata.name, the value every focus mark holds. The block is absent for a Player that drives no screen and where the cluster names no display-draw class. A delegate reads this block and never the spec, because the spec may inherit its controller from the default MediaPreferences, and only the media operator resolves the tiers. |

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
| <span id="statusidlebus--commandstopic"></span>`commandsTopic` | string | no | The topic the operator publishes re-present on when a Play ends. The client maps a fresh surface when it arrives. The operator is the only writer, and a client publishes nothing here. |
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
running `Play`. See [the media bus](/docs/reference/bus/) for the
rules every topic follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's name, activity, and parts |
| `players/{namespace}/{name}/volume` | the operator and the pods | yes | the listening level |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel desire |
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

The unit's own state stays on the retained topics above. A client
reads `status` for the name, the activity, and the parts, and
`volume` for the level. The first `volume` message of a session is
the broker's retained catch-up, which sets the level and shows no
indicator, and every message after it is a press.

### `panel`

The desire the idle screen client states for the unit's screen:

    {"desire": "off"}

The two desires are `on` and `off`. The client holds no API
credentials and writes no hardware, so the operator reads this topic
and applies or lifts `spec.override` on the screen's `Display`. What
the panel actually shows comes back the other way, from the
`Display`'s observed state into `status.panel`.

### `commands`

The one display command around the `Player`'s idle screen. It is not
retained, because it is an event and not a state, and a controller
never sends it directly. The operator is the only writer, and a client
publishes nothing here.

| Message | Writer | What it says |
|---|---|---|
| `{"action": "re-present"}` | the operator | A `Play` ended. The idle screen client maps a fresh surface. |
