# The idle screen in Iced

Plan 05. The browser's first face, and the first plan that changes
`media-operator`. At the end of this plan a `Player` can name an
image for its idle screen, and this repository's browser image draws
the idle screen `media-operator` draws today, on the same bus
contract, with `mpv` and Lua out of that pod.

## The problem

The idle screen is `media-operator`'s: an `mpv` with no file and a
Lua overlay under `display/`, driven through `mpv`'s IPC socket by
an idle-command sidecar that owns the bus. A browser has to draw on
that same screen, in the same pod, between plays. It could draw
beside `mpv`, or it could be the idle screen. Two clients on one
surface is a handoff, and the toolkit spike showed that one native
client draws the idle screen at 87 MB resident and under a
millisecond a frame. So the browser is the idle screen, and the
idle screen is the browser at rest.

## The change below: the idle client image

`media-operator`'s idle pod runs the operator's own image for its
idle container, hard-wired. This plan adds one field to `Player`
under `spec.idle` that names an image, defaulting to the operator's
own. The idle container runs that image with the same claim, the
same environment, and the same sidecar as today. The idle-command
sidecar is unchanged and keeps the clock, the fade and off timers,
and the panel power. The field is generic: `media-operator` learns
nothing about libraries, only that the idle client can be another
program.

## The contract between the sidecar and a client

Today the sidecar reaches the idle screen through `mpv`'s IPC socket
with script messages: the `Player` status, the focus pulse, the shade
drawn and lifted, the volume level, and a surface recreate. A client
that is not `mpv` needs those over the bus, which the sidecar already
reads everything from. So the contract is the bus:

- The client subscribes to the `Player`'s status, commands, focus, and
  volume topics, which exist today, and draws from them.
- The sidecar publishes its own decisions, the shade drawn or lifted
  and the wake, on one new retained topic under the `Player`, instead
  of calling `mpv`. The stock Lua screen keeps working through the
  sidecar's `mpv` adapter, which becomes a reader of that same topic.
- The client publishes one retained mark that names its face, rest or
  browse, so the `Player` status can show that a screen is browsing.
  A client that never browses never publishes it.

One thing this plan must verify before the browser can navigate:
whether the commands topic carries the navigation presses while the
`Player` is idle, or whether the sidecar consumes them for its own
toggles. If the sidecar swallows them, the browser has no arrow keys,
and the fix belongs in `media-operator` in this plan.

## The client

The browser image, from this repository, reproduces the idle screen:
the black ground, the mark at center, the clock at the top right in
the `Player`'s zone, the unit's name and parts at the bottom left, and
every motion the Lua draws, taken from `theme.lua` and the display
modules. The palette, the type scale, the margins, and the font,
`Source Sans 3`, are the display's numbers. The spike drew all of it
and found what a builder should know before starting:

- Iced's renderer draws one layer in a fixed order, shapes under text,
  so a shade drawn as a black rectangle never covers the clock. The
  screen fades by drawing everything fainter, or it puts the shade in
  its own layer.
- The window must ask for no decorations, or the Wayland toolkit
  draws a title bar and the surface comes back 35 rows short.
- Rust's standard library has no time zones; the clock reads the
  zone's TZif file itself or takes a crate.
- Every animation is a function of the wall clock and the events
  before it, never a counter advanced per frame. That is what makes a
  captured frame reproducible and a dropped frame harmless, and it is
  what the harness flags depend on.
- `mpv`'s overlay composites in sRGB and the GPU toolkit in linear
  light, so the fade curves in `theme.lua` do not port as numbers;
  they need a timing pass by eye on a real screen.
- The high-level entry point of the toolkit hands over no frame; the
  harness that captures frames and measures runs the window loop by
  hand, in the shape of the toolkit's own integration example. That
  is harness cost, not client cost.

## The local harness

`local/idle` in this repository does what `media-operator/local/idle`
does: opens the screen in a window on the workstation, takes the unit
name and parts as arguments, and binds the same preview keys so each
animation can be seen. Under `cage` on the headless backend the same
binary captures frames to files, since the workstation has no screen
grabber for that compositor.

## What was set aside

Two clients on one surface, an `mpv` for rest and a browser for
browsing. One more claim, one handoff, and one more process for
nothing one client cannot do.

Keeping the sidecar's `mpv` adapter as the only path. A client that
had to be `mpv` on a socket could never be swapped, and the field
above would be a lie.

## Proof

On `liken-1`: a `Player` names the browser image, the idle pod rolls,
and the screen shows the idle face with the clock in the right zone
and the unit's parts. A `Play` starts and ends, and the mark moves and
rests through the status topic as it does today. A press wakes the
shade through the sidecar's new topic. A `Player` with the field unset
still runs the stock Lua screen unchanged. The browser's resident
memory on the box is recorded beside the spike's 87 MB.
