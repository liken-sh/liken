# 46, The display places every subtitle track

Proposed and built on 2026-11-21. The operator's tests pass.

A drill ran on 2026-11-22 on the testbed's `lab-portable` `Player`,
with `media-operator` 2026.11.21-001-dev-002 and a 1920x1080 display.
Each row below is from a capture of the display, and counts the white
of the text and not its outline. On a 1920x804 film, the text of a
SubRip track ended at row 1027, or at row 1018 for a line with no
descender. An ASS track ended at row 908 on release 2026.11.19-002 and
at rows 1017 to 1026 on the development build, where the display
logged `sub-ass-override force` and `sub-pos 97.358`. A PGS track on
another 1920x804 film ended at rows 817 to 825 on the release. On the
development build a measured PGS track on a 1920x804 film ended a
one-line cue at rows 1016 to 1017, with `sub-scale 0.7541` and
`sub-pos 110.231`, and the last line of a two-line cue at row 1008 or
1016. With the film paused on a cue, the display logged `subtitles
move above row 846 of the display, to sub-pos 91.1`, the cue stood
above the scrubber, and on resume the display set `sub-pos 110.231`
again. The same build then played a household's SubRip track on a
home cluster. No drill has played a VobSub track, a 4:3 title, or an
image track on a screen that is not 16:9. `library-operator` plan 97
records that the first measurement read two lines that touch as one
line on about 3% of tracks, which this display then drew at half
size, and how the second measurement corrects it.

[Root plan 95](../../../plans/completed/95-subtitles-on-one-row-in-one-style.md)
states the rule that this plan builds in the player: the last line of
every subtitle ends on one screen row at one text size, in one house
style wherever the track is text.

## The problem

mpv draws each kind of subtitle track by different rules, and the
player sets no style of its own. A mock-up on 2026-11-21 ran the
player image's mpv 0.41.0 through `--vo=dmabuf-wayland` on a
1920x1080 output. On a 1920x804 film, the last row of text of a
one-line cue was row 1027 for a SubRip track, row 918 or 926 for an
ASS track, and row 821, 829, or 897 for three PGS tracks.

The display also moves subtitles today. While it draws at the foot of
the frame, it lowers `sub-pos` from 100 so that a cue ends above the
scrubber, and it sets 100 again when it hides. Its arithmetic is
right for a plain text track and near for the other kinds.

## The design

### The house style on the command line

`mpvPreferenceOptions` adds the house style to mpv's options, because
it is the same for every `Play`: `--sub-font=Source Sans 3`,
`--sub-font-size=38`, `--sub-color=#FFFFFFFF`,
`--sub-outline-color=#FF000000`, `--sub-outline-size=1.65`,
`--sub-shadow-offset=0`, `--sub-margin-y=34`, and `--sub-margin-x=19`.
Each value except the font is mpv's own default today, and the font is
the one that `sans-serif` already gives in the player image. The
options state them so that a new mpv default or a new font in the
image does not change the style.

A plain text track needs nothing more. mpv ends its last line 34
scaled pixels above the bottom of the screen, in the bar of a film
that has one. That row is the house row.

### The display owns the place of the selected track

The command sidecar chooses the track, and a person can choose another
in the display's chooser. The display observes `track-list`, so it
sees the selected track whoever selected it. It is also the one
program that sets `sub-pos`. So the display sets every property that
places a track: `sub-ass-override`, `stretch-image-subs-to-screen`,
`sub-scale`, and `sub-pos`. It sets them when the selected subtitle
track changes and when a value that the arithmetic reads changes. It
sets all four for each kind, because mpv applies `sub-scale` to an ASS
track under `force` and to plain text, so a value that an image track
left behind would shrink the next track.

The display reads the kind from the `codec` of the selected track.

**Plain text** (any codec that is not ASS and not an image):
`sub-ass-override=scale`, `stretch-image-subs-to-screen=no`,
`sub-scale=1`, and a resting `sub-pos` of 100.

