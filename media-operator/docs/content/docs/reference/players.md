---
title: Players
weight: 10
toc: true
---

# Players

A `Player` is one named unit of equipment at one spot, for one
purpose: a lone speaker, a TV with its built-in speakers, a TV with
a receiver. The spec selects the unit's devices out of what the
hardware operators publish, with the same CEL selectors a
hand-written `ResourceClaim` would use. A `Player` is equipment, not
a running thing: the operator turns it into claims only while a
[Play](/docs/reference/plays/) runs on it, and an idle `Player`
holds nothing.

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

## The spec

All of a `Player`'s devices must be reachable from one machine. A
`Play` becomes one pod, and the scheduler places that pod on the
machine that owns every claimed device, so a `Player` whose devices
span machines produces `Play`s that stay `Pending`. The spec must
select a display or at least one sink; `render` alone plays nothing.

| Field | What it declares |
|---|---|
| `zone` | the area this unit is in, such as `living-room`; a word for grouping and display, nothing acts on it yet |
| `displayName` | the human name of the unit, shown by the idle screen in place of the object name |
| `display` | the display this unit shows video on; omit it for an audio-only unit |
| `render` | the GPU render node the player program decodes and draws with; `mpv` needs one to put video on a display |
| `sinks` | the audio outputs this unit plays sound through |
| `control` | the panel's DDC/CI control device, an opt-in; the idle pod darkens the panel through it |
| `remotes` | the controllers this unit owns, each naming a [Remote](/docs/reference/remotes/) in the same namespace |
| `audioLanguages`, `subtitleLanguages`, `subtitles` | per-unit overrides of the language preferences; omit them to inherit the default `MediaPreferences` |
| `idle` | this unit's idle screen policy, field by field over the default `MediaPreferences` |

`display`, `render`, `sinks` entries, and `control` share one shape,
a device selection:

| Field | What it declares |
|---|---|
| `class` | the `DeviceClass` the claim allocates through |
| `displayName` | the human name of the selection, shown in the idle screen's parts list |
| `selector` | a CEL expression over `device.attributes`; omitted, the class alone chooses |
| `parameters` | opaque configuration for the driver that prepares the device, carried onto the claim unread |

Each `remotes` entry names the controller and, optionally, how it
maps on this unit:

| Field | What it declares |
|---|---|
| `name` | the `Remote` this unit owns, by name, in this namespace |
| `displayName` | the human name of the controller, shown in the idle screen's parts list |
| `keymap` | a per-unit [Keymap](/docs/reference/keymaps/) override; empty, the unit reads the `Remote`'s own `Keymap` |

The `idle` block states what the screen does while nothing plays.
Each field overrides the default `MediaPreferences` on its own:

| Field | What it declares |
|---|---|
| `fadeAfterSeconds` | seconds of quiet before the idle screen fades to black; zero disables the fade |
| `offAfterSeconds` | seconds of quiet before the panel itself goes dark, at least `fadeAfterSeconds`; it acts only with a `control` device |
| `offMode` | what the off window writes: `backlight`, the default, always wakes over DDC; `power` is deeper, and some panels never answer from it |

## The status

Only the operator writes the status. It is derived from the `Play`s
that name the `Player`, so it is empty until one does.

| Field | What it reports |
|---|---|
| `activity` | `Playing`, `Starting`, or `Idle` |
| `play` | the name of the `Play` on this unit, empty while `Idle` |
| `panel` | the panel state the idle sidecar last actuated: `On`, `BacklightOff`, `Off`, or `Unresponsive` |

## On the bus

The `players` tree describes the equipment, with or without a
running `Play`. See [the media bus](/docs/reference/bus/) for the
rules every topic follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `players/{namespace}/{name}/status` | the operator | yes | the unit's presentable state |
| `players/{namespace}/{name}/volume` | the operator and the pods | yes | the listening level |
| `players/{namespace}/{name}/panel` | the idle pod | yes | the panel state |
| `players/{namespace}/{name}/commands` | the operator | no | a command for the idle pod |

### `status`

The presentable state of one unit: its friendly name, what it is
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
        {"name": "Studio Controller", "kind": "remote", "connected": true}
      ]
    }

`activity` is the same word the Kubernetes status carries. `play` is
present while a run starts or plays: `name` is the object a person
finds with `kubectl`, and `title` is the one line a screen draws. A
component's `kind` is `display`, `sink`, or `remote`, and only a
remote carries `connected`, because a wired screen reports no
presence.

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

What the idle sidecar last actuated on the unit's screen:

    {"state": "On"}

The states are the four the `Player` status carries: `On`,
`BacklightOff`, `Off`, and `Unresponsive`. The sidecar holds no API
credentials, so the operator folds this topic into
`status.panel`.

### `commands`

The operator's channel to the standing idle pod. It carries
`{"action": "re-present"}` when a `Play` ends, and the idle sidecar
recreates the idle surface. A controller never sends it, and it is
display plumbing, not part of the media vocabulary a
[Play's commands topic](/docs/reference/plays/#commands) accepts.
