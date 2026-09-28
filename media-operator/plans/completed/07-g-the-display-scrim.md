# The display scrim

Plan 07-g, a slice of [plan 07](07-the-player-draws-its-own-display.md). It
puts a dark gradient behind the header and the bottom cluster, so every glyph
reads against the same background, and it removes the text outlines the display
uses today. When this slice lands, the header, the scrubber, and the control
strip sit on a scrim, and the text is flat.

This slice is built before 07-e, out of the plan's number order,
because it settles the display's look while the display is small.

## The problem

The display draws its text over the film, and the film is any color. So each
glyph carries an outline to hold contrast against a bright frame. The outline
works, but it reads busy, and the contrast still shifts with the frame behind
it. A caption over a white sky and the same caption over a night street do not
read the same.

## The scrim gives one background

A dark gradient sits behind the header at the top and behind the scrubber and
the control strip at the bottom. It darkens the edge and fades to nothing
toward the center, so the picture is never boxed in. Every glyph then reads
against the same dark background, and the contrast no longer depends on the
frame. With the scrim in place, the text drops its outline and draws flat.

The scrim appears only with the OSD. A playing film shows no scrim, because the
OSD is hidden and the viewer wants the picture. A press or a pause summons the
OSD and the scrim together.

## The z-order decides the technique

`mpv` draws the `libass` OSD and the `overlay-add` bitmaps in a fixed order,
and which one sits on top decides how the scrim is drawn. This slice settles
that order in the harness first, then builds the scrim to match:

* If `libass` draws over the `overlay-add` logo, a scrim drawn in ASS would
  darken the logo. So the scrim is a gradient the bridge draws to `bgra` and
  places under the logo with `overlay-add`, sized to the screen the way the
  logo is.
* If the logo draws over `libass`, the scrim is a stack of translucent
  rectangles on the virtual canvas, with stepped alpha for the gradient. It
  needs no bridge and no resize handling, and `libass` scales it with the rest
  of the canvas.

The rectangle technique is simpler, so it wins if the z-order allows it.

## The theme owns the scrim and the flatter text

`theme.lua` is the display's visual vocabulary, so the scrim and the text
weight live there. The scrim is a theme primitive the header and the bottom
cluster both draw. The outline the text carries today becomes a theme choice
the modules no longer set, so removing it is one change in the theme, not one
per module.

## Set aside for this slice

* **The chooser dimming.** An open chooser already dims the whole frame, and
  that dimming stays as it is. The scrim is behind the resting OSD, and the
  chooser dim is a separate, heavier layer over everything.
* **A per-type scrim.** Every media type gets the same scrim. A layout tuned
  to `music` or `image` art is a later concern.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired DualSense. A
`Play` runs a film, and the drill summons the OSD over a bright frame and a
dark frame.

The drill checks each claim:

* The header and the bottom cluster sit on a dark gradient that fades toward
  the center. The text carries no outline.
* The same caption reads with the same contrast over a bright frame and a dark
  frame. The scrim holds the contrast.
* The logo reads clearly over the scrim, whichever layer the scrim draws on.
* A playing film shows no scrim. A press or a pause summons the OSD and the
  scrim together, and playing hides both.
