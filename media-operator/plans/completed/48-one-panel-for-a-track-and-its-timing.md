# 48, One panel for a track and its timing

Proposed and built on 2026-11-23. The tests of the display pass, and
captures of the display under a headless compositor on a laptop show
each state. No drill has run on a television.

This plan replaces the control strip's stops and panels of
[plan 07-h](07-h-the-control-strip.md). It closes the open problem
"The track labels say too little and too much", and it builds the
section "The strip names the direction" of
[`library-operator` plan 67](../../../library-operator/plans/67-a-subtitle-offset-that-stays.md).
Saving an offset between plays stays in that plan.

## The problem

A viewer who opens the strip to change a track or to correct its
timing cannot read what the strip offers.

- A track value has 176 canvas pixels. An audio title such as
  "[English / DD+ 5.1 Atmos]" is cut to "[English / DD+ 5.…", and a
  subtitle value reads `ENG`.
- The chooser names a track by its title and its language code. Many
  files give a subtitle track no title, so three rows read `ENG`, and
  the forced track, the full track, and the SDH track look the same.
- An offset reads "+300 ms". Neither the strip nor the adjuster says
  whether that shows the subtitles sooner or later.
- A chooser or an adjuster opens at the left margin, and the stop
  that opened it is at the right.
- An open chooser or adjuster dims the film, and the viewer judges
  the timing by watching the film.

## The design

Every control for a track is on the frame's vertical centre line,
under the cue it changes. Up and down move along the line and left and
right move along the timing scale, so the panel draws the remote's two
axes, and a delay reads as a distance from the line.

**One stop for each kind of track.** The strip has a stop for
`Audio`, a stop for `Subtitles` when the file has a subtitle track,
and a stop for `Video` when the file has more than one video track.
A delay is not a stop. Select on a stop opens that kind's panel.

**The strip is one line on the centre line.** A short hairline is on
the centre line under the scrubber. The audio stop ends a gutter to
its left and the subtitle stop starts a gutter to its right, each its
heading in italics and then its value: "*Audio* English E-AC3 Atmos
5.1 | *Subtitles* English *SDH* · 0.7 s sooner". A file with no
subtitles keeps the hairline with its audio on the left, and a video
stop follows the subtitle stop. A value takes the longest of these
that fits: the language with the role and the format, the language
with the role, or with the channels when the track has no role, the
language alone, and the language's code. A subtitle value has no
format. While a delay is not zero, the value ends with it in faint ink.
The delay is never the part that is cut. The strip is one line, so the
scrubber moved 24 pixels down: the bar is at y 928, and the strip's
baseline is 56 pixels above the bottom edge.

**The panel has two axes.** The panel has the timing at the top and the
tracks under it. Up and down move the focus through the rows, and left
and right move the timing by one step whichever row has the focus.

| Key | In an open panel |
|---|---|
| Up, down | Move the focus. The timing is the row above the first track. |
| Left, right | Show the track one step sooner or one step later. At the end of the range, a press that way changes nothing. |
| Select on a track | Play that track, and the panel stays open. On the track that plays, close the panel. |
| Select on the timing | Compare: set the delay to zero, and on the next select set it back. |
| Back | Close the panel. |

The panel opens with the focus on the track that plays. It has no
plate and does not dim the film. A soft darkening with no edge sits
behind its words, and each word has a thin dark outline, so the words
read over a bright picture.

**The timing reads as words over a scale.** The readout is centred on
the line: "Subtitles 0.7 s sooner", "Audio 0.10 s later", "Subtitles as
timed in the file" at zero, and "Subtitles off" while no subtitle track
plays. The display cannot observe whether a track is in sync, so no
word claims it. A subtitle delay has one decimal and an audio delay
two, the size of their steps. The scale runs 340 pixels each way from
the centre line, with italic "sooner" and "later" at its ends. The
mark is an accent needle with a run back to zero, at the square root of
the delay's share of the range, so a delay of 0.7 s is 90 pixels from
zero where a linear scale puts it 24 pixels away. Short ticks mark 0.5,
1, 2, and 5 s each way for subtitles and 0.1, 0.25, 0.5, and 1 s for
audio, with no numerals. At the end of the range, the end word on that
side turns faint. The steps and the ranges of plan 07-h do not change.
While subtitles are off, the readout and the scale are faint and left
and right change nothing.

**Compare answers "did I go the right way".** A viewer can tell only
by watching a line with the delay and without it. Select on the
timing row sets the delay to zero and remembers the value, and the
next select sets it back. A tick on the scale marks the remembered
delay, and the line under the scale reads "Select returns to 0.7 s
sooner". The timing keeps room for that line whether it shows or not,
so the rows and the cue do not move. A step to the left or the right
forgets the remembered value. This replaces the reset on up.

**A row names a track in parts, on both sides of the line.** They are
the parts that the media browser writes on a film's page, in the same
words, from the same code in `media-screen`.

| Part | Audio | Subtitles |
|---|---|---|
| Language | In English, from the tag. With no language, the title, and with no title, "Track 3". | The same. |
| Role, in italics | `commentary` from the title. `described` from the visual-impaired flag or the title. | `SDH`, `forced`, `commentary`. A full track has no word. |
| Title | Only when it has a word that the other parts do not say. | The same. |
| Format | "E-AC3 Atmos 5.1" | "SRT", "PGS", "SRT file" |

