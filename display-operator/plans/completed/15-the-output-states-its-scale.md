# The output states its scale

Plan 15. Built on 2026-09-07 and proved on the house 4K `Player` the same day.

The compositor's config states an integer scale on every
output whose mode is wide enough to need one, so a client that reads
`wl_output` scale draws a 4K panel at the size it draws a 1080p one.

## The problem

The first house `Player` drove a 4K television. Every client on it
drew at one physical pixel per logical pixel: the media browser's
type, cards, and margins were half the size they are on a 1080p
panel, and the idle screen's clock the same. The clients are right.
The compositor told them the output's scale is 1, and they drew for
that.

The clients already read the scale. The media browser hands iced its
window's scale factor on every resize, and iced lays text and
geometry out in logical pixels and rasters them at physical size. mpv
sets its buffer scale from the output. What none of them can do is
decide the scale, because the scale is a fact about the panel and the
distance a person sits from it, and the compositor is the one place
that fact is stated for every client at once.

Weston 14 takes that fact as `scale=` in an output section, an
integer, 1 by default. The man page says what it does with it: a
client that draws its own high-resolution content is left alone, and
a client that does not is scaled up by the compositor so that it is
readable.

## The contract

- **A wide mode gets `scale=2`.** When the mode an output section
  states is 3840 pixels wide or wider, the section carries `scale=2`.
  A narrower mode carries no scale line, which is Weston's default of
  1. The width is read from the mode name the section writes, whether
  a claim stated it or the monitor preferred it, so the scale always
  matches the mode Weston runs.
- **The scale is integer and the rule is one line.** Weston's scale is
  an integer. A 4K panel at scale 2 lays out as a 1080p one. A wider
  mode than 4K takes the same 2, because no panel at that width is
  in front of anyone yet, and a rule with one threshold is a rule a
  person can predict.
- **Nothing else changes.** The section keeps its name, mode, and
  app-ids lines. The operator's status and the slice report the mode
  as before. The scale is Weston's to tell clients, and it is not
  reported anywhere the operator writes, because a client reads it off
  the wire.

  ```ini
  [output]
  name=HDMI-A-1
  mode=3840x2160@60
  app-ids=display.liken.sh.HDMI-A-1
  scale=2
  ```

## What was considered

- **Each client picks a scale from its physical size.** The media
  browser could read its window's physical width and choose 2 at
  3840. Every client would need the same rule, and mpv's on-screen
  text would keep the compositor's answer of 1. The compositor states
  it once and every client agrees.
- **A scale in the claim or in the `Display` spec.** A person could
  state the scale beside the mode. Nobody has asked for a 4K panel at
  scale 1, and the field would be one more thing to explain. When a
  panel arrives that wants 3 or 1, the field goes in the claim beside
  `mode`, and this rule becomes its default.

## The proof

Drill on `liken-1` with a 4K monitor on a node, if one is on the
bench, and on the house `Player` in the living room on a dev build
otherwise:

1. Roll the display-operator with the change. Read the written
   `weston.ini` from the pod and confirm the 4K output section carries
   `scale=2` and the 1080p section carries none.
2. Confirm the media browser's home page lays out as it does at
   1080p, with the wall's six columns, and that its poster art is
   sharp at the panel's full resolution. The browser half is
   library-operator plan 45.
3. Start a `Play` and confirm mpv's on-screen display text is readable
   and the film fills the panel at its full resolution.
4. Confirm the idle screen's clock draws at the 1080p size.
