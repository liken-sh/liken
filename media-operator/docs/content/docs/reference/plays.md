---
title: Plays
weight: 20
toc: true
---

<!-- Generated from deploy/plays-crd.yaml by crdref. Do not edit. -->

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

A Play is one run of media on a Player: a film, an album, or a season of episodes, played in order. Create a Play to start it; delete the Play to stop it early.

## spec

What to play and where. The spec is immutable: a different film or a different player is a different Play.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--players"></span>`players` | []string | yes | The Players this Play runs on, by name, in this namespace. One entry today. |
| <span id="spec--items"></span>`items` | [\[\]object](#specitems) | yes | The media to play in order. Each entry is a URI and an optional presentation that declares how the display should render it. |
| <span id="spec--start"></span>`start` | string | no | Where in the first item the run begins, as a time the player accepts, such as 0:10:00 or 600. Omitted, the run begins at the start. Later items always begin at their own start. This is also how a run resumes: a new Play with the position a finished or deleted one reported. |
| <span id="spec--trickplayinterval"></span>`trickplayInterval` | string | no | The seconds one trickplay tile covers, as a Go duration like 10s. Jellyfin writes no manifest beside the sheets, so the Play declares it. Omitted, it defaults to 10s, the Jellyfin default. |
| <span id="spec--ttlsecondsafterfinished"></span>`ttlSecondsAfterFinished` | integer | no | How long this Play stays after it finishes, in seconds, the meaning a Job gives the name. While it stays, kubectl get plays still answers what just played and where it stopped; deleting the Play deletes that record. Omitted, it is 300 seconds. Zero deletes the Play as soon as it finishes. The playback pod does not wait for this window: it is deleted as soon as the run finishes. |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | A per-Play override of the audio language order, the most specific tier; omit it to inherit the Player. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | A per-Play override of the subtitle language order, the most specific tier; omit it to inherit the Player. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | A per-Play override of when subtitles show, the most specific tier; omit it to inherit the Player. One of: `on`, `off`, `auto`. |
| <span id="spec--volume"></span>`volume` | [object](#specvolume) | no | The level this run starts at. The operator writes it to the Player's volume topic before it creates the pod, so the value becomes the unit's state and stays after the film ends. Omitted, the run starts at whatever the unit already holds. |

### spec.items[]

One entry in the playlist: the URI to play and, optionally, how it should look.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitems--uri"></span>`uri` | string | yes | The operator resolves https:// to a stream the player reads directly, and nfs://host/export/path to a mount on the playback pod. A URI whose scheme the operator does not know fails the Play before any pod exists. |
| <span id="specitems--presentation"></span>`presentation` | [object](#specitemspresentation) | no | How the item should look, for the fields the display cannot read from the file. The library that fed the item supplies these, and the display prefers them over the container's tags. Omit the block for a loose file, and the display falls back to the file's own tags. |

#### spec.items[].presentation

How the item should look, for the fields the display cannot read from the file. The library that fed the item supplies these, and the display prefers them over the container's tags. Omit the block for a loose file, and the display falls back to the file's own tags.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentation--type"></span>`type` | string | no | The media type the display tunes its layout by. mpv cannot infer this, and the display does not read it from the file name. One of: `video`, `music`, `image`. |
| <span id="specitemspresentation--hint"></span>`hint` | string | no | The finer kind within the type. A video is a movie or a series, and music is an album. It selects the layout the display draws. An album also declares that the item's URI names a directory, which the playback pod expands into one timeline of the audio files it holds. The directory must hold at least one audio file, or the run fails. One of: `movie`, `series`, `album`. |
| <span id="specitemspresentation--title"></span>`title` | string | no | The item's name, which overrides the file's own tag. Set it when the tag is wrong or absent. |
| <span id="specitemspresentation--series"></span>`series` | string | no | The series this episode belongs to. |
| <span id="specitemspresentation--season"></span>`season` | integer | no | The season number of the episode. |
| <span id="specitemspresentation--episode"></span>`episode` | integer | no | The episode number within its season. |
| <span id="specitemspresentation--episodetitle"></span>`episodeTitle` | string | no | The title of the episode. |
| <span id="specitemspresentation--year"></span>`year` | integer | no | The release year, shown under a movie's title. |
| <span id="specitemspresentation--date"></span>`date` | string | no | The air date of an episode, shown on its line. Give it as an ISO date like 2017-03-05, and the display formats it. |
| <span id="specitemspresentation--artist"></span>`artist` | string | no | The artist of a music item, shown in the header under the title. For an album this field is the one source; a standalone track can also leave it unset and let its own tags supply it. |
| <span id="specitemspresentation--album"></span>`album` | string | no | The record a music item belongs to, shown in the header beside the year. It fills in the same way the artist does. |
| <span id="specitemspresentation--art"></span>`art` | string | no | The cover art URI, nfs:// or https://, resolved the way the media URI is. It is the first place the cover is looked for; a picture embedded in the file and a cover.jpg beside it follow, and the pod reads both of those itself. |
| <span id="specitemspresentation--logo"></span>`logo` | string | no | The logo art URI, nfs:// or https://, resolved the way the media URI is. The display shows it in the header in place of the title. |
| <span id="specitemspresentation--trickplay"></span>`trickplay` | string | no | The X.trickplay directory URI, nfs:// or https://, resolved the way the media URI is. The display shows a tile from it on the scrub cursor. |

### spec.volume

The level this run starts at. The operator writes it to the Player's volume topic before it creates the pod, so the value becomes the unit's state and stays after the film ends. Omitted, the run starts at whatever the unit already holds.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specvolume--level"></span>`level` | integer | no | The listening level, 0 to 100. 100 is unity, the player's own default, and the cap: a software gain above unity only distorts. Omitted, the level the unit already holds stays. |
| <span id="specvolume--muted"></span>`muted` | boolean | no | Whether the run starts muted. Omitted, the muted state the unit already holds stays. |

## status

What the playback pod reports, written only by the media operator. The playback pod itself holds no API credentials; it reports to the operator, and the operator writes here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | Where the run is in its life, in the words Jobs and Pods use. Pending is declared but not yet performing, Running is the pod performing the play, paused or not, and Finished and Failed are the two ends. The word is Running rather than Playing because a phase moves forward only, and a paused film would force Playing to flap; the paused field beside this one says the rest. One of: `Pending`, `Running`, `Finished`, `Failed`. |
| <span id="status--activity"></span>`activity` | string | no | The one word for what the Play does right now, the phase and the paused flag folded together. Starting is Pending, Playing and Paused both mean Running, and Finished and Failed match the phase. The phase is the lifecycle; the activity is what a person reads at a glance. One of: `Starting`, `Playing`, `Paused`, `Finished`, `Failed`. |
| <span id="status--paused"></span>`paused` | boolean | no | True while the player holds the current item still. The phase stays Running, because a pause does not advance the lifecycle. |
| <span id="status--item"></span>`item` | integer | no | Which URI plays now, counting from 1 in spec order. The third of five episodes shows 3. |
| <span id="status--position"></span>`position` | string | no | The playhead inside the current item, as H:MM:SS. |
| <span id="status--duration"></span>`duration` | string | no | The length of the current item, as H:MM:SS, once the player has read it. |
| <span id="status--pod"></span>`pod` | string | no | The playback pod's name, for kubectl describe and logs. The pod is owned by this Play and is deleted with it. |
| <span id="status--message"></span>`message` | string | no | The reason for the phase, as one line of text: the resolver refused a URI, the Player does not exist, the pod failed. |
| <span id="status--finishedat"></span>`finishedAt` | string | no | When the operator first read this run's phase as Finished. The time-to-live after finishing counts from here and not from the Play's creation, so the window measures the end of the film. It is written here rather than held in the operator, so an operator that restarts reads the clock back. |
| <span id="status--audiolanguages"></span>`audioLanguages` | []string | no | The resolved audio language order this run applied, the record of what the three tiers settled on. |
| <span id="status--subtitlelanguages"></span>`subtitleLanguages` | []string | no | The resolved subtitle language order this run applied. |
| <span id="status--subtitles"></span>`subtitles` | string | no | The resolved subtitle setting this run applied, one of on, off, or auto. |
| <span id="status--audiolanguage"></span>`audioLanguage` | string | no | The language of the audio track mpv chose, so you can see when a code matched no track. The value is the track's own tag as the file carries it, for Matroska the three-letter ISO 639-2 code, whatever form the preference used. |
| <span id="status--subtitlelanguage"></span>`subtitleLanguage` | string | no | The language of the subtitle track mpv chose; empty when none plays. The value is the track's own tag as the file carries it, the way audioLanguage reports its track. |

## On the bus

The `plays` tree carries one run's commands, its report, and its
availability. See [the media bus](/docs/reference/bus/) for the
rules every topic follows.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | no | one named command |
| `plays/{namespace}/{name}/status` | the playback pod | yes | the run's report |
| `plays/{namespace}/{name}/availability` | the playback pod | yes | `online` or `offline` |

### `commands`

The topic any program publishes to drive the run. A
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

### `availability`

`online` or `offline`, retained. The pod names this topic as its
MQTT Last Will with `offline` as the payload, and publishes `online`
once it connects, so a retained status a killed pod left behind does
not read as a live run.
