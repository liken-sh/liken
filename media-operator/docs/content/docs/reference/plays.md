---
title: Plays
weight: 20
toc: true
---

# Plays

A `Play` is one run of media on a [Player](/docs/reference/players/):
a film, an album, or a season of episodes, played in order. Its
lifecycle is analogous to a `Job`'s: it runs once to completion, and
it stays for its status until `ttlSecondsAfterFinished` passes or a
person deletes it. Create a `Play` to start it, delete it to stop it
early, and `kubectl get plays` lists what plays right now.

The operator reconciles a `Play` into one playback pod and the
claims that pod needs, all owned by the `Play`, so deleting the
`Play` is the whole teardown: the garbage collector takes the pod
and the claims with it. A `Finished` run leaves nothing running.

The spec is immutable, like a `Job`'s template. A `Play` whose
player or media changed mid-run would describe a different run;
delete the `Play` and create another.

    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: dune
      namespace: media
    spec:
      players: [studio]
      items:
        - uri: nfs://nas/media/movies/Dune (2021)/Dune.mkv
          presentation:
            type: video
            hint: movie
            title: Dune
            year: 2021
      start: "0:10:00"

## The spec

| Field | What it declares |
|---|---|
| `players` | the `Player`s this run plays on, by name, in this namespace; one entry today, and the carriage layer lifts the limit |
| `items` | the media to play in order; each entry is a `uri` and an optional `presentation` |
| `start` | where in the first item the run begins, such as `0:10:00` or `600`; this is also how a run resumes |
| `trickplayInterval` | the seconds one trickplay tile covers, as a Go duration like `10s`, the default |
| `ttlSecondsAfterFinished` | how long the `Play` stands after it finishes; 300 seconds when omitted, and zero deletes it at once |
| `audioLanguages`, `subtitleLanguages`, `subtitles` | per-run overrides of the language preferences, the most specific tier |
| `volume` | the level this run starts at; the operator writes it to the unit's volume topic before the pod exists |

An item's `uri` resolves by scheme: `https://` becomes a stream the
player reads directly, and `nfs://host/export/path` becomes a mount
on the playback pod. A scheme the operator does not know fails the
`Play` before any pod exists.

An item's `presentation` declares what the player cannot read from
the file, so the display renders the item the way the library that
fed it describes it. Omit the block for a loose file, and the
display falls back to the file's own tags:

| Field | What it declares |
|---|---|
| `type` | `video`, `music`, or `image`; the display tunes its layout by it |
| `hint` | the finer kind within the type: `movie`, `series`, or `album` |
| `title` | the item's name, which overrides the file's own tag |
| `series`, `season`, `episode`, `episodeTitle`, `date` | the episode's place in its show |
| `year` | the release year, shown under a movie's title |
| `logo` | the logo art URI, `nfs://` or `https://`, resolved the way the media URI is |
| `trickplay` | the item's trickplay directory URI; the display shows a tile from its sprite sheets on the scrub cursor |

## The status

Only the operator writes the status. The playback pod holds no API
credentials; it reports on the bus, and the operator writes here.

| Field | What it reports |
|---|---|
| `phase` | `Pending`, `Running`, `Finished`, or `Failed`; the lifecycle, and it moves forward only |
| `activity` | the phase and the paused flag folded into one word: `Starting`, `Playing`, `Paused`, `Finished`, or `Failed` |
| `paused` | true while the player holds the current item still; the phase stays `Running` |
| `item` | which URI plays now, counting from 1 in spec order |
| `position`, `duration` | the playhead and the current item's length, each as `H:MM:SS` |
| `pod` | the playback pod's name, for `kubectl describe` and `logs` |
| `message` | why the phase is what it is, when a word is not enough |
| `finishedAt` | when the operator first read the phase as `Finished`; the time-to-live counts from here |
| `audioLanguages`, `subtitleLanguages`, `subtitles` | the resolved preferences this run applied |
| `audioLanguage`, `subtitleLanguage` | the languages of the tracks the player chose, so a code that matched no track shows plainly |

## On the bus

The `plays` tree carries one run's commands, its report, and its
availability. [The media bus](/docs/reference/bus/) gives the rules
the tree follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | no | one named command |
| `plays/{namespace}/{name}/status` | the playback pod | yes | the run's report |
| `plays/{namespace}/{name}/availability` | the playback pod | yes | `online` or `offline` |

### `commands`

The one open surface a program joins a run on in media terms. A
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
absent while no track of that kind plays. One more field, `ended`,
appears when the run is over and stays set in every later report of
the same run. The pod takes seconds to terminate, so the operator
reads this mark and returns the unit to idle in bus time instead of
pod time.

The operator folds each report into the `Play`'s Kubernetes status,
so a program that only wants the current position may read either
surface.

### `availability`

`online` or `offline`, retained. The pod names this topic as its
MQTT Last Will with `offline` as the payload, and publishes `online`
once it connects, so a retained status a killed pod left behind does
not read as a live run.
