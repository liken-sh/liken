# The stack and the choosers

Plan 07-b, the second slice of [plan 07](07-the-player-draws-its-own-display.md).
It completes the navigation model on top of [07-a](07-a-the-scrubber-a-remote-summons.md):
the vertical stack of focus regions, the chapter scrubber, and the
control strip with the audio and subtitle choosers. When this slice
lands, a remote walks the OSD from the scrubber to the chapters to the
controls, and a chooser changes the audio track.

## The problem

07-a draws one region, the scrubber, and a remote scrubs it. The
display has no way to reach a second region, no chapter navigation, and
no controls. This slice adds the rest of the navigation model, and it
does so entirely from `mpv`'s properties, so it needs no `presentation`
block and no bitmaps.

## The stack that up and down walk

The OSD becomes a vertical stack of focus regions, and `up` and `down`
walk between them. Each region uses `left` and `right` for its own
axis. `focus.lua`, which held one region in 07-a, now holds which
region has focus and moves it on `up` and `down`. `up` and `down` are
never captured, so the viewer is never trapped in a row.

The stack is dynamic. A region draws and takes focus only when it has
something to show, so `focus.lua` walks the regions that are present for
the current file, and skips the rest.

## The chapter scrubber

`chapters.lua` draws a second scrubber row, present only when the file
has chapters. It reads `chapter-list` from `mpv`, which carries each
chapter's title and start time. `left` and `right` step one chapter at
a time, and the fine playhead follows, so the row is a coarse seek axis
above the fine scrubber's seconds. This is why chapter stepping is a
row and not a mode on the fine scrubber: a mode would capture `up` and
`down`, and a row keeps them free to leave.

## The control strip and its choosers

`focus.lua` gains the control strip as the lowest region. `left` and
`right` move across its controls, and `select` acts on the focused one.
This slice ships two controls, both fed by `mpv`'s `track-list`:

* `audio.lua`, the audio-track control. Its chooser lists the audio
  tracks, each with the `title` and `lang` that `track-list` carries,
  and `select` switches `aid`.
* `subtitles.lua`, the subtitle control. Its chooser lists the subtitle
  tracks and an off entry, and `select` switches `sid`. Its icon shows
  the current track's language, filled when subtitles are on and
  outlined when they are off.

Each control follows the module contract from the parent design:
`available` decides whether it appears, `draw` returns its icon and
status, `activate` opens its chooser, and `handle` consumes presses
while the chooser captures. A chooser is the one state that captures
input, and it always closes on `back`.

The icon font arrives in this slice, because the controls are the first
elements that draw an icon. It is an open-licensed font in the player
image, and a control prints a codepoint to place its icon.

## Set aside for this slice

* **The quality and prev/next controls.** Quality depends on more than
  one source for the same media, and prev/next depends on a multi-item
  `Play`, which arrives with the contract in 07-c.
* **The presentation contract.** This slice reads only `mpv`'s
  properties. The `Play` gains no field here.
* **The art and the trickplay.** They need bitmaps, in 07-d and 07-e.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A film with chapters and more than one audio track plays
from a `Play`.

The drill checks each claim:

* A press summons the OSD, and `down` moves focus from the scrubber to
  the chapter scrubber to the control strip. `up` returns. The stack
  walks and never traps the cursor.
* On the chapter scrubber, `left` and `right` step one chapter, and the
  fine playhead jumps to it.
* On the control strip, `select` opens the audio chooser, `select` in
  the chooser changes the audio track, and `back` closes it. The
  subtitle control shows its language and toggles.
* A file with no chapters shows no chapter row, and `up` and `down`
  skip it. The stack is dynamic.
