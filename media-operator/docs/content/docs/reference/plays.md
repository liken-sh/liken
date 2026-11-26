---
title: Plays
weight: 20
toc: true
---

<!-- Generated from deploy/plays-crd.yaml by crdref. Do not edit. -->

# `Play`

A `Play` is one run of media on a [Player](/docs/reference/players/):
a film, an album, or a season of episodes, played in order. It works
like a `Job`: it runs once to completion, and then it stays, so that
you can read its status, until `ttlSecondsAfterFinished` passes or you
delete it. Create a `Play` to start it, and delete it to stop it early.
`kubectl get plays` shows the status, item, and position of each run,
finished runs included.

The operator creates a playback pod for a `Play`, and the claims that
the pod needs. The `Play` owns the pod and the claims, so the
Kubernetes garbage collector deletes them with the `Play`. A
`Finished` `Play` has no pod running.

The operator adds the finalizer `media.liken.sh/bus-topics` to every
`Play`, so a deleted `Play` stays in the API server until the operator
has stopped the run. The operator deletes the playback pod, waits
until the pod is gone, and then clears the run's retained `status` and
`availability` topics on the bus. It waits because the pod publishes
closing messages as it ends, and those would overwrite an earlier
clear. When a `Play` stays in deletion for more than a few seconds,
its pod does not stop. Run `kubectl describe` on the pod to see why.

The spec is immutable, like the template of a `Job`, because a changed
`Play` would no longer describe the run that is playing. To change the
player or the media, delete the `Play` and create another.

A `Player` runs one `Play` at a time. When two unfinished `Play`
objects name the same `Player`, the newest one runs, by creation time
and then by name, and the operator deletes every older one. So a
`Play` that you create while a film plays ends that film. The deleted
`Play` reports its last position before its pod ends. To resume from
that position, create a `Play` with the position in `spec.start`.

`spec.next` describes the work that follows this run, and the
on-screen display offers it on the scrubber. When a person accepts the
offer, the program that wrote the `Play` creates the next one. The
offer is in the `Play` because the program that created the `Play`
decides what follows, and the on-screen display reads no catalog.

    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: nosferatu
      namespace: media
    spec:
      players: [studio]
      items:
        - uri: nfs://nas/media/movies/Nosferatu (1922)/Nosferatu.mkv
          presentation:
            type: video
            hint: movie
            title: Nosferatu
            year: 1922
      start: "0:10:00"

A Play is one run of media on a Player: a film, an album, or a season of episodes, played in order. Create a Play to start it. Delete the Play to stop it early.

## spec

