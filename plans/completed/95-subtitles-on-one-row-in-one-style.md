# 95, Subtitles on one row in one style

Proposed and built on 2026-11-21. The tests of both operators pass.

## The problem

A household that reads subtitles on every title sees them in a
different place, size, and font from one title to the next. A subtitle
track is one of three kinds, and mpv draws each kind by different
rules:

- **Plain text**, such as SubRip. mpv chooses the font, the size, and
  the place. On a film with black bars above and below the picture,
  mpv draws the text in the lower bar.
- **Styled text**, which is ASS. The script names its own fonts,
  sizes, colours, and margins, and mpv draws it inside the picture.
- **Images**, which are the PGS tracks of a Blu-ray and the VobSub
  tracks of a DVD. Each cue is a picture of text at the size and the
  row that the disc's author chose. mpv fits the disc's canvas into
  the picture, so on a film with bars the cue is smaller and higher.

A survey of a home cluster's catalog on 2026-11-21 counted the track
that `media-operator` chooses for each of 9,698 video files under the
default `MediaPreferences`: 6,869 plain text, 616 ASS, 1,828 image,
and 385 none. On one film with bars, the last line of a cue ended at
screen row 821 for its PGS track, row 918 for an ASS track, and row
1027 for a SubRip track, of 1080 rows.

## The rule

The last line of every subtitle ends at the same screen row on every
title, at one text size, and in one house style wherever the track is
text. On a film with bars that row is in the lower bar, and on a 16:9
title it is over the picture.

The house style is Source Sans 3 Regular at mpv's size 38, white with
a black outline of 1.65, with no shadow and no box. The last line ends
34 scaled pixels above the bottom of the screen, where a scaled pixel
is one row of a screen 720 rows tall.

## The parts

Two operators build the rule, and one fact passes between them.

- **`library-operator` measures each image track.** mpv receives an
  image track one cue at a time while it plays, so it cannot report
  where the track usually draws or how tall its text is. The probe
  fact reads a sample of each image track's cues and records the
  height of the track's canvas, the height of one line of its text,
  and the row where its text usually ends. The media browser sends the
  three numbers with the track in `items[].presentation.subtitles` of
  a play request.
  [`library-operator` plan 97](../../library-operator/plans/completed/97-the-probe-measures-image-subtitle-tracks.md)
  has the design.
- **`media-operator` places every track.** mpv's command line states
  the house style. The on-screen display sets, for the track that is
  selected, the mpv properties that put its last line on the house row
  at the house size. An image track with no measurement is drawn as
  the disc placed it.
  [`media-operator` plan 46](../../media-operator/plans/completed/46-the-display-places-every-subtitle-track.md)
  has the design.
- **A text track goes before an image track** when both have the
  language and the role that the preferences ask for, because a text
  track takes the whole house style and an image track takes only the
  row and the size. The role still decides first. `media-operator`
  plan 46 has the change to the rule of
  [plan 90](90-subtitle-tracks-by-role.md).

## What the rule leaves out

An image track keeps the disc's font, because its cues are pictures.
[`library-operator` plan 91](../../library-operator/plans/91-text-subtitles-from-image-subtitles.md)
proposes text tracks made from image tracks by character recognition,
and it stays a separate plan. A measurement on 2026-11-21 of 4,212
image cues from 24 files is recorded there for the session that
designs it.

## How the work is proved

Each operator's tests cover its part. The components are then proved
together on a home cluster with a development build of both
operators: the probe measures the image tracks of real libraries, and
a `Play` of a file of each kind shows its last line on the house row.
Each component's plan records what its drill measured.