The language is right-aligned to the line and is written once for its
group of rows, like a film's credits. Each row's role, title, and
format are left-aligned from the line. A subtitle row always starts
with its role, and a plain subtitle track reads *full* there, so a
viewer reads every row of a group as a track to pick. The word is the
panel's: the strip and the media browser write no word for a full
track. When a language has two or more subtitle tracks and all of them
have the same format, the format is written once, faint, before the
language on its line, wherever the language is written, and the rows
leave it out. It depends on the group and not on the rows that show,
so a row reads the same wherever the list is scrolled. A language with
one track, or with tracks of different formats, keeps the format on
each row. An audio row keeps its format,
because the format names a plain audio track. A title that starts with
the role word, as in "Commentary by novelist Ann Example", drops the
word and a following "by" or "with", and what remains starts with a
capital letter: "Novelist Ann Example". A track tagged `mul`, `zxx`, or
`mis` names no one language, so it reads as a track with no language. A title too long for the frame ends in an
ellipsis, and the format after it stays whole. A track with no
language shows its title or its number on the right side. When two
rows read the same, each shows its track number. The dot of the
playing track is on a faint hairline down the centre line.

**The rows are in an order that a viewer can scan.** The subtitle
list starts with `Off`. Then come the tracks in the language of the
subtitles that play, then those in the language of the audio that
plays, then the other languages by their English names. Within a
language the order is full, SDH, forced, and commentary, and tracks
of one role keep the file's order. A track with no events is not
listed unless it plays. The audio list has the language that plays
first and the others by name, and within a language the described
tracks and the commentaries follow the others, so they stay in their
language's group.

**A long list names what it hides.** A list of up to seven rows shows
whole. A longer list shows five rows and a line at each end that names
the languages of the rows above or below, such as "Off · English ·
Chinese · Dutch", with the dot beside the language that plays. A line is
no wider than the scale with its end words, so it reads as part of the
panel. It names `Off`, the playing language, and the languages nearest
the rows that show, for as long as they fit, and counts the rest at
the end far from the rows: "4 more · Off · English · Danish · Dutch"
above, and "German · Greek · Hebrew · and 21 more" below. The
panel keeps its height while the list scrolls. So the cue, which rises
to 24 pixels above the panel's top, does not move and stays in the
lower two thirds of the frame for a file with 30 subtitle tracks.

**The focus and the mark glide.** By the rule for the focus outline
in `brand/motion.md`, the underline glides from the row it left to the
row it takes, and the mark glides to a new delay, each in 7 frame ticks,
about 117 ms, with smoothstep easing. A press during a glide starts
from where the underline or the mark is drawn, and the glide shortens
to the time since the last one started, so a held key never leaves
them behind. The glides count the ticks of the display's fades, so the
display asks for frames only while one moves.

**Where each fact comes from.** The display gives each subtitle track
its role with the rule of
[root plan 90](../../../plans/completed/90-subtitle-tracks-by-role.md),
from `track-list` and the event counts of `presentation.subtitles`,
through the port of the rule in `media-screen`. The command sidecar
applies the same rule in Go, and both read the cases of
`testdata/subtitle-rule.json`. An audio track's format comes from the
entry of `presentation.audio` with the track's `ff-index`, which
[`library-operator` plan 98](../../../library-operator/plans/completed/98-audio-facts-on-the-play.md)
adds. Without an entry, the format is mpv's `codec` and a word for
`demux-channel-count`. The display does not read `codec-profile` or
`demux-channels`, because mpv 0.41 sets them only for the track that
it decodes, and a format that changed when a track was selected would
move the columns. The block does not name its item, so the display
records the `playlist-pos` that a block arrives at and uses the
block's facts only while mpv plays that item.

## Rejected

Four designers drew the panel without seeing each other's work. The
design above is the centre line, and these were the others:

* **Type set in a column at the right**, with the timing's zero on the
  column's edge. Its darkening dimmed a bright picture visibly, and the
  display would have to narrow the cue's wrap width while the column is
  open, so that a long cue does not run under the column.
* **A full-width plate with a dial and a ledger**, like a receiver's
  faceplate. The plate hides the film from y 386 to 812, the film that
  the viewer watches to judge the timing.
* **A small scrubber with a caption mark** standing where the track
  plays. Its scale is linear to 1 s and logarithmic beyond, the mark of
  a 0.10 s delay overlaps the stem of now, and a cue wider than about
  760 pixels reaches the column.

And these were rejected while the design was discussed:

* **Each delay as its own stop**, as plan 07-h built it. The strip had
  five small stops, and the adjuster opened apart from the track it
  belonged to.
* **A separate `Timing` stop** with both scales. A viewer whose
  subtitles are late looks under `Subtitles`.
* **The role from the command sidecar**, written to mpv's `user-data`
  property. The sidecar classifies no track when a `Play` has no
  preferences, and a role would arrive after the display first draws
  the list. The display already receives both inputs of the rule.
* **"In sync" at zero.** The display knows the delay, not the sync.
* **A linear scale.** One audio step moves its mark 3 pixels.

## Proof

Unit tests cover the naming, the order, the grouping, the key map, the
glides, and the layout. `capture.sh`, a script outside the repository,
runs mpv and the display under a headless Weston on a laptop, sends
the same `script-message` lines that the command sidecar sends, and
saves a frame of each state over a bright test pattern and over a dim
moving gradient.
