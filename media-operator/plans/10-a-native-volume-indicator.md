# A native volume indicator

Plan 10. It draws a volume readout `liken` owns, in place of `mpv`'s
built-in text. It builds on the display of
[plan 07](completed/07-the-player-draws-its-own-display.md) and the OSD
fade that ships with it. It builds before
[plan 09](09-the-music-experience.md): the readout is the small piece,
and the music layout is the large one.

## The problem

Plan 07 gave `seek`, `chapter`, and `pause` a `liken`-drawn OSD, and
turned off `mpv`'s own overlay for them. `volume` and `mute` still carry
`osd-auto`, so `mpv` draws its own line for those two, a different look
from the rest of the display. A viewer who raises the volume sees
`mpv`'s text, not `liken`'s, so one action breaks the display's voice.

## The display owns the readout

`volume` and `mute` move to `no-osd`, so `mpv` draws nothing, and the
display draws the level itself. The command sidecar already sends
`no-osd` for the actions the display owns, and this slice adds `volume`
and `mute` to that set.

## The indicator appears on a change

The display observes `mpv`'s `volume` and `mute` properties. A change
shows the indicator and arms the same idle hide the scrubber uses, so
the readout appears the moment the level moves and leaves on its own.
The first observation only records the value, the way the pause observer
does, so a `Play` that starts does not pop the indicator on load.

The indicator is not a focus stop. `up` and `down` do not land on it,
because volume is a quick repeated action, not a control a viewer walks
to. It appears on a change and leaves on idle, and nothing else.

## The look

A short bar with the level and a speaker glyph, drawn through `theme`,
so it matches the scrubber and fades with the rest of the OSD. The glyph
carries the muted state, so `mute` reads on the same element with no
second one. The placement settles when the slice is built. A low
centered bar and a readout under the clock are the two candidates.

## Set aside for this slice

* **A preferred startup volume.** A level `MediaPreferences` sets at a
  `Play`'s start is a preference like the languages of plan 08, and a
  follow-on, not part of drawing the readout.
* **Volume shared across players.** This slice shows and changes one
  `Play`'s own `mpv` volume. A level shared across several players, and
  the unity-volume path the audio operator runs, are the larger A/V
  design and are set aside here.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A film plays, and the drill checks each claim:

* Raising and lowering the volume shows the `liken` bar, not `mpv`'s
  text, and the bar tracks the level.
* `mute` shows the muted state on the same indicator.
* The indicator fades out on idle, and it does not pop at the `Play`'s
  start.
