# 50, A two-line cue goes in the encoded bar

Proposed and built on 2026-11-26. The display's tests and the
operator's tests pass. No drill has run on a cluster or on a screen.

[Plan 49](49-a-two-line-cue-goes-in-the-bar-or-above-it.md) puts a text
cue of two or more lines in the lower bar of a film, or above the
picture when the bar cannot hold it. It reads the bar from mpv's
`osd-dimensions`. This plan adds the bars that are part of the encoded
picture, which
[plan 46](46-the-display-places-every-subtitle-track.md) lists in its
"Limits".

## The problem

A film of 2.39:1 stored as a 1920x1080 frame has black rows in the
frame above and below its picture. mpv draws the whole frame, so
`osd-dimensions` report a lower bar of 0, and plan 49 moves no cue. On
2026-11-26 the maintainer measured one such episode on a 3840x2160
screen: the visible picture ended at screen row 1895 of 2160, and the
ink of a two-line cue was on rows 1859 to 2046, so its top line was 36
rows into the picture.

## The design

[`library-operator` plan 102](../../../library-operator/plans/completed/102-the-probe-measures-the-active-area-of-each-video.md)
measures the active area of each video stream, the rectangle of the
frame that contains picture. Each item's `presentation` in a `Play` can
have `video`, a list with one entry for each video stream of the item's
file, in container order:

```yaml
video:
  - stream: 0
    width: 1920
    height: 1080
    activeArea: {left: 0, top: 131, width: 1920, height: 817}
```

- `stream` is the stream's index in its container. The display matches
  it to the `ff-index` of the selected video track in mpv's
  `track-list`.
- `width` and `height` are the size of the frame that the area is
  measured in.
- `activeArea` is absent when the probe found no area that its samples
  agree on.

The operator copies the list from the `Play` to the display's
presentation block and does not read it, as it copies `audio`.

### The bar

The lower bar of plan 49 becomes the bar from `osd-dimensions` plus the
black rows of the frame below the active area, scaled from rows of the
frame to rows of the screen. mpv draws the frame in the rectangle that
`osd-dimensions` leaves between its bars, so one row of the frame is
that rectangle's height divided by the frame's height, in rows of the
screen. In rows of a 1080-row screen, where `height` is the frame's
height from the entry:

    encoded = (height − activeArea.top − activeArea.height) ÷ height
    picture = (screen height − top − bottom) × 1080 ÷ screen height
    bar     = bottom × 1080 ÷ screen height + encoded × picture

The rest of plan 49's arithmetic is the same, with this `bar`. libass
still scales a cue's margin by the picture that mpv draws, the whole
frame, so `picture` in plan 49's equation for the margin keeps its
value. A cue of one line keeps `MarginV` 0 on every title, so it ends
where it ends today. An item with no `video`, or whose selected video
track has no `activeArea`, has an encoded bar of 0 and keeps plan 49's
margins. So does an entry with no `height`, or with an area that does
not fit in its frame.

The display builds the copy's script again when the bar changes, as it
does when `osd-dimensions` change.

### The margins

The tests of `cue.rs` state the margin of a two-line cue of a SubRip
copy, whose header plan 49 writes for each screen. The rows of the ink
are on a 1080-row screen and come from plan 49's constants: the ink of
two lines is 101 rows tall and ends 4 rows above the end of its line
box. They are computed, not rendered.

| Screen | Frame and active area | Bar | `MarginV` | Rows of the ink |
|---|---|---|---|---|
| 1920x1080 | 1920x1080, area 1920x804 from row 138 | 138 | 4 | 960 to 1061 |
| 3840x2160 | 1920x1080, area 1920x804 from row 138 | 138 | 4 | 960 to 1061 |
| 3840x2160 | 1920x1080, area 1920x817 from row 131 | 132 | 3 | 964 to 1065 |
| 1920x1080 | 1920x872, area 1920x800 from row 36 | 140 | 5 | 960 to 1061 |
| 1920x1080 | 1920x1080, area the whole frame | 0 | 0 | 926 to 1027 |
| 1920x1080 | 1920x1080, no area | 0 | 0 | 926 to 1027 |

The episode that the maintainer measured is the third row. Its bar is
rows 948 to 1079 of 1080, and the ink of two lines leaves 16 rows of
black above it and 14 below it. On the 3840x2160 screen the ink is on
rows 1928 to 2130, and the picture ends at row 1895. The fourth row is
a frame with a bar of its own and black rows inside it: the screen's
bar is 104 rows, and the 36 black rows of the frame make it 140. The
last two rows are plan 49's two-line cue on a title that fills the
screen, which its renders drew on rows 927 to 1026. A cue of one line
keeps `MarginV` 0 in every row.

### Image tracks

Plan 46 places an image track by `sub-pos` and `sub-scale`, which apply
to every cue of the track, and the display cannot write a margin into a
PGS or VobSub cue. So a two-line image cue still covers the foot of the
picture, as plan 49's "Limits" says, and the encoded bar changes
nothing for an image track. Its one-line placement does not change.

## Limits

- **An area that changes within a film.** The probe records the largest
  area that its samples found, so a film with IMAX scenes usually has no
  encoded bar, and its two-line cues cover the foot of its scenes of
  2.39:1. When no sample is in an IMAX scene, the film has the area of
  its wider scenes, and a two-line cue covers the foot of each IMAX
  scene.
- **The top bar** is not used, because no cue rests at the top of the
  frame.
- **An external video track**, or a video stream that the probe did not
  read, has no entry, and keeps plan 49's margins.

## Alternatives that were set aside

- **mpv's `video-crop`.** Cropping the encoded bars would make mpv
  report real bars, and plan 49 would work unchanged. But mpv then
  scales the cropped picture to the screen's width or height, which
  changes the picture's size on every screen whose shape differs from
  the frame's, and the size of every subtitle that mpv scales with the
  picture.
- **A `cropdetect` filter in mpv** for each `Play`, for the reasons in
  `library-operator` plan 102.

## How the work is proved

Tests of `cue.rs` compute the margin for the cases of the table. Tests
of `streams.rs` read the `video` list and match an entry to the
selected video track. A test of `pod.go` checks that the list reaches
the display's presentation block, and a test of the CRD checks that its
schema keeps each field. A drill plays the episode above on a
3840x2160 screen, and a capture checks the rows of a two-line cue.
