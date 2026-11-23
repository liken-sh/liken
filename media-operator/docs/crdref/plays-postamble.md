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
- On a film with bars, a cue of two lines covers the foot of the
  picture, because two lines of house text are taller than the lower
  bar.
- A cue at the top of the frame has no common row. Each kind of track
  puts it somewhere else.
- A script that places an ASS cue on the left or the right keeps its
  side margin.
- Bars that are part of the encoded picture look like picture to
  `mpv`, so no track moves a cue into them.

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
its own over the film only for the first 15 seconds of its span. For
the rest of the span it shows with the on-screen display: `up` or
`down` shows the display with the control focused, and the control
hides when the display hides. Skipping to a post-credits scene works
only while the on-screen display is showing. The display never skips
by itself.

In a track chooser, the focused row is bright and underlined in the
accent color, and a dot marks the track that plays now. The on-screen
display stays visible while a chooser or an adjuster is open.

While the on-screen display is showing, it moves the subtitles up so
that the last line of a cue stays just above whatever the display draws
at the bottom of the frame. When the display hides, the subtitles
return to the house row that [How subtitles look](#how-subtitles-look)
describes. The move does not apply to an ASS sign that the track places
at a fixed point, or to an image cue in the upper half of the picture.

`back` does the first of these that applies:

1. It closes an open chooser.
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