**ASS** (`ass` or `ssa`): `sub-ass-override=force`,
`stretch-image-subs-to-screen=no`, and `sub-scale=1`. mpv then draws the
script's dialogue in the house font, size, colours, and outline. It
keeps each event's side, its italics, and every event that the script
places itself with `\pos`, `\move`, `\clip`, `\org`, or a drawing, so
a sign stays on the thing that it labels. Under `force` libass
measures a style's `MarginV` from the bottom of the screen, in rows of
the script's `PlayResY` scaled to the height of the picture. So the
dialogue ends at screen row
`bottom = screen − MarginV × picture ÷ PlayResY`, and the resting
`sub-pos` is `100 × house ÷ bottom`, where `house` is the row where
mpv ends the line box of a plain text cue. The display reads
`PlayResY` and the styles from mpv's `sub-ass-extradata` property and
the heights from `osd-dimensions`. `MarginV` is that of the style
named `Default`, or else of the first style that is centred at the
bottom, or else of the first style, or else libass's default of 20. A
script with no `PlayResY` has the one that libass gives it: 288 when
it has no `PlayResX` either, 1024 for a `PlayResX` of 1280, and
otherwise the height of a 4:3 frame of that width.

mpv sends `sub-ass-extradata` to a client once, when the client starts
to observe it, and not when the selected track changes. So the display
ends its observation and starts it again each time an ASS track is
selected, and it sets nothing for the track until the header arrives.

The resting value is above 100 for a script whose margin is larger
than the house margin. A render on 2026-11-21 of one script with a
`MarginV` of 20, 80, and 150 at a `PlayResY` of 1080 ended its last
line on row 1029 each time, with `sub-pos` 97.4, 103.2, and 111.0.

**Image** (`hdmv_pgs_subtitle` or `dvd_subtitle`) with an `image`
block on its entry in the item's `presentation.subtitles`:

    rows     = canvasHeight ÷ 1080
    scale    = 46 × rows ÷ lineHeight
    move     = 1007 × rows − inkBottom + lineHeight ÷ 2
    sub-pos  = 100 + 100 × (round(move) + 0.5) ÷ canvasHeight

`sub-scale` is `scale`, and `sub-ass-override` is `scale`, mpv's
default. `rows` is the number of canvas rows in one row of a 1080-row
screen, so the arithmetic is the same on a screen of any height. mpv
first moves each bitmap that starts in the lower half of the canvas by
`sub-pos`, in whole canvas rows, and then shrinks each bitmap around
its own centre by `sub-scale`. The shrink takes
`(1 − scale) × lineHeight ÷ 2` rows from the bottom of a one-line cue,
so `move` is the distance from `inkBottom` to the house row plus those
rows: `1030 × rows − inkBottom + (1 − scale) × lineHeight ÷ 2`, which
is the line above with `scale` written out. The numbers 46 and 1030
are the house line of 45 rows and the row after its last row, 1029, on
a 1080-row screen, each with the one row that mpv's integer steps
remove. The half row keeps a rounding error in mpv's float from moving
the cue one row up.

`stretch-image-subs-to-screen=yes` maps the canvas onto the whole
screen, so a canvas row is the same screen row on a film with bars and
on a title without them. The display sets it for a PGS track.
For a VobSub track it sets it only when the picture is as wide as the
screen. A 4:3 title fills the height of the screen and not its width,
and there mpv's own mapping gives the same rows and the right width.
The stretch drew a 4:3 DVD's text 33% too wide.

The display places an image track this way only on a screen whose
shape is 16:9, within 1%. On another screen, and for an image track
with no `image` block, the display sets the plain text values, and
mpv draws the track as the disc placed it.

### The lift starts from the resting place

The display's lift keeps its rule: the last line of a cue ends `GAP`
above the highest thing that the display draws at the foot of the
frame. It now starts from the selected track's resting `sub-pos`:

- For plain text and ASS, mpv ends the cue at `sub-pos` percent of the
  way down to the resting row, so the lifted value is the resting
  value times `(top − GAP) ÷ FOOT`.