What to play and where. The spec is immutable. To play something else, or on another Player, create another Play.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--players"></span>`players` | []string | yes | The name of the `Player` in the Play's namespace that this Play runs on. The list has exactly one entry. |
| <span id="spec--items"></span>`items` | [\[\]object](#specitems) | yes | The media to play, in order. |
| <span id="spec--start"></span>`start` | string | no | Where the first item starts, as a time that mpv accepts, such as `0:10:00` or `600`. When omitted, the first item starts at its beginning. Later items always start at their beginning. To resume, create a Play with the `status.position` that the earlier Play reported. |
| <span id="spec--next"></span>`next` | [object](#specnext) | no | The work to offer after this run, such as the next episode. The display shows it as a chip on the scrubber and as a card near the end of the run, and a Play with no `next` offers nothing. When a person selects the offer, the display publishes `request` on the Player's commands topic, and the program that wrote the Play reads it there and creates the next Play. The card appears at the start of the credits, or after the scene that follows them, when the item's `marks` put the credits in its second half. Otherwise it appears when 4% of a film or 4 minutes remain, or 2.5% of an episode or 2 minutes remain, whichever is less. An episode is an item whose presentation sets `series`. The card never appears before the point where the work counts as watched, 5% or 5 minutes before the end, whichever is less. It hides after 5 seconds, and a press that shows the OSD shows it again. With the OSD hidden, select takes the card, unless a skip control is also showing. When the compositor supports the `liken_arrangement_v1` protocol, the film moves into the left half of the screen when its card appears, and the display shows the next work in the right half until a press or the end of the file. Back with the OSD hidden returns the film to the whole screen, and a second back ends the run. In either layout, select fades the offer out and pauses the film, so its sound stops at the press. |
| <span id="spec--trickplayinterval"></span>`trickplayInterval` | string | no | How many seconds one trickplay tile covers, as a Go duration such as `10s`. Jellyfin writes no manifest beside the sheets, so the Play declares it. When omitted, it is `10s`, the Jellyfin default. |
| <span id="spec--ttlsecondsafterfinished"></span>`ttlSecondsAfterFinished` | integer | no | How many seconds the Play remains after it finishes, as for a Job, so `kubectl get plays` shows what just played and where it stopped. The default is 300, and 0 deletes the Play as soon as it finishes. The operator deletes the playback pod when the run finishes, whatever this value is. |
| <span id="spec--audiolanguages"></span>`audioLanguages` | []string | no | Overrides the Player's audio language order for this Play. Omit it to use the Player's. |
| <span id="spec--subtitlelanguages"></span>`subtitleLanguages` | []string | no | Overrides the Player's subtitle language order for this Play. Omit it to use the Player's. |
| <span id="spec--subtitles"></span>`subtitles` | string | no | Overrides the Player's setting for when subtitles show. Omit it to use the Player's. One of: `on`, `off`, `auto`. |
| <span id="spec--sdh"></span>`sdh` | string | no | Overrides the Player's setting for whether an SDH track ranks ahead of a full track in the same language. Omit it to use the Player's. One of: `prefer`, `avoid`. |

### spec.items[]

One entry of the playlist: the URI to play and, optionally, how the display shows it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitems--uri"></span>`uri` | string | yes | Where the media is. mpv streams an `https://` URI directly. It checks the certificate against the CA bundle in its image, so it refuses a certificate that is expired, self-signed, or for another host. The playback pod mounts `nfs://host/export/path` and `claim://namespace/claim/path`. A `claim://` URI must name the Play's own namespace, because a pod can mount a claim only in its own namespace. `pattern://white`, `pattern://bars`, and `pattern://speakers` play a test pattern from the player image, at the frame size in the URI, such as `pattern://bars/1920x1080`, or else at the frame size of the Player's screen. The Play fails before any pod starts when the scheme is unknown, when a `claim://` URI names no namespace or another namespace, or when the image has no such pattern or frame size. |
| <span id="specitems--presentation"></span>`presentation` | [object](#specitemspresentation) | no | What the display shows about the item. A library that supplies the item sets these fields, and the display uses them in place of the file's own tags. Omit the block to show only the file's tags. |

#### spec.items[].presentation

What the display shows about the item. A library that supplies the item sets these fields, and the display uses them in place of the file's own tags. Omit the block to show only the file's tags.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentation--type"></span>`type` | string | no | The media type, which selects the layout of the display. The display does not guess it from the file. One of: `video`, `music`, `image`. |
| <span id="specitemspresentation--hint"></span>`hint` | string | no | The kind of video or music, which also selects the layout: `movie` or `series` for video, and `album` for music. With `album`, the item's URI is a directory, and the playback pod plays its audio files as one timeline. The run fails when the directory has no audio file. One of: `movie`, `series`, `album`. |
| <span id="specitemspresentation--role"></span>`role` | string | no | Set `trailer` when the item is a trailer and not the work itself. The display then says so on the line under the title. One of: `trailer`. |
| <span id="specitemspresentation--title"></span>`title` | string | no | The item's title, in place of the file's title tag. |
| <span id="specitemspresentation--series"></span>`series` | string | no | The series this episode belongs to. |
| <span id="specitemspresentation--season"></span>`season` | integer | no | The season number of the episode. |
| <span id="specitemspresentation--episode"></span>`episode` | integer | no | The episode number within its season. |
| <span id="specitemspresentation--episodetitle"></span>`episodeTitle` | string | no | The title of the episode. |
| <span id="specitemspresentation--year"></span>`year` | integer | no | The release year. The display shows it under a movie's title, beside the length of the film. |
| <span id="specitemspresentation--date"></span>`date` | string | no | The air date of an episode, as an ISO date such as `2017-03-05`. The display formats it for the screen. |
| <span id="specitemspresentation--artist"></span>`artist` | string | no | The artist of a music item, shown under the title. When it is unset, the display shows the artist tag that mpv reads from the file. Set it for an album, which mpv plays as a timeline of its files and not as one tagged file. |
| <span id="specitemspresentation--album"></span>`album` | string | no | The album of a music item, shown beside the year. It falls back to the file's tag as `artist` does. |
| <span id="specitemspresentation--art"></span>`art` | string | no | The URI of the cover art, as `claim://`, `nfs://`, or `https://`, resolved like `uri`. When it is unset, the pod uses a picture embedded in the file, or else a `cover.jpg` beside it. |
| <span id="specitemspresentation--logo"></span>`logo` | string | no | The URI of the logo art, as `claim://`, `nfs://`, or `https://`, resolved like `uri`. The display shows it in the header in place of the title. |
| <span id="specitemspresentation--trickplay"></span>`trickplay` | string | no | The URI of the `X.trickplay` directory, as `claim://`, `nfs://`, or `https://`, resolved like `uri`. While you scrub, the display shows the tile for the position under the cursor. |
| <span id="specitemspresentation--appearances"></span>`appearances` | string | no | The URI of the file that lists when each credited person is on screen, as `claim://`, `nfs://`, or `https://`, resolved like `uri`. library-operator writes it as `X.spans.json` in `.liken/appearances/` beside the video. When the film pauses, the display shows the people on screen at that moment. |
| <span id="specitemspresentation--contributors"></span>`contributors` | string | no | The URI of the library's `.contributors` directory, as `claim://`, `nfs://`, or `https://`, resolved like `uri`. The `appearances` file names each person's portrait by its path under this directory. |
| <span id="specitemspresentation--marks"></span>`marks` | [\[\]object](#specitemspresentationmarks) | no | Where the intro, the recap, the credits, a scene after the credits, and a preview are in the file. The list can hold several spans of one kind, and the display merges spans that overlap into one, from their median start and median end. While the playhead is in an intro or a recap, the display shows a skip control. It shows over the bare video for the first 10 seconds of the span, and select there skips. After that it shows only with the OSD, which up or down opens with the control focused. Credits count only when they start in the second half of the item. A scene after the credits is a `post-credits` span or the gap between two `credits` spans, and during the credits before it the display offers to skip to the scene. Select takes that offer only while the OSD is visible. The marks also set when the up-next card appears, as the `next` field describes. |
| <span id="specitemspresentation--subtitles"></span>`subtitles` | [\[\]object](#specitemspresentationsubtitles) | no | The number of subtitle events in each subtitle track of the item, which library-operator writes from its probe because mpv does not report it. The command sidecar uses the counts to choose a subtitle track when the file's flags are wrong. It never chooses a track with no events. It treats a track with fewer than a fifth as many events as the largest track in its language as forced, even when no flag or title says so. A track with no entry here is judged by its flags, title, and file name alone. An entry for an image track can also carry `image`, the measurement that the display uses to place the track. |
| <span id="specitemspresentation--audio"></span>`audio` | [\[\]object](#specitemspresentationaudio) | no | The format of each audio stream of the item's file. library-operator's media browser writes it from what library-operator's probe read of the file. mpv reports the profile and the channel layout only of the audio track that it decodes, so the display reads this list to name the format of every other audio track, such as `E-AC3 Atmos 5.1`. A file that the probe has not read gets no list, and the display names each track from what mpv reports. |
| <span id="specitemspresentation--video"></span>`video` | [\[\]object](#specitemspresentationvideo) | no | The frame and the active area of each video stream of the item's file. library-operator's media browser writes it from what library-operator's probe measured of the file. A film stored with its letterbox bars in the frame, such as a film of 2.39:1 in a 1920x1080 frame, has black rows that mpv draws as picture. The display adds the black rows below the active area of the selected video track to the screen's lower bar, and puts a subtitle cue of two or more lines in that bar when the bar can hold it. A file that the probe has not read gets no list, and the display uses the bar that mpv reports. |

#### spec.items[].presentation.marks[]

Where the intro, the recap, the credits, a scene after the credits, and a preview are in the file. The list can hold several spans of one kind, and the display merges spans that overlap into one, from their median start and median end. While the playhead is in an intro or a recap, the display shows a skip control. It shows over the bare video for the first 10 seconds of the span, and select there skips. After that it shows only with the OSD, which up or down opens with the control focused. Credits count only when they start in the second half of the item. A scene after the credits is a `post-credits` span or the gap between two `credits` spans, and during the credits before it the display offers to skip to the scene. Select takes that offer only while the OSD is visible. The marks also set when the up-next card appears, as the `next` field describes.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationmarks--kind"></span>`kind` | string | yes | What the span is: `intro`, `recap`, `credits`, `post-credits` (the scene after the credits), or `preview`. The display ignores any other kind. |
| <span id="specitemspresentationmarks--start"></span>`start` | number | no | Where the span starts, in seconds from the start of the file. Omit it for a span that starts with the file. |
| <span id="specitemspresentationmarks--end"></span>`end` | number | no | Where the span ends, in seconds from the start of the file. Omit it for a span that ends with the file. |
| <span id="specitemspresentationmarks--source"></span>`source` | string | no | The database that the span came from, such as `theintrodb`. The display does not read it. |

#### spec.items[].presentation.subtitles[]

What the library measured of one track. Set exactly one of `stream` and `file` to identify the track.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationsubtitles--stream"></span>`stream` | integer | no | The index of an embedded track in its container: the stream index from ffprobe, which is mpv's `ff-index`. |
| <span id="specitemspresentationsubtitles--file"></span>`file` | string | no | The base name of an external subtitle file beside the media, such as `Movie.en.hi.srt`, matched against the track's file name as written. |
| <span id="specitemspresentationsubtitles--events"></span>`events` | integer | no | The number of subtitle events in the track. Omit it when the count is unknown, which is not the same as 0. |
| <span id="specitemspresentationsubtitles--image"></span>`image` | [object](#specitemspresentationsubtitlesimage) | no | Where an image track draws its text and how tall the text is, which library-operator measures from a sample of the track's cues. The display uses it to move the track's last line to the same row as a text track's and to shrink the line to the same height. Set it only for an image track, such as a PGS track of a Blu-ray or a VobSub track of a DVD. Omit it for a track that is not measured, and the display draws the track where the disc placed it. Every number is in rows of the track's own canvas. |

#### spec.items[].presentation.subtitles[].image

Where an image track draws its text and how tall the text is, which library-operator measures from a sample of the track's cues. The display uses it to move the track's last line to the same row as a text track's and to shrink the line to the same height. Set it only for an image track, such as a PGS track of a Blu-ray or a VobSub track of a DVD. Omit it for a track that is not measured, and the display draws the track where the disc placed it. Every number is in rows of the track's own canvas.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationsubtitlesimage--canvasheight"></span>`canvasHeight` | integer | yes | The height of the track's canvas, such as `1080` for a Blu-ray. |
| <span id="specitemspresentationsubtitlesimage--lineheight"></span>`lineHeight` | integer | yes | The height of one line of the track's text, from the top of its tallest letter to the bottom of its lowest. |
| <span id="specitemspresentationsubtitlesimage--inkbottom"></span>`inkBottom` | integer | yes | The row just below the last line of a usual cue at the bottom of the canvas. |

#### spec.items[].presentation.audio[]

The format of one audio stream, in ffprobe's own values.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationaudio--stream"></span>`stream` | integer | yes | The index of the stream in its container: the stream index from ffprobe, which is mpv's `ff-index`. |
| <span id="specitemspresentationaudio--codec"></span>`codec` | string | no | ffprobe's codec name, such as `eac3`, `truehd`, or `aac`. |
| <span id="specitemspresentationaudio--profile"></span>`profile` | string | no | ffprobe's profile, such as `Dolby Digital Plus + Dolby Atmos` or `DTS-HD MA`. Omit it when ffprobe reports none. |
| <span id="specitemspresentationaudio--channels"></span>`channels` | integer | no | The number of channels. |
| <span id="specitemspresentationaudio--layout"></span>`layout` | string | no | ffprobe's channel layout, such as `5.1(side)` or `stereo`. Omit it when ffprobe reports none. |

#### spec.items[].presentation.video[]

One video stream and the rectangle of its frame that contains picture.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationvideo--stream"></span>`stream` | integer | yes | The index of the stream in its container: the stream index from ffprobe, which is mpv's `ff-index`. |
| <span id="specitemspresentationvideo--width"></span>`width` | integer | no | The width of the stream's frame, in pixels. |
| <span id="specitemspresentationvideo--height"></span>`height` | integer | no | The height of the stream's frame, in pixels. The display needs it to use `activeArea`. |
| <span id="specitemspresentationvideo--activearea"></span>`activeArea` | [object](#specitemspresentationvideoactivearea) | no | The rectangle of the frame that contains picture, in pixels of the frame. Each pixel outside it is black on every frame that library-operator sampled. Omit it when the stream is not measured, and the display uses the bar that mpv reports. |

#### spec.items[].presentation.video[].activeArea

The rectangle of the frame that contains picture, in pixels of the frame. Each pixel outside it is black on every frame that library-operator sampled. Omit it when the stream is not measured, and the display uses the bar that mpv reports.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specitemspresentationvideoactivearea--left"></span>`left` | integer | yes | The first column of the area. |
| <span id="specitemspresentationvideoactivearea--top"></span>`top` | integer | yes | The first row of the area. |
| <span id="specitemspresentationvideoactivearea--width"></span>`width` | integer | yes | The width of the area, in pixels. |
| <span id="specitemspresentationvideoactivearea--height"></span>`height` | integer | yes | The height of the area, in pixels. |

### spec.next

The work to offer after this run, such as the next episode. The display shows it as a chip on the scrubber and as a card near the end of the run, and a Play with no `next` offers nothing. When a person selects the offer, the display publishes `request` on the Player's commands topic, and the program that wrote the Play reads it there and creates the next Play. The card appears at the start of the credits, or after the scene that follows them, when the item's `marks` put the credits in its second half. Otherwise it appears when 4% of a film or 4 minutes remain, or 2.5% of an episode or 2 minutes remain, whichever is less. An episode is an item whose presentation sets `series`. The card never appears before the point where the work counts as watched, 5% or 5 minutes before the end, whichever is less. It hides after 5 seconds, and a press that shows the OSD shows it again. With the OSD hidden, select takes the card, unless a skip control is also showing. When the compositor supports the `liken_arrangement_v1` protocol, the film moves into the left half of the screen when its card appears, and the display shows the next work in the right half until a press or the end of the file. Back with the OSD hidden returns the film to the whole screen, and a second back ends the run. In either layout, select fades the offer out and pauses the film, so its sound stops at the press.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specnext--reason"></span>`reason` | string | no | The first line of the card: why this work follows, such as "Next in The Saga". The display draws it in uppercase. |
| <span id="specnext--title"></span>`title` | string | no | The second line of the card: the name of the work that follows. The chip shows only this line. |
| <span id="specnext--detail"></span>`detail` | string | no | The third line of the card, under the title: the series and the season, or the year and the runtime. The display draws it as given. |
| <span id="specnext--remainingseconds"></span>`remainingSeconds` | integer | no | How long the work that follows will run, in seconds, from where it would start: the length of its file, minus the position where the viewer stopped in it before. The offer shows a line under the detail, such as "46m · ends at 10:42 pm", with the time of day the work would end if it started now. Under 60 seconds, the line shows only the end time. When omitted, the offer shows no such line. |
| <span id="specnext--art"></span>`art` | string | no | The art of the work that follows, as a `claim://`, `nfs://`, or `https://` URI, resolved like an item's art. The display fits it inside the card's box and keeps its aspect ratio, so a poster is letterboxed. |
| <span id="specnext--request"></span>`request` | object | no | Any object. The operator does not read it. When a person selects the offer, the display publishes it unchanged on the Player's commands topic, so the program that wrote the Play gets back what it offered and needs no record of its own. |

## status

What the playback pod is doing. media-operator writes this from what the pod reports to it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--phase"></span>`phase` | string | no | Where the run is in its lifecycle. `Pending` means not yet playing, `Running` means playing or paused, and `Finished` and `Failed` are the ends. The phase only moves forward, so a pause shows in `paused` and `activity`, not here. One of: `Pending`, `Running`, `Finished`, `Failed`. |
| <span id="status--activity"></span>`activity` | string | no | What the Play is doing now, for reading at a glance. `Starting` is the `Pending` phase, `Playing` and `Paused` are the `Running` phase, and `Finished` and `Failed` match the phase. One of: `Starting`, `Playing`, `Paused`, `Finished`, `Failed`. |
| <span id="status--paused"></span>`paused` | boolean | no | True while the current item is paused. |
| <span id="status--item"></span>`item` | integer | no | The position in `spec.items` of the item that plays now, counting from 1. |
| <span id="status--position"></span>`position` | string | no | The playhead in the current item, as H:MM:SS. |
| <span id="status--duration"></span>`duration` | string | no | The length of the current item, as H:MM:SS, once mpv has read it. |
| <span id="status--pod"></span>`pod` | string | no | The name of the playback pod, for `kubectl describe` and `kubectl logs`. |
| <span id="status--message"></span>`message` | string | no | Why the Play is in its phase, such as a URI that the operator refused, a `Player` that does not exist, or a pod that failed. |
| <span id="status--finishedat"></span>`finishedAt` | string | no | When the operator first read the run's phase as `Finished`. `ttlSecondsAfterFinished` counts from this time, and a restarted operator reads it back from here. |
| <span id="status--audiolanguages"></span>`audioLanguages` | []string | no | The audio language order that this run used, after the operator combined the Play, the Player, and MediaPreferences. |
| <span id="status--subtitlelanguages"></span>`subtitleLanguages` | []string | no | The subtitle language order that this run used, after the same combination. |
| <span id="status--subtitles"></span>`subtitles` | string | no | The subtitle setting that this run used: `on`, `off`, or `auto`. |
| <span id="status--sdh"></span>`sdh` | string | no | The SDH preference that this run used, `prefer` or `avoid`. Empty when no Play, Player, or MediaPreferences sets one. |
| <span id="status--audiolanguage"></span>`audioLanguage` | string | no | The language tag of the audio track that mpv chose, as the file has it, such as the ISO 639-2 code `eng` in a Matroska file. Compare it with `audioLanguages` to see whether a preference matched a track. |
| <span id="status--subtitlelanguage"></span>`subtitleLanguage` | string | no | The language tag of the subtitle track that mpv chose, as the file has it. Empty when no subtitles show. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | The run's conditions. `DisplayAlive` reports the playback pod's display container, the sidecar that draws the on-screen display. It is `True` with reason `Running` while the container runs, and `False` with reason `Restarting` while the container is down after an exit. After the first restart, the message gives the restart count and the reason and exit code of the last exit. After more than two restarts in one run, the operator sets the phase to `Finished` with the same message. The condition is absent until the display container starts. |

### status.conditions[]

The run's conditions. `DisplayAlive` reports the playback pod's display container, the sidecar that draws the on-screen display. It is `True` with reason `Running` while the container runs, and `False` with reason `Restarting` while the container is down after an exit. After the first restart, the message gives the restart count and the reason and exit code of the last exit. After more than two restarts in one run, the operator sets the phase to `Finished` with the same message. The condition is absent until the display container starts.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes | The condition's type, `DisplayAlive`. |
| <span id="statusconditions--status"></span>`status` | string | yes | The condition's status. One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | no | `Running` or `Restarting`. |
| <span id="statusconditions--message"></span>`message` | string | no | The restart count, with the reason and exit code of the last termination that the kubelet recorded. |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | no | When the status last changed between `True` and `False`. |
| <span id="statusconditions--observedgeneration"></span>`observedGeneration` | integer | no | The `metadata.generation` of the Play when the operator set the condition. |

## Test patterns

A test pattern is a video with a known picture. Use one to check the
on-screen display or the screen's colors without hunting for a suitable
scene in a film. The pattern files are in the player image, so a
pattern needs no volume and no network. `white` and `bars` are
ten-minute files with a chapter every minute, so the scrubber and the
chapter marks look the same as for a film.

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
| `white` | a white screen, which is the hardest background for the dark backing behind the on-screen display |
| `bars` | the SMPTE HD color bars, which show a color error or a range error |
| `speakers` | pink noise from each speaker of a 7.1 layout in turn, with the speaker's name on the screen |

`speakers` is 40 seconds long, at one frame size, `1280x720`. It plays
pink noise for 5 seconds from each speaker in this order: front left,
center, front right, side right, back right, back left, side left, and
the subwoofer, whose noise stops at 120 Hz. Each speaker is a chapter,
so a chapter skip moves to the next speaker. On a system with fewer
than eight speakers, the downmix shows which speaker each missing
channel goes to.

To choose a frame size, add it after the pattern, as in
`pattern://bars/1920x1080`. `white` and `bars` come in every frame size
in this table:

| Frame | Shape | What it matches |
|---|---|---|
| `1280x720` | 16:9 | a 720p screen |
| `1920x1080` | 16:9 | a 1080p screen |
| `3840x2160` | 16:9 | a 4K screen |
| `2560x1080` | 21:9 | a wide 1080-row screen |
| `3840x1600` | 2.4:1 | a wide 1600-row screen |
| `1920x804` | 2.39:1 | a scope film, which letterboxes on a 16:9 screen |

When the URI gives no frame size, the operator picks the largest one
that has the same shape as the `Player`'s screen and fits the canvas
size that the screen's `Display` reports. When no frame size has the
screen's shape, or the operator does not know the screen yet, the
pattern plays at `1920x1080` and `mpv` scales it. The operator picks
the frame size when it creates the playback pod, so a later mode change
on the `Display` does not affect a run that is already playing.

## How subtitles look

Every subtitle track ends its last line on the same screen row, at the
same text size, so subtitles do not change place or size from one title
to the next. On a film with black bars, that row is in the lower bar.
On a title that fills the screen, it is over the picture, 34 pixels
above the bottom of a screen that is 720 pixels tall, scaled to the
real height.

The style is the same in every `Play`, whatever the subtitle mode,
because a person can turn subtitles on from the on-screen display. It
is Source Sans 3 at `mpv`'s size 38, in white with a black outline of
1.65 and no shadow. No field of a `Play`, a `Player`, or a
`MediaPreferences` changes it.

How a track takes the style depends on its kind:

- **Plain text**, such as SubRip, takes the whole style from `mpv`.
- **ASS** tracks have their own fonts, sizes, and colors. The display
  makes `mpv` draw the dialogue in the house style, and it moves the
  dialogue to the house row from the script's own bottom margin. Each
  line keeps its side of the screen and its italics, and a sign that
  the script places at a fixed point stays on the thing it labels.
- **Image** tracks, the PGS tracks of a Blu-ray and the VobSub tracks
  of a DVD, are pictures of text, so they keep the disc's font. The
  display moves each cue to the house row and shrinks it so that a
  line is as tall as a line of house text.

A cue of two or more lines of a text track would cross from the house
row into the picture, because it grows up from that row. So the
display gives `mpv` its own copy of the selected text track, in which
each such cue has a bottom margin of its own. The copy is a file in the
playback pod's own scratch volume, which is deleted with the pod. `mpv`
then draws the cue in its place from its first frame. On a film with a
lower bar, the cue is centred in the bar when the bar has room for the
whole cue and 12 rows of black above and below it on a 1080-row
screen. That is the case for two lines on a film of about 2.31:1 or
wider, such as a 2.39:1 film. Otherwise the cue is raised until it ends
just above the bottom of the picture. On a title that fills the screen,
no cue moves. A cue of one line of the copy is drawn on the same rows as
the same cue of the original. The track panel shows the original track,
and the copy has its title and language.

Some films have their bars inside the encoded picture, such as a film of
2.39:1 stored as a 1920x1080 frame. `mpv` draws those black rows as
picture and reports no bar. `library-operator`'s probe measures the
active area of each video once, the rectangle of the frame that
contains picture, and the media browser sends it in
`items[].presentation.video[].activeArea`. The display adds the black
rows below the active area of the selected video track to the bar that
`mpv` reports, and places a cue of two or more lines in that bar as
above. A video with no `activeArea`, which the probe has not measured or
whose samples did not agree, keeps the bar that `mpv` reports. A film
whose shape changes, such as one with IMAX scenes, usually has the whole
frame as its active area, so in its scenes of 2.39:1 a cue of two lines
still crosses into the foot of the picture. When the probe's samples
missed its IMAX scenes, the film has the area of its wider scenes, and
in an IMAX scene a cue of two lines covers the foot of the picture.

The display reads the cues of a SubRip, ASS, or WebVTT track in a
Matroska file through the file's index, of a `mov_text` track in an MP4
file through its sample table, and of a SubRip, ASS, SSA, WebVTT, or
MicroDVD file beside the video by reading the file. Until the read
finishes, which takes a moment at the start of play, the original is
drawn and a cue of two lines crosses into the picture. The display
makes a copy only when it reads every cue of the track, so a copy never
drops a subtitle. A track in another container, a Matroska file whose
index lists no cue of the track, a file with a cue that cannot be read,
and a stream from the network
keep the house row for every cue, and the display logs why. The display counts only the line breaks in a
cue, so a long line that `mpv` wraps counts as one line. An ASS cue that
the script places itself, at the top, in the middle, or at a fixed
point, keeps its place.

An image track needs a measurement to be placed. `library-operator`'s
probe measures each image track once from a sample of its cues. The
media browser sends the result in the track's `image` field in
`items[].presentation.subtitles[]`. A track with no `image` field is
drawn where the disc placed it, at the disc's size, so its last line
ends on a different row from the other tracks. A track has no `image`
field when the probe has not measured the file yet, or when the track
had too few cues to measure. The display moves and shrinks an image
track only on a screen whose shape is 16:9, within 1%. On another
screen it draws the track as the disc placed it.

The display logs one line each time the place of the selected track
changes. The line names the track, its codec, and the four `mpv`
properties that the display set, and for an image track that it did
not move, the reason.

These cases still differ from the house row:

- A cue of two or more lines of an image track ends higher than the
  house row, because `mpv` shrinks each picture around its own centre.
- On a film with bars, a cue of two lines of an image track covers
  the foot of the picture, because the cue is a picture and the display
  cannot give it a margin of its own.
- A cue at the top of the frame has no common row. Each kind of track
  puts it somewhere else.
- A script that places an ASS cue on the left or the right keeps its
  side margin.
- Bars that are part of the encoded picture take a cue of two or more
  lines of a text track only when the item's `presentation.video` has
  the frame's active area.

## Events

The operator posts a Kubernetes `Event` on the `Play` each time its
phase changes, each time it creates the playback pod again, and when it
deletes the `Play`. `kubectl describe play` prints them. The API server
deletes an `Event` one hour after its last write, so after that hour,
read the phase, the conditions, and the operator's log.

| Reason | Type | When |
|---|---|---|
| `PlaybackStarted` | Normal | The phase moved to `Running`. |
| `PlaybackFinished` | Normal | The phase moved to `Finished`. The message gives the item and the position. |
| `PlaybackFailed` | Warning | The phase moved to `Failed` because the playback pod failed. The message is the pod's failure message. |
| `InvalidSpec` | Warning | The phase moved to `Failed` because the `Play` can never run as written: it names no `Player`, an item does not resolve, or a `Remote` that the pod needs does not exist. |
| `PodRecreated` | Warning or Normal | The operator created the playback pod again. The type is Warning when the old pod failed or is gone, and Normal when an edit to the spec changed the pod. The message gives the position where the new pod starts. |
| `ResumeBackoff` | Warning | The playback pod failed again soon after the operator created it again. The message gives the number of failures and the wait before the next attempt. |
| `Restarting`, `Running` | Warning, Normal | The `DisplayAlive` condition changed, with the condition's message. |
| `Superseded` | Normal | The operator deleted the `Play` because a newer `Play` on the same `Player` replaced it. |
| `Retired` | Normal | The operator deleted the `Play` because `ttlSecondsAfterFinished` passed after it finished. |

The operator posts `Superseded` and `Retired` on the `Player` as well,
because `kubectl describe` stops showing the `Play`'s own `Event`s when
the `Play` is deleted.

## On the bus

Each run has a commands topic, a status topic, and an availability
topic. [The media bus](/docs/reference/bus/) gives the rules that every
topic follows and lists the writers and readers of each topic.

| Topic | Writer | Retained | Carries |
|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | no | one named command |
| `plays/{namespace}/{name}/status` | the playback pod | yes | the run's status report |
| `plays/{namespace}/{name}/availability` | the playback pod | yes | `online` or `offline` |

### `commands`

Any program, such as a phone app or a Home Assistant integration, can
publish one JSON command here to control the run. A `Remote` does not
publish here. The playback pod's command sidecar reads the `Remote`'s
events topic itself and maps each key name to one of these commands.

    {"action": "seek", "amount": -30}

`action` is one of the words in the table below. Only `seek` and
`chapter` take `amount`. For `seek` it is in seconds, and for `chapter`
it is in chapters. A negative `amount` moves backward.

| Action | What it does |
|---|---|
| `pause` | switches between paused and playing |
| `play` | starts playing, and does nothing when the film is already playing |
| `hold` | pauses, and does nothing when the film is already paused |
| `stop` | ends the run, the same as exiting the display |
| `seek` | moves the playback position by `amount` seconds |
| `chapter` | moves by `amount` chapters |
| `subtitles` | switches to the next subtitle track |
| `audio` | switches to the next audio track |
| `info` | shows the file name and the position for a few seconds |
| `up`, `down`, `left`, `right`, `select`, `back` | navigate the on-screen display |
| `home` | asks the `Player`'s client to show its home page, then ends the run |

When an item sets an `appearances` file and the on-screen display is
showing, whether the film is playing or paused, the display draws a row
of cards above the skip control and the up-next chip. There is one card
for each credited person in the scene. `up`, `left`, and `right` move
the focus along the row, and `select` on a card pauses or resumes the
film.

When an item's `marks` put the playback position inside an intro, a
recap, or the credits before a post-credits scene, the display shows a
skip control, and `select` skips. An intro or recap control shows on
its own over the film only for the first 10 seconds of its span. For
the rest of the span it shows with the on-screen display: `up` or
`down` shows the display with the control focused, and the control
hides when the display hides. Skipping to a post-credits scene works
only while the on-screen display is showing. The display never skips
by itself.

The strip at the bottom of the on-screen display names the audio and
the subtitles that play, and their delay when one is set, as in
"English SDH · 0.7 s sooner". `left` and `right` move between its
stops, and `select` opens the stop's track panel on the center line of
the screen. Each row names a track: the language, written once for
each language, then for a subtitle track its kind, *full*, *SDH*,
*forced*, or *commentary*, then its title and its format. A dot marks
the track that plays. In the panel, `up` and
`down` move through the tracks, and `select` plays the focused track.
`select` on the track that plays closes the panel.

The row above the tracks sets the timing. `left` shows the track
sooner and `right` shows it later, from any row of the panel, by
0.05 s for audio and 0.1 s for subtitles, up to 5 s and 10 s each way.
To check a correction, move to that row and press `select`: the delay
goes to zero, so you see the track as the file times it, and the next
`select` sets the delay back. The on-screen display stays visible
while a panel is open.

While the on-screen display is showing, it moves the subtitles up so
that the last line of a cue stays just above whatever the display draws
at the bottom of the frame. When the display hides, the subtitles
return to the place that [How subtitles look](#how-subtitles-look)
describes: the house row, or for a cue of two or more lines, the bar
or the foot of the picture. The move does not apply to an ASS sign that the track places
at a fixed point, or to an image cue in the upper half of the picture.

`back` does the first of these that applies:

1. It closes an open track panel.
2. It cancels a scan's preview.
3. It hides the on-screen display.
4. When the display is hidden and the film is stepped back beside the
   up-next column ([`next`](#spec--next)), it returns the film to the
   whole screen, and the film keeps playing.
5. When only the video is showing, it ends the run, the same as `stop`.

The display steps the film back by asking the compositor to move the
window whose xdg `app_id` is `liken-media-video`. The display's own
window has the `app_id` `liken-media-overlay`.

Sending `play`, `hold`, or `stop` twice changes nothing. The playback
pod binds them to the key names that
the kernel's `rc-cec` keymap gives the deterministic functions of a TV
remote (HDMI-CEC 1.3a, CEC 13.13.3): `KEY_PLAYCD`, `KEY_PAUSECD`, and
`KEY_STOPCD`. The playback pod binds no power key. The operator reads
the power keys and turns the `Player` off, which publishes `stop` here
([Turning a Player off and on](/docs/reference/players/#turning-a-player-off-and-on)).
The keymap also gives the Pause-Play Function the name `KEY_PLAYPAUSE`,
which the play button of a Bluetooth remote sends too, so that key
switches between paused and playing. A `Keymap` row on the `Remote` of
the CEC adapter's input device can map the Pause-Play Function to
`KEY_PAUSECD` instead.

This topic has no volume action, because the volume belongs to the
`Player` and not to the run. The operator reads the volume keys from the
`Remote`'s events topic, and any other program publishes to the
[Player's `volume/commands` topic](/docs/reference/players/#volumecommands).
The playback pod ignores an action that it does not know. The key that
cycles the focus, `KEY_CYCLEWINDOWS`, becomes a request on the
[Remote's topics](/docs/reference/remotes/), not a command here.

### `status`

The playback pod publishes the run's status from `mpv`. It publishes a
pause, a resume, a new item, or a new track language at once, and a
change of position at most once a second. The message is retained, so a
restarted operator reads the position of a running `Play` from the
broker.

    {
      "paused": false,
      "item": 1,
      "position": "0:41:22",
      "duration": "1:58:03",
      "audioLanguage": "eng",
      "subtitleLanguage": "eng",
      "pod": "5f0c7a52-8e1d-4c3b-9a27-2d6b1e4f8c90"
    }

`item` counts from 1 in the order of `spec.items`. `duration` is empty
until the player has read the item's header. `audioLanguage` and
`subtitleLanguage` are absent while no track of that kind plays. Their
values are the track's own language tags from the file, which for
Matroska are the three-letter ISO 639-2 codes, whatever form the
language preference used.

The `ended` field appears when the run is over, and it stays in every
later report of the same run. The operator returns the `Player` to idle
when it reads `ended`, without waiting for the pod to stop.

`pod` is the UID of the playback pod that sent the report. One `Play`
can have several pods in turn: the operator creates the pod again after
an edit to the `Player`, after `mpv` crashes, and after something else
deletes the pod, such as an eviction. The new pod has the same name, and
the old pod reports `ended` when it stops. So the operator ignores a
report from a pod that is being deleted, from a pod that is not the
run's current pod, and from a pod that it knows is gone or replaced,
and the old pod's ending does not move the `Player` to `Idle`. While the
operator's pod watch shows no pod for the run, it accepts a report from
any pod that it does not know is gone, because the new pod can report
before the watch shows it.

The operator treats a report with no `pod` field as coming from the
run's pod, so a pod that runs an older sidecar image still reports. The
operator cannot tell two such pods of one run apart, so the rules above
do not apply to them.

The operator copies the reports into the `Play`'s status, but when only
the position changed, it writes the status about every 10 seconds. A program that shows a
position that advances every second reads this topic.

The pod publishes an empty retained payload here when its run ends
cleanly. The operator clears the topic too, in case a pod died without
clearing it. It adds the finalizer `media.liken.sh/bus-topics` to every
`Play`. When the `Play` is deleted, the operator deletes the pod, waits
until the pod is gone, publishes an empty retained payload on `status`
and on `availability`, and then removes the finalizer. So a deleted
`Play` leaves no report on the broker.

### `availability`

The payload is `online` or `offline`, a space, and the pod's UID, for
example `offline 5f0c7a52-8e1d-4c3b-9a27-2d6b1e4f8c90`. The message is
retained, and it tells a reader whether the status report comes from a
running pod ([availability](/docs/reference/bus/#availability)). The
pod registers `offline` and its UID as its MQTT Last Will, publishes
`online` and its UID after it connects, and publishes `offline` and its
UID itself when its run ends cleanly.

The broker publishes the Last Will of a dead pod only when the pod's
MQTT keepalive runs out, which can be after the run's new pod is online.
So the operator applies the same rules to an availability message as to
a status report, and a late `offline` from the old pod does not discard
the new pod's report. It treats a message with no UID as coming from
the run's pod. Until a newly started operator's pod watch has listed the
cluster, the retained `offline` of an old pod can make it discard the
report that it read. The new pod's next report restores it.

The operator clears this topic with an empty retained payload on the
same terms as `status`: on its finalizer, after the `Play` is deleted
and its pod is gone.
