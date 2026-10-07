---
title: Plays
weight: 20
toc: true
---

<!-- Generated from deploy/plays-crd.yaml by crdref. Do not edit. -->

# `Play`

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

The operator holds the finalizer `media.liken.sh/bus-topics` on every
`Play`, so a delete completes only after the operator has torn the run
down. Inside that window it deletes the playback pod, waits until the
pod is gone, and clears the run's retained `status` and `availability`
topics on the bus. The finalizer owns the clear because a `Play` must
never be gone from the API server while its topics still stand on the
broker, and because the pod's own closing messages have to land before
the operator's clear or they would overwrite it. A `Play` that stays
deleting for longer than a few seconds has a pod that will not go, and
`kubectl describe` on the pod says why.

The spec is immutable, like a `Job`'s template. A `Play` whose
player or media changed mid-run would describe a different run;
delete the `Play` and create another.

One `Player` runs one `Play`. When two unfinished `Play`s name the
same `Player`, the newest one by creation time, and then by name, is
the one that runs, and the operator deletes every older one. So a
`Play` created while a film plays ends that film. The deleted `Play`
reports its last position before its pod ends, and a `Play` that
resumes it names that position in `spec.start`.

`spec.next` names the work that follows this run. The display offers
it on the scrubber, and when a person takes the offer the program that
wrote the `Play` creates the next one. The `Play` carries the offer
because the display reads no catalog, and the program that wrote it
decides what follows.

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
| <span id="spec--next"></span>`next` | [object](#specnext) | no | The work that follows this run. The display offers it on the scrubber as a chip, and as a card near the end of the run. The card stays up for five seconds, and the OSD shows it again after that. When the item's marks place credits in its second half, the card rises at the start of the credits, or after the scene that follows them, as the marks field describes. Otherwise a film's card rises when three percent of the item or three minutes remain, whichever is less, and an episode's card, for an item whose presentation names a series, rises when one and a half percent of the item or ninety seconds remain, whichever is less. With the OSD down, the risen card shows focused, and select takes it, unless a skip control is up beside it. Then select does what it does for the skip control alone. A select on the offer publishes the request below on the Player's commands topic, where the program that wrote the Play reads it back and creates the next Play. A Play with no next block offers nothing. |
| <span id="spec--trickplayinterval"></span>`trickplayInterval` | string | no | The seconds one trickplay tile covers, as a Go duration like 10s. Jellyfin writes no manifest beside the sheets, so the Play declares it. Omitted, it defaults to 10s, the Jellyfin default. |
| <span id="spec--ttlsecondsafterfinished"></span>`ttlSecondsAfterFinished` | integer | no | How long this Play remains after it finishes, in seconds, the meaning a Job gives the name. While it remains, kubectl get plays still shows what just played and where it stopped; deleting the Play deletes that record. Omitted, it is 300 seconds. Zero deletes the Play as soon as it finishes. The playback pod does not wait for this window: it is deleted as soon as the run finishes. |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | A per-Play override of the audio language order. Omit it to use the Player's. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | A per-Play override of the subtitle language order. Omit it to use the Player's. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | A per-Play override of when subtitles show. Omit it to use the Player's. One of: `on`, `off`, `auto`. |

### spec.items[]

One entry in the playlist: the URI to play and, optionally, how it should look.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitems--uri"></span>`uri` | string | yes | The operator resolves https:// to a stream the player reads directly, and nfs://host/export/path and claim://namespace/claim/path to a mount on the playback pod. A claim URI names the Play's own namespace, because a pod mounts only a claim in its own namespace. pattern://white, pattern://bars, and pattern://speakers play a test pattern the player image carries, at the frame the URI names, such as pattern://bars/1920x1080, or at the frame of the Player's screen when it names none. A URI whose scheme the operator does not know, a claim URI that names no namespace or another namespace, or a pattern or frame the image does not carry, fails the Play before any pod exists. |
| <span id="specitems--presentation"></span>`presentation` | [object](#specitemspresentation) | no | How the item should look, for the fields the display cannot read from the file. The library that fed the item supplies these, and the display prefers them over the container's tags. Omit the block for a loose file, and the display falls back to the file's own tags. |

#### spec.items[].presentation

How the item should look, for the fields the display cannot read from the file. The library that fed the item supplies these, and the display prefers them over the container's tags. Omit the block for a loose file, and the display falls back to the file's own tags.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentation--type"></span>`type` | string | no | The media type the display tunes its layout by. mpv cannot infer this, and the display does not read it from the file name. One of: `video`, `music`, `image`. |
| <span id="specitemspresentation--hint"></span>`hint` | string | no | The finer kind within the type. A video is a movie or a series, and music is an album. It selects the layout the display draws. An album also declares that the item's URI names a directory, which the playback pod expands into one timeline of the audio files it holds. The directory must hold at least one audio file, or the run fails. One of: `movie`, `series`, `album`. |
| <span id="specitemspresentation--role"></span>`role` | string | no | What the item is, when it is not the work itself. The only value is trailer, and the display marks a trailer on the line under the title. Omit it for the work itself. One of: `trailer`. |
| <span id="specitemspresentation--title"></span>`title` | string | no | The item's name, which overrides the file's own tag. Set it when the tag is wrong or absent. |
| <span id="specitemspresentation--series"></span>`series` | string | no | The series this episode belongs to. |
| <span id="specitemspresentation--season"></span>`season` | integer | no | The season number of the episode. |
| <span id="specitemspresentation--episode"></span>`episode` | integer | no | The episode number within its season. |
| <span id="specitemspresentation--episodetitle"></span>`episodeTitle` | string | no | The title of the episode. |
| <span id="specitemspresentation--year"></span>`year` | integer | no | The release year, shown under a movie's title. |
| <span id="specitemspresentation--date"></span>`date` | string | no | The air date of an episode, shown on its line. Give it as an ISO date like 2017-03-05, and the display formats it. |
| <span id="specitemspresentation--artist"></span>`artist` | string | no | The artist of a music item, shown in the header under the title. For an album this field is the one source; a standalone track can also leave it unset and let its own tags supply it. |
| <span id="specitemspresentation--album"></span>`album` | string | no | The record a music item belongs to, shown in the header beside the year. It fills in the same way the artist does. |
| <span id="specitemspresentation--art"></span>`art` | string | no | The cover art URI, claim://, nfs:// or https://, resolved the way the media URI is. It is the first place the cover is looked for; a picture embedded in the file and a cover.jpg beside it follow, and the pod reads both of those itself. |
| <span id="specitemspresentation--logo"></span>`logo` | string | no | The logo art URI, claim://, nfs:// or https://, resolved the way the media URI is. The display shows it in the header in place of the title. |
| <span id="specitemspresentation--trickplay"></span>`trickplay` | string | no | The X.trickplay directory URI, claim://, nfs:// or https://, resolved the way the media URI is. The display shows a tile from it on the scrub cursor. |
| <span id="specitemspresentation--appearances"></span>`appearances` | string | no | The URI of the item's spans file, claim://, nfs:// or https://, resolved the way the media URI is. library-operator writes it as X.spans.json in .liken/appearances/ beside the video. When the film pauses, the display shows the credited people on screen at the playhead. |
| <span id="specitemspresentation--contributors"></span>`contributors` | string | no | The URI of the library's .contributors directory, claim://, nfs:// or https://, resolved the way the media URI is. The spans file names each portrait by its path under it. |
| <span id="specitemspresentation--marks"></span>`marks` | [\[\]object](#specitemspresentationmarks) | no | The spans in the file where the intro, the recap, the credits, the scene after the credits, and the preview are. A database can give several candidate spans for one kind, and the list carries every one. The display merges the candidates that overlap into one span, from the median start and the median end. It offers a skip control while the playhead is inside an intro or a recap. With the OSD down, the control shows focused, and select skips. The credits count when they start in the second half of the item. A scene after them is a post-credits span, or the gap between two credits spans. Inside the credits before a scene, the display offers a skip to the scene, and select takes it only while the OSD is up. The up-next card rises at the start of the credits, or, when a scene follows them, at the later of the start of the last credits span and the end of the last scene. A card after a scene never rises later than it would with no marks. An item with no marks plays with no skip control, and its card rises by the time that remains, as the next field describes. |

#### spec.items[].presentation.marks[]

The spans in the file where the intro, the recap, the credits, the scene after the credits, and the preview are. A database can give several candidate spans for one kind, and the list carries every one. The display merges the candidates that overlap into one span, from the median start and the median end. It offers a skip control while the playhead is inside an intro or a recap. With the OSD down, the control shows focused, and select skips. The credits count when they start in the second half of the item. A scene after them is a post-credits span, or the gap between two credits spans. Inside the credits before a scene, the display offers a skip to the scene, and select takes it only while the OSD is up. The up-next card rises at the start of the credits, or, when a scene follows them, at the later of the start of the last credits span and the end of the last scene. A card after a scene never rises later than it would with no marks. An item with no marks plays with no skip control, and its card rises by the time that remains, as the next field describes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationmarks--kind"></span>`kind` | string | yes | What the span holds: intro, recap, credits, post-credits, or preview. post-credits is the scene after the credits. The display ignores a kind it does not know. |
| <span id="specitemspresentationmarks--start"></span>`start` | number | no | Where the span starts, in seconds from the start of the file. Omit it for a span that starts with the file. |
| <span id="specitemspresentationmarks--end"></span>`end` | number | no | Where the span ends, in seconds from the start of the file. Omit it for a span that ends with the file. |
| <span id="specitemspresentationmarks--source"></span>`source` | string | no | The database the span came from, such as theintrodb. The display does not read it. |

### spec.next

The work that follows this run. The display offers it on the scrubber as a chip, and as a card near the end of the run. The card stays up for five seconds, and the OSD shows it again after that. When the item's marks place credits in its second half, the card rises at the start of the credits, or after the scene that follows them, as the marks field describes. Otherwise a film's card rises when three percent of the item or three minutes remain, whichever is less, and an episode's card, for an item whose presentation names a series, rises when one and a half percent of the item or ninety seconds remain, whichever is less. With the OSD down, the risen card shows focused, and select takes it, unless a skip control is up beside it. Then select does what it does for the skip control alone. A select on the offer publishes the request below on the Player's commands topic, where the program that wrote the Play reads it back and creates the next Play. A Play with no next block offers nothing.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specnext--reason"></span>`reason` | string | no | The first line of the card, which says why this work follows: "Next in The Saga", for one. The display draws it as given, in uppercase. |
| <span id="specnext--title"></span>`title` | string | no | The second line of the card, the name of the work that follows. The chip draws this line alone. |
| <span id="specnext--detail"></span>`detail` | string | no | The third line of the card, under the title: the series and the season, or the year and the runtime. The display draws it as given. |
| <span id="specnext--art"></span>`art` | string | no | The art of the work that follows, as a claim://, nfs://, or https:// URI, resolved the way an item's art is. The display fits it inside the card's box and keeps its ratio, so a poster is letterboxed. |
| <span id="specnext--request"></span>`request` | object | no | An object the operator never reads. When a person takes the offer, it is published byte for byte on the Player's commands topic, so the program that wrote the Play gets its own words back and needs no other record of what it offered. |

## status

What the playback pod reports, written only by the media operator. The playback pod itself holds no API credentials; it reports to the operator, and the operator writes here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | Where the run is in its life, in the words Jobs and Pods use. Pending is declared but not yet performing, Running is the pod performing the play, paused or not, and Finished and Failed are the two ends. The word is Running rather than Playing because a phase moves forward only, and a phase of Playing would switch back and forth each time a film paused. The paused field reports the pause. One of: `Pending`, `Running`, `Finished`, `Failed`. |
| <span id="status--activity"></span>`activity` | string | no | The one word for what the Play does right now, the phase and the paused flag folded together. Starting is Pending, Playing and Paused both mean Running, and Finished and Failed match the phase. The phase is the lifecycle; the activity is what a person reads at a glance. One of: `Starting`, `Playing`, `Paused`, `Finished`, `Failed`. |
| <span id="status--paused"></span>`paused` | boolean | no | True while the player holds the current item still. The phase stays Running, because a pause does not advance the lifecycle. |
| <span id="status--item"></span>`item` | integer | no | Which URI plays now, counting from 1 in spec order. The third of five episodes shows 3. |
| <span id="status--position"></span>`position` | string | no | The playhead inside the current item, as H:MM:SS. |
| <span id="status--duration"></span>`duration` | string | no | The length of the current item, as H:MM:SS, once the player has read it. |
| <span id="status--pod"></span>`pod` | string | no | The playback pod's name, for kubectl describe and logs. The pod is owned by this Play and is deleted with it. |
| <span id="status--message"></span>`message` | string | no | The reason for the phase, as one line of text: the resolver refused a URI, the Player does not exist, the pod failed. |
| <span id="status--finishedat"></span>`finishedAt` | string | no | When the operator first read this run's phase as Finished. The time-to-live after finishing counts from here and not from the Play's creation, so the window measures the end of the film. It is written here rather than held in the operator, so an operator that restarts reads the clock back. |
| <span id="status--audiolanguages"></span>`audioLanguages` | []string | no | The audio language order this run applied, after the operator combined the Play, the Player, and MediaPreferences. |
| <span id="status--subtitlelanguages"></span>`subtitleLanguages` | []string | no | The resolved subtitle language order this run applied. |
| <span id="status--subtitles"></span>`subtitles` | string | no | The resolved subtitle setting this run applied, one of on, off, or auto. |
| <span id="status--audiolanguage"></span>`audioLanguage` | string | no | The language of the audio track mpv chose, so you can see when a code matched no track. The value is the track's own tag as the file contains it, for Matroska the three-letter ISO 639-2 code, whatever form the preference used. |
| <span id="status--subtitlelanguage"></span>`subtitleLanguage` | string | no | The language of the subtitle track mpv chose; empty when none plays. The value is the track's own tag as the file contains it, the way audioLanguage reports its track. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The run's conditions. There is one, DisplayAlive, and it reports the playback pod's display container, the native sidecar that draws the on-screen display. It is True with reason Running while the container runs, at any restart count, and False with reason Restarting only while the container is down after an exit the kubelet recorded. The message states the restart count with the reason and exit code of the last termination, and a display that has never restarted has no message. More than two restarts in one run set the phase to Finished with the same message, and the run retires the way a finished film does. The condition is absent while the pod reports no display container or the container has not started yet. lastTransitionTime moves only when the status does. |

### status.conditions[]

The run's conditions. There is one, DisplayAlive, and it reports the playback pod's display container, the native sidecar that draws the on-screen display. It is True with reason Running while the container runs, at any restart count, and False with reason Restarting only while the container is down after an exit the kubelet recorded. The message states the restart count with the reason and exit code of the last termination, and a display that has never restarted has no message. More than two restarts in one run set the phase to Finished with the same message, and the run retires the way a finished film does. The condition is absent while the pod reports no display container or the container has not started yet. lastTransitionTime moves only when the status does.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's name, DisplayAlive. |
| <span id="statusconditions--status"></span>`status` | string | yes | The condition's status. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | One word for the status: Running or Restarting. |
| <span id="statusconditions--message"></span>`message` | string | no | The restart count, with the reason and exit code of the last termination the kubelet recorded. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed, so a reader can tell how long the display has been down or up. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The Play's metadata.generation the condition was derived from, so a reader can tell a condition on the current spec from a stale one. |

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
