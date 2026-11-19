---
title: Players
weight: 10
toc: true
---

<!-- Generated from deploy/players-crd.yaml by crdref. Do not edit. -->

# `Player`

A `Player` is one unit of equipment that plays media, such as a lone
speaker, a TV with its built-in speakers, or a TV with a receiver. Its
spec selects the `Player`'s devices from the devices that the hardware
operators publish, with the same CEL selectors that you would write in
a `ResourceClaim`. For the idle screen, the operator keeps a claim on
the shared draw device of the `Player`'s display, and on the render node
when `spec.render` is set. It claims the other devices only while a
[`Play`](/docs/reference/plays/) runs on the `Player`.

A `Player` name is at most 32 characters, because `media-operator` and
`library-operator` build the names of the objects they create from it,
and Kubernetes limits some of those names to 63 characters.

A `Player` is namespaced. The operator creates its claims and pods in
the `Player`'s namespace, and a `Play` refers to a `Player` in its own
namespace. So RBAC on the namespace covers all of them.

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

The `class` values are names of `DeviceClass` objects. You create the
consumer `DeviceClass` objects yourself, and the manual of each
hardware operator gives the YAML for its class.

One named set of equipment that plays media. A Play names a Player to run media on it. The media operator keeps a claim on the display at all times, for the idle screen, and claims the other devices only while a Play runs.

## spec

