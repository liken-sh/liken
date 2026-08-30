# The idle screen in Iced

Plan 05. The client's first view, and the first plan that changes
`media-operator`. At the end of this plan a `Player` can name an image
for its idle screen. This repository's media browser image draws the
idle screen that `media-operator` draws today, on the same bus contract,
and `mpv` and Lua are out of that pod.

## The problem

The idle screen is `media-operator`'s: an `mpv` with no file and a Lua
overlay under `display/`, driven through `mpv`'s IPC socket by an
idle-command sidecar that owns the bus. A media browser has to draw on
the same screen, in the same pod, between plays. Two clients on one
surface is a handoff. One native client drew the idle screen in the
toolkit head-to-head at 87 MB resident and 0.63 ms per frame. So the
media browser is the idle screen, and the idle screen is the media
browser at rest.

## The change below: the idle client image

`media-operator`'s idle pod runs the operator's own image for its idle
container. This plan adds one field to `Player` under `spec.idle` that
names an image, defaulting to the operator's own. The idle container
runs that image with the same claim, the same environment, and the same
sidecar as today. The idle-command sidecar is unchanged: it keeps the
clock, the fade and off timers, and the panel power. The field is
generic. `media-operator` gains no notion of libraries, only that the
idle client can be another program.

## The contract between the sidecar and a client

Today the sidecar drives the idle screen through `mpv`'s IPC socket with
script messages: the `Player` status, the focus pulse, the shade drawn
and lifted, the volume level, and a surface recreate. Every input the
sidecar consumes is already a bus topic. So the contract for a client
that is not `mpv` is the bus.

- The client subscribes to the `Player`'s status, commands, focus, and
  volume topics, which exist today, and draws from them.
- The sidecar publishes its own outputs, the shade drawn or lifted and
  the wake, on one new retained topic under the `Player`, instead of a
  call to `mpv`. The stock Lua screen keeps working through the
  sidecar's `mpv` adapter, which becomes a reader of that topic.
- The client publishes one retained mark that names its view, idle or
  browsing, so the `Player` status can show that a screen is browsing. A
  client that never browses never publishes it.

One question must be answered in `media-operator` before the media
browser can navigate. Are the navigation presses published on the
commands topic while the `Player` is idle, or does the sidecar consume
them for its own toggles? If the sidecar consumes them, the media
browser has no arrow keys, and the fix is part of this plan.

## The client

The media browser image, from this repository, draws the idle screen:
the black ground, the mark at center, the clock at the top right in the
`Player`'s zone, and the unit's name and parts at the bottom left. It
draws every motion the Lua draws, taken from `theme.lua` and the display
modules. The palette, the type scale, the margins, and the font, `Source
Sans 3`, are the display's values. The head-to-head drew all of it and
recorded what a builder needs before starting.

- Iced's renderer draws one layer in a fixed order, shapes under text,
  so a shade drawn as a black rectangle never covers the clock. The
  screen fades by drawing everything fainter, or it puts the shade in
  its own layer.
- The window must request no decorations, or the toolkit draws a title
  bar and the surface is 35 rows short of 1080.
- Rust's standard library has no time zones. The clock reads the zone's
  TZif file or uses a crate.
- Every animation is a function of the wall clock and the events before
  it, never a counter advanced per frame. That is what makes a captured
  frame reproducible and a dropped frame harmless, and the harness flags
  depend on it.
- `mpv`'s overlay composites in sRGB and the GPU toolkit in linear
  light. The fade curves in `theme.lua` do not port as numbers; they
  need a timing pass by eye on a real screen.
- The toolkit's high-level entry point exposes no frame. The harness
  that captures frames and measures runs the window loop by hand, in the
  shape of the toolkit's own integration example. That is harness cost.

## The local harness

`local/idle` does what `media-operator/local/idle` does: it opens the
screen in a window on the workstation, takes the unit's name and parts
as arguments, and binds the same preview keys so each animation can be
seen. Under `cage` on the headless backend the same binary captures
frames to files.

## What was set aside

Two clients on one surface, an `mpv` at rest and a media browser for
browsing: one more claim, one handoff, and one more process.

The sidecar's `mpv` adapter as the only path. A client that had to be
`mpv` on a socket could never be swapped.

## Proof

On `liken-1`: a `Player` names the media browser image, the idle pod
rolls, and the screen shows the idle view with the clock in the right
zone and the unit's parts. A `Play` starts and ends, and the mark moves
and rests through the status topic as it does today. A press wakes the
shade through the sidecar's new topic. A `Player` with the field unset
runs the stock Lua screen unchanged. The media browser's resident memory
on the box is recorded beside the head-to-head's 87 MB.
