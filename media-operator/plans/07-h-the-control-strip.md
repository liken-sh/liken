# The control strip fills out

Plan 07-h, a slice of [plan 07](07-the-player-draws-its-own-display.md). It
gives the control strip its full set of media controls, drops the word captions
for grouped rows, restyles the pop-up menus, and adds a clock. Today the strip
holds two controls, audio and subtitles. When this slice lands, the strip adds
the video track and two delay offsets, reads as grouped rows at the bottom
right, the menus carry a green border and no title, and the top right shows the
time.

## The problem

The strip is the row of per-file controls under the scrubber. It holds two
controls, audio and subtitles, and draws each as a word caption over its value.
The set is short, and the caption stacked over the value reads heavy.

## The strip controls the media, not the routing

The strip controls the one file that plays. It does not control where the sound
goes or how the picture looks on the panel. The room, the speakers, and the
display's brightness are the operators' job, the audio-operator and the
display-operator, set on the `Player` and its devices. The strip sets the track
and the delay offsets for the file in front of the viewer.

There is no stream quality on the strip. `liken` plays the file direct over its
mount, with no transcode, so there is no bitrate to choose. A 4K edition and a
1080p edition are two files, and choosing between them is the `Play`'s item
list, not a control here.

## The set

* **Audio track.** Present today. It switches `aid` among the file's audio
  tracks.
* **Subtitles.** Present today. It switches `sid`, or turns subtitles off.
* **Video track.** It switches `vid` when the file carries more than one video
  track, like an alternate angle. It mirrors the audio control, and it shows
  only when a second video track is present, so most files never draw it.
* **Audio offset and subtitle offset.** Two controls, one bound to `audio-delay`
  and one to `sub-delay`. Each sits beside its track control, so a viewer lines
  up late sound, or a late subtitle, by hand. The subtitle offset shows only
  when the file carries subtitles.

Playback speed is left out by choice. A whole-house cinema plays a film at its
own pace.

## The offset controls adjust, they do not choose

A chooser is a vertical list, and a delay is a value on a line, so an offset does
not fit the list. It captures input as an adjuster instead. left and right nudge
the delay by a step, and the nudge applies at once. up resets the delay to zero,
and down, select, or back closes the adjuster. select does not reset, because a
reset on the main action is a surprise.

This adds left and right to a capturing widget. A chooser ignores left and right
today. The focus router passes them to the capturing widget, and the widget uses
them or ignores them. The offset uses them to nudge, and the chooser ignores
them.

The two offsets are one module. `offset.new` builds one bound to a property, and
the strip builds it twice, one for each delay.

## The strip reads as grouped rows

The strip draws no glyphs. It groups each track with its offset under a heading:
"audio" over the audio track and the audio offset, "subtitles" over the subtitle
track and the subtitle offset. The heading reads small and dim, and the two
values read on the line below it. So the heading names the kind, and an icon adds
nothing. The focused control reads bright and green, and the rest read dim, so
brightness marks the focus with no cursor.

## The layout right-aligns the groups

The groups sit at the bottom right, the row's right edge at the margin, below the
scrubber's time line. The type is small and the spacing is tight, so the row is
compact. The groups fall on a fixed pitch, and the row right-aligns on the last
group's nominal width, a constant, so the row holds its right edge when a value
changes width, and only that value moves. A group with no present control leaves
no column, so a file with no subtitles draws no subtitle group. The type size
and the spacing are tuned on the workstation before the drill.

## The menus lose their titles and gain a border

A track chooser and an offset adjuster pop up over the video. Each drops its
title, because the rows name themselves, and each draws inside a solid green
border in `liken`'s accent, so the menu reads as one surface over the film. The
panel dims the video behind the text without hiding it.

## The clock

The top right shows the wall-clock time and the time the film ends, the current
time bright and the end time dim. The end time is now plus the time left. It
reads the hour when the display appears. The display is transient, so it does not
tick while it shows.

## Set aside for this slice

* **Glyphs.** The controls read as words under a group heading, not as icons.
  The heading names the kind, so an icon adds nothing.
* **Aspect and zoom.** `liken` plays clean files on widescreen panels, so `mpv`
  reads the right shape and there is nothing to override. The one common use,
  Fill, crops a scope frame to remove the letterbox, which a cinema does not
  want.
* **Playback speed.** Left out by choice, not by cost.
* **Routing and picture.** The room, the speakers, and the display's brightness
  are the operators' controls, reached through the `Player`, not the strip.
* **Stream quality.** `liken` plays the file direct, so there is no bitrate to
  pick.
* **The music layout.** A music item composes its own controls, and that is
  plan 07-f.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired DualSense. A
`Play` runs a film with more than one audio track and with subtitles.

The drill checks each claim:

* The strip reads as grouped rows at the bottom right, a dim heading over each
  track and its offset, and the focused control reads bright.
* The audio, subtitle, and video choosers switch the track, and the value
  follows. Each menu drops its title and draws a green border.
* Each offset adjuster nudges its delay with left and right, up resets it, and
  down closes it. The subtitle offset shows only with subtitles.
* A file with one video track draws no video group.
* The top right shows the wall-clock time and the end time, the current time
  bright and the end time dim.

Before the hardware drill, the same path runs on a workstation through
`media-preview`, so the layout is seen and tuned locally before the release.