- For an image track, `sub-pos` moves the cue by a share of the
  canvas, so the lifted value is the resting value less the rows to
  move as a percentage of the screen's height.

When the display hides, it sets the resting value and not 100.

### The `image` field of a `Play`

`spec.items[].presentation.subtitles[]` gains an optional `image`
object with three required integers of 1 or more: `canvasHeight`,
`lineHeight`, and `inkBottom`, in rows of the track's canvas.
`library-operator` plan 97 defines how they are measured. The API
server prunes a field that the CRD does not list, so the CRD and the
Go type both gain it. The command sidecar already sends each item's
presentation to the display unread.

### A text track before an image track

`bestOf` in `subtitletracks.go` orders the tracks that have a wanted
language and role by the rank of the role, then the default flag,
then track order. It gains one step after the rank of the role: a
track that is not an image goes first. A role still outranks a kind,
so `sdh: prefer` keeps an SDH image track over a full text track. The
media browser marks the track that the rule chooses, so its copy of
the rule and the shared cases in `testdata/subtitle-rule.json` change
with it.

## What the mock-up measured

Each row is the last row of text of a one-line cue, on a 1920x1080
output, with the settings above.

| Track | 1920x804 film | 1920x1080 title |
|---|---|---|
| SubRip | 1027 to 1028 | 1028 |
| ASS, `PlayResY` 804 and 1608 | 1026 to 1027 | 1029 |
| PGS, three tracks, not shrunk | 1026 to 1027 | 1028 to 1030 |
| PGS, three tracks, shrunk | not measured | 1027 to 1029 |
| VobSub, two tracks, shrunk | not measured | 1028 to 1029 |

On the 1920x804 film the black outline is on the black bar and does
not count, so those rows are about one row lower than on the
1920x1080 title. A shrunk line was 45 or 46 rows tall, and the house
line is 45. The arithmetic for an image track, run with mpv's integer
steps over 1,444 full-height one-line lower cues of 24 sampled tracks,
put the last row within 1 row of the house row for 99.9% of the cues
and within 2 rows for all of them. Every property took effect within
0.3 seconds on the cue that was showing.

## Limits

- **A cue of two or more lines of an image track ends high.** mpv
  shrinks each bitmap around its own centre, so a taller bitmap loses
  more rows from its bottom. On the 24 sampled tracks the last line of
  a two-line cue ended 4 to 19 rows above the house row. No setting of
  these properties removes that.
- **A two-line cue covers the foot of the picture on a film with
  bars.** The lower bar of a 1920x804 film is 138 rows, and two lines
  of house text use rows 928 to 1027.
- **Cues at the top of the frame have no common row.** `sub-pos` does
  not move a bitmap that starts in the upper half of the canvas, and
  each kind puts a top cue on a different row.
- **An image track keeps the disc's font.**
- **An ASS cue on the left or the right uses the script's side
  margin**, which `force` does not replace.
- **Bars that are part of the encoded picture** look like picture to
  mpv, so nothing here moves a cue into them.
- **HDR.** The mock-up did not measure the brightness of the subtitle
  surface on an HDR title.

## Alternatives that were set aside

- **The command sidecar sets the resting values.** A track that a
  person selects in the display's chooser does not pass through the
  sidecar, and the display's reset of `sub-pos` to 100 would remove
  the value. Two programs would then set one property and need a
  channel between them.
- **Image tracks at the disc's size.** The last line of every cue
  then ends on the exact row, but one line is 51 to 73 rows tall
  against the house line of 45. The maintainer compared both on
  2026-11-21 and chose the house size.
- **`sub-ass-override=strip` for ASS.** It removes each event's side
  and its italics.

## How the work is proved

Tests of the display compute the properties for a track of each kind
and for each lift, from the numbers of the mock-up. Tests of
`pod.go` state the options. The shared cases of the subtitle rule
gain files with a text track and an image track in one role. A drill
on a home cluster with a development build then plays a real file of
each kind, and a capture of the display shows where its last line
ends. The drill's rows go in this header.