The devices of this Player, each selected from the devices that a hardware operator publishes. All of a Player's devices must be on one machine, because a Play runs as one pod and the scheduler places that pod on the machine that has every claimed device. When the devices are on different machines, the pod of each Play stays `Pending`. The spec must select a display or at least one sink, because a render node alone plays nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--zone"></span>`zone` | string | no | The area of the building where this Player is, for example `living-room`. The Zone printer column shows it, and the `media_players` metric counts Players by it. The operator does not act on it in any other way. |
| <span id="spec--displayname"></span>`displayName` | string | no | The name that the idle screen shows for this Player, for example `Studio Lab`. When you omit it, the idle screen shows the object name. |
| <span id="spec--display"></span>`display` | [object](#specdisplay) | no | The display where this Player shows video. Omit it for an audio-only Player. |
| <span id="spec--sinks"></span>`sinks` | [\[\]object](#specsinks) | no | The audio outputs where this Player plays sound. |
| <span id="spec--render"></span>`render` | [object](#specrender) | no | The GPU render node that the player program uses to decode and draw video. Omit it only for an audio-only Player, because mpv needs a GPU to show video on a display. |
| <span id="spec--remotes"></span>`remotes` | [\[\]object](#specremotes) | no | The controllers that drive this Player. Each entry names a Remote in the same namespace. Each Remote runs its own pod, and the command sidecar of a Play reads the events that all of these pods publish. |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | The order of audio languages for this Player. It replaces the order in the default MediaPreferences. When you omit it, the Player uses that order. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | The order of subtitle languages for this Player. It replaces the order in the default MediaPreferences. When you omit it, the Player uses that order. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | When this Player shows subtitles. It replaces the setting in the default MediaPreferences. When you omit it, the Player uses that setting. One of: `on`, `off`, `auto`. |
| <span id="spec--sdh"></span>`sdh` | string | no | Whether a track for the deaf and hard of hearing (SDH) ranks ahead of a full track in the same language. It replaces the setting in the default MediaPreferences. When you omit it, the Player uses that setting. One of: `prefer`, `avoid`. |
| <span id="spec--idle"></span>`idle` | [object](#specidle) | no | The settings for this Player's idle screen. Each field replaces the same field of the default MediaPreferences. A field that you omit keeps the default. |
| <span id="spec--power"></span>`power` | string | no | Whether the Player is on. `Off` turns its panel, TV, and receiver off and stops its Play. A Play that starts sets `On`. When you omit it, the Player is on. One of: `On`, `Off`. |
| <span id="spec--passthrough"></span>`passthrough` | string | no | Whether the Player sends the Dolby and DTS tracks of a film to the receiver without decoding them. The receiver then decodes them, and its height speakers play Dolby Atmos and DTS:X. `On` has an effect only for a Player with one entry in `sinks` whose volume a reachable Receiver sets, and only for the formats that the Sink lists in `status.passthrough`. While a track plays this way, only the volume control of the receiver changes the volume. When you omit it, the Player decodes the tracks (`Off`). One of: `On`, `Off`. |

### spec.display

The display where this Player shows video. Omit it for an audio-only Player.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdisplay--class"></span>`class` | string | yes | The name of the `DeviceClass` that the claim allocates from. The cluster owner creates this class. The manual of each hardware operator has the YAML for its class. |
| <span id="specdisplay--displayname"></span>`displayName` | string | no | The name that the idle screen shows for this device in its list of parts, for example `Portable Screen`. When you omit it, the idle screen shows the `DeviceClass` name. |
| <span id="specdisplay--selector"></span>`selector` | string | no | A CEL expression over `device.attributes`, the same expression that a ResourceClaim written by hand would have. When you omit it, the claim can allocate any device of the class. That is correct for a class that has one kind of device. |
| <span id="specdisplay--parameters"></span>`parameters` | [object](#specdisplayparameters) | no | Configuration for the driver that prepares the device. The operator copies it to the claim and does not read it. The manual of the display operator describes its parameters, such as mode and brightness. |

#### spec.display.parameters

Configuration for the driver that prepares the device. The operator copies it to the claim and does not read it. The manual of the display operator describes its parameters, such as mode and brightness.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specdisplayparameters--driver"></span>`driver` | string | yes | The name of the driver that reads the parameters, for example `display.liken.sh`. |
| <span id="specdisplayparameters--values"></span>`values` | object | no | The parameters. The driver defines them, and this operator copies them to the claim. |

### spec.sinks[]

The audio outputs where this Player plays sound.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsinks--class"></span>`class` | string | yes | The name of the `DeviceClass` that the claim allocates from. The cluster owner creates this class. The manual of each hardware operator has the YAML for its class. |
| <span id="specsinks--displayname"></span>`displayName` | string | no | The name that the idle screen shows for this device in its list of parts, for example `Built-in Speakers`. When you omit it, the idle screen shows the `DeviceClass` name. |
| <span id="specsinks--selector"></span>`selector` | string | no | A CEL expression over `device.attributes`, the same expression that a ResourceClaim written by hand would have. |
| <span id="specsinks--parameters"></span>`parameters` | [object](#specsinksparameters) | no | Configuration for the driver that prepares the device. The operator copies it to the claim and does not read it. The manual of the audio operator describes its parameters, such as `codec`. |

#### spec.sinks[].parameters

Configuration for the driver that prepares the device. The operator copies it to the claim and does not read it. The manual of the audio operator describes its parameters, such as `codec`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specsinksparameters--driver"></span>`driver` | string | yes | The name of the driver that reads the parameters, for example `audio.liken.sh`. |
| <span id="specsinksparameters--values"></span>`values` | object | no | The parameters. The driver defines them, and this operator copies them to the claim. |

### spec.render

The GPU render node that the player program uses to decode and draw video. Omit it only for an audio-only Player, because mpv needs a GPU to show video on a display.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specrender--class"></span>`class` | string | yes | The name of the `DeviceClass` that the claim allocates from. A render class usually needs no selector, because it has one kind of device. |
| <span id="specrender--displayname"></span>`displayName` | string | no | The name that the idle screen shows for this device in its list of parts. When you omit it, the idle screen shows the `DeviceClass` name. |
| <span id="specrender--selector"></span>`selector` | string | no | A CEL expression over `device.attributes`. Use it to choose one GPU on a machine that has more than one. |

### spec.remotes[]

The controllers that drive this Player. Each entry names a Remote in the same namespace. Each Remote runs its own pod, and the command sidecar of a Play reads the events that all of these pods publish.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specremotes--name"></span>`name` | string | yes | The name of the Remote, in this namespace. |
| <span id="specremotes--displayname"></span>`displayName` | string | no | The name that the idle screen shows for this controller in its list of parts, for example `Studio Dualsense Controller`. When you omit it, the idle screen shows `name`. |

### spec.idle

The settings for this Player's idle screen. Each field replaces the same field of the default MediaPreferences. A field that you omit keeps the default.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specidle--controller"></span>`controller` | string | no | The operator that draws this Player's idle screen, as `domain/name`. With `media.liken.sh/idle-screen`, media-operator runs its own idle screen. With `media.liken.sh/none`, nothing draws an idle screen and media-operator creates no claim. Any other name hands the screen to the operator that handles that name. That operator reads the claim, the request names, the time limits, and the message bus from `status.idle`, and runs its own pod, so `image` has no effect. When you omit this field, the Player uses the default MediaPreferences, and when that sets none, `media.liken.sh/idle-screen`. Pattern: `^[a-z0-9.-]+/[a-z0-9-]+$`. |
| <span id="specidle--image"></span>`image` | string | no | The container image that draws this Player's idle screen. The image runs its own entrypoint and reads the Player's state from the message bus. A replacement image must do everything the shipped idle client does: fade the screen, ignore key presses while the focus mark is another Player's name, draw the volume indicator, and publish whether the panel should be on. When you omit this field, the Player uses the image in the default MediaPreferences. When neither sets an image, the screen runs the idle client that media-operator ships. |
| <span id="specidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds with no activity before the idle screen fades to black. Zero turns the fade off. When you omit it, the Player uses the default MediaPreferences, and when that sets none, 600. |
| <span id="specidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds with no activity before the panel turns off. The shipped idle client treats a value below `fadeAfterSeconds` as `fadeAfterSeconds`. Zero keeps the panel on. When you omit it, the Player uses the default MediaPreferences, and when that sets none, the panel stays on. The panel turns off only when display-operator publishes a `Display` for the screen. |
| <span id="specidle--offmode"></span>`offMode` | string | no | How the operator turns the panel off, through an override on the screen's `Display`. With `backlight`, the default, the panel's brightness goes to zero and the panel still responds to DDC commands. With `power`, the panel powers off, and some panels then stop responding to DDC, so use it only for a panel that you have seen turn on again after it. One of: `backlight`, `power`. |

## status

What this Player is doing now and what it is connected to. Only media-operator writes it, for every Player, idle or playing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--activity"></span>`activity` | string | no | What the Player is doing. `Playing` means a Play runs on it. `Starting` means a Play exists and its pod has not started. `Idle` means no Play on this Player is `Pending` or `Running`. A `Finished` or `Failed` Play can still name the Player until it is deleted. One of: `Playing`, `Starting`, `Idle`. |
| <span id="status--play"></span>`play` | string | no | The name of the Play on this Player, in the same namespace. Empty while the Player is `Idle`. |
| <span id="status--power"></span>`power` | string | no | The value of `spec.power` that the media operator last applied to the Player's hardware. One of: `On`, `Off`. |
| <span id="status--poweredonat"></span>`poweredOnAt` | string | no | When the media operator last applied a change of `spec.power` to `On`. |
| <span id="status--panel"></span>`panel` | string | no | The state that the screen's Display last reported: `On`, `BacklightOff`, or `Off`. Empty until a Display reports a state for this Player's screen. |
| <span id="status--receiver"></span>`receiver` | [object](#statusreceiver) | no | The Receiver that this Player's video goes through. The operator finds it from the machine that draws the screen and the monitor id of the screen. Absent for a Player that plays directly to its panel. |
| <span id="status--screen"></span>`screen` | [object](#statusscreen) | no | The screen that the idle claim last allocated. The operator keeps this value while the panel is disconnected, so it can still read the screen's `Display` and report in the `Screen` condition why the idle pod waits. Absent until the claim has been allocated once. |
| <span id="status--sinks"></span>`sinks` | [\[\]object](#statussinks) | no | The Sink that each entry of `spec.sinks` was allocated, in the same order. The operator writes the list from an allocated playback claim, and keeps it after the Play ends, as it keeps `status.screen`. Capturing audio from these Sinks still needs a running Play. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The Player's conditions, `Screen` and `PowerApplied`. `Screen` follows the `Connected` condition of the screen's `Display`, and is absent until the idle claim has been allocated once. `True` with reason `Present` means the `Display` reports a panel on its connector. `False` with reason `PanelAway` means it reports no panel, for example because the monitor shows another input. The idle claim is then deallocated and the idle pod stays `Pending` until the panel returns. This is expected, so an alert on `Pending` pods can use this reason to skip the pod. `False` with any other reason copies the reason of the `Display`. `Unknown` with reason `NoDisplay` means no `Display` has the monitor id in `status.screen`, and `Unknown` with reason `NotReported` means the `Display` has no `Connected` condition. `PowerApplied` is absent until `spec.power` first changes. It is `True` with reason `Applied` when the operator applied the last change to the hardware, and `False` with reason `ReceiverAskFailed` or `DisplayWriteFailed` when a write failed. The operator then retries the change on its next pass. |
| <span id="status--idle"></span>`idle` | [object](#statusidle) | no | What draws this Player's idle screen, and everything that the operator drawing it needs. An operator that draws the idle screen in place of media-operator sets up its client from this block alone, and sets `MEDIA_PLAYER_NAME` on the client to the Player's `metadata.name`, which is the value of a controller's focus mark while this Player has the focus. It must read this block and not the spec, because only media-operator combines the Player's settings with the default MediaPreferences. The block is absent for a Player that has no screen, and when the cluster has no `DeviceClass` for drawing on a display. |

### status.receiver

The Receiver that this Player's video goes through. The operator finds it from the machine that draws the screen and the monitor id of the screen. Absent for a Player that plays directly to its panel.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusreceiver--name"></span>`name` | string | no | The name of the Receiver. |
| <span id="statusreceiver--input"></span>`input` | string | no | The input of the Receiver that this Player is connected to. |
| <span id="statusreceiver--reachable"></span>`reachable` | string | no | The status of the Receiver's `Reachable` condition. Empty until the Receiver has that condition. |

### status.screen

The screen that the idle claim last allocated. The operator keeps this value while the panel is disconnected, so it can still read the screen's `Display` and report in the `Screen` condition why the idle pod waits. Absent until the claim has been allocated once.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusscreen--node"></span>`node` | string | no | The machine that publishes the device that draws on the screen. |
| <span id="statusscreen--monitor"></span>`monitor` | string | no | The monitor id of the screen. It is the name of the screen's Display. |

### status.sinks[]

The Sink that each entry of `spec.sinks` was allocated, in the same order. The operator writes the list from an allocated playback claim, and keeps it after the Play ends, as it keeps `status.screen`. Capturing audio from these Sinks still needs a running Play.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussinks--request"></span>`request` | string | no | The name of the request in the claim, for example `audio0` for the first entry of `spec.sinks`. |
| <span id="statussinks--name"></span>`name` | string | no | The name of the `Sink` object. It is the name that the audio operator gives the device. |

### status.conditions[]

The Player's conditions, `Screen` and `PowerApplied`. `Screen` follows the `Connected` condition of the screen's `Display`, and is absent until the idle claim has been allocated once. `True` with reason `Present` means the `Display` reports a panel on its connector. `False` with reason `PanelAway` means it reports no panel, for example because the monitor shows another input. The idle claim is then deallocated and the idle pod stays `Pending` until the panel returns. This is expected, so an alert on `Pending` pods can use this reason to skip the pod. `False` with any other reason copies the reason of the `Display`. `Unknown` with reason `NoDisplay` means no `Display` has the monitor id in `status.screen`, and `Unknown` with reason `NotReported` means the `Display` has no `Connected` condition. `PowerApplied` is absent until `spec.power` first changes. It is `True` with reason `Applied` when the operator applied the last change to the hardware, and `False` with reason `ReceiverAskFailed` or `DisplayWriteFailed` when a write failed. The operator then retries the change on its next pass.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition type, `Screen` or `PowerApplied`. |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | The reason for the status. For `Screen`, it is `Present`, `PanelAway`, `NoDisplay`, `NotReported`, or a reason of the Display. For `PowerApplied`, it is `Applied`, `ReceiverAskFailed`, or `DisplayWriteFailed`. |
| <span id="statusconditions--message"></span>`message` | string | no | For `Screen`, the message of the Display, which names the connector. For `PowerApplied`, the change that the operator applied and the hardware that it applied it to. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed. |

### status.idle

What draws this Player's idle screen, and everything that the operator drawing it needs. An operator that draws the idle screen in place of media-operator sets up its client from this block alone, and sets `MEDIA_PLAYER_NAME` on the client to the Player's `metadata.name`, which is the value of a controller's focus mark while this Player has the focus. It must read this block and not the spec, because only media-operator combines the Player's settings with the default MediaPreferences. The block is absent for a Player that has no screen, and when the cluster has no `DeviceClass` for drawing on a display.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidle--controller"></span>`controller` | string | no | The controller that draws the idle screen: the first that is set of `spec.idle.controller`, the controller in the default MediaPreferences, and `media.liken.sh/idle-screen`. |
| <span id="statusidle--claim"></span>`claim` | string | no | The name of the ResourceClaim on this Player's screen, in the Player's namespace. The operator keeps this claim while a Play runs and while none runs, and the pod that draws the screen names it in its `resourceClaims`. Empty with `media.liken.sh/none`. |
| <span id="statusidle--requests"></span>`requests` | []string | no | The names of the requests in the claim, in claim order: `draw`, and `render` when the Player has a render node. The container that draws has one `resources.claims` entry for each name. Empty with `media.liken.sh/none`. |
| <span id="statusidle--fadeafterseconds"></span>`fadeAfterSeconds` | integer | no | Seconds with no activity before the screen fades to black, with the defaults applied. The client that draws the screen runs this timer. The operator always writes the field, so zero means that the screen does not fade. |
| <span id="statusidle--offafterseconds"></span>`offAfterSeconds` | integer | no | Seconds with no activity before the panel turns off, with the defaults applied. The operator always writes the field, so zero means that the panel stays on. The operator writes the value as set, and the shipped idle client treats a value below `fadeAfterSeconds` as `fadeAfterSeconds`. |
| <span id="statusidle--bus"></span>`bus` | [object](#statusidlebus) | no | The message bus settings for the client that draws the idle screen. Present for every controller except `media.liken.sh/none`. |

#### status.idle.bus

The message bus settings for the client that draws the idle screen. Present for every controller except `media.liken.sh/none`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidlebus--address"></span>`address` | string | no | The address of the broker that the operator uses, as `host:port`. |
| <span id="statusidlebus--statustopic"></span>`statusTopic` | string | no | The retained topic with the state that the screen shows: the Player's name, its activity, the Play that runs on it, and its parts. Because it is retained, a client receives the current state as soon as it subscribes. |
| <span id="statusidlebus--volumetopic"></span>`volumeTopic` | string | no | The retained topic with the Player's volume, as `{"level": 0.63, "muted": false}`, where `level` is the fraction of the device's maximum. Only media-operator publishes here. The client draws the volume indicator for each live message. It does not handle volume keys, because media-operator reads them from the controller's events topic. To change the volume, a client publishes to this topic with `/commands` added. Empty when the Player has no sinks, and the client then draws no indicator. |
| <span id="statusidlebus--commandstopic"></span>`commandsTopic` | string | no | The topic for requests to this Player, not retained. The playback pod publishes `play-next` here when a person accepts the up-next offer on the progress bar, and the program that created the Play reads it and creates the next Play. The pod publishes `home` just before the Play ends when a person presses the home key during a film, and the idle client handles it as a press of the home key. The end of a Play is not published here. The client reads it from the status topic. |
| <span id="statusidlebus--paneltopic"></span>`panelTopic` | string | no | The retained topic where the client publishes whether the panel should be on or off. The client has no Kubernetes API credentials, so the operator reads this topic and sets the override on the screen's `Display`. |
| <span id="statusidlebus--powertopic"></span>`powerTopic` | string | no | The topic where the operator publishes `wake` and `sleep` requests for the screen, not retained. The client wakes or sleeps the screen when it receives one. The operator publishes `wake` when `spec.power` changes to `On` and `sleep` when it changes to `Off`. For a Player with a Receiver, it also passes on the TV's requests: `wake` when a person selects this Player's input in the TV's source menu while the screen sleeps, and `sleep` when the TV goes to standby. Every Player with a screen has this topic, with or without a Receiver. |
| <span id="statusidlebus--remotes"></span>`remotes` | [\[\]object](#statusidlebusremotes) | no | The Player's controllers, one entry for each entry of `spec.remotes`, in the same order. A focus message identifies a controller by its index in this list, and the status topic lists the parts in this order. Empty for a Player with no controllers. |

#### status.idle.bus.remotes[]

The Player's controllers, one entry for each entry of `spec.remotes`, in the same order. A focus message identifies a controller by its index in this list, and the status topic lists the parts in this order. Empty for a Player with no controllers.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusidlebusremotes--events"></span>`events` | string | no | The topic where this controller's key events arrive, with the kernel's name for each key. Every Player that lists the controller receives them, so the client checks the focus mark before it acts. |
| <span id="statusidlebusremotes--focus"></span>`focus` | string | no | The retained topic with this controller's focus mark: the name of the Player that receives the controller's presses now. The client acts on a press only while the mark is this Player's name. To move focus to the next Player, the client publishes to this topic with `/cycle` added. |

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
