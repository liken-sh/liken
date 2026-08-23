# The scrubber a remote summons

Plan 07-a, the first slice of [plan 07](07-the-player-draws-its-own-display.md).
It is the core the rest of the display grows from: a script `liken`
writes, drawn over `mpv` through `libass`, summoned by a press and
scrubbed by a remote. When this slice lands, a film plays under a
`liken` scrubber that shows the time, the playhead, and a seek that
glides, and a plain `mpv` OSD is gone.

## The problem

The whole display in plan 07 rests on one path that does not exist yet:
a script `liken` owns, loaded into `mpv`, drawing through `libass`, and
driven by a press that arrives over the command bus. Until that path
runs end to end, none of the layout, the choosers, or the art can be
built or drilled. This slice builds the path and proves it with the one
element that needs nothing but `mpv` itself: the fine scrubber.

## The script directory

The display is a script directory, `main.lua` and a few modules, loaded
as one `mpv` client. The parent design states why: one client so the
modules share state by a function call, and a directory so `main.lua`
reaches its parts with plain `require`. This slice creates the
directory with the modules the scrubber needs and no others:

* `main.lua`, the frame loop that gathers each module's ASS and updates
  the one overlay.
* `theme.lua`, the color palette, the type scale, and the drawing
  primitives.
* `focus.lua`, the input router. In this slice it holds one region, the
  scrubber, and whether the OSD is summoned.
* `seekbar.lua`, the fine scrubber: its geometry, its time labels, the
  playhead, and the accelerating seek.

The script draws through `osd-overlay` with `format: ass-events`, and
draws no bitmap, so this slice needs neither `overlay-add` nor the
bridge decode.

## The scrubber draws from mpv's properties

The scrubber needs no `presentation` block. It reads `duration`,
`time-pos`, and `percent-pos` from `mpv` and draws the bar, the
playhead, and the time labels from them. The labels show the position,
the time remaining, and the total length. `mpv` sends each property
once at observe time and then on every change, so the script draws from
the values it is given and runs no timer of its own for them.

The time-of-day cluster is out of this slice, because it is a separate
module and adds nothing to the path this slice proves.

## The seek glides

`left` and `right` seek, and the seek accelerates the longer a
direction is held. A tap nudges the position a few seconds, and a hold
ramps the step so the cursor glides across an hour. Playback follows
the cursor with a short debounce, so there is no separate commit step.
This is the scrub behavior the parent design states, built here as the
first thing the remote drives.

## The navigation actions reach the bus

This slice adds the navigation actions to the command vocabulary the
bus already carries: `up`, `down`, `left`, `right`, `select`, and
`back`. A `Keymap` binds a controller's buttons to them exactly as it
binds `play-pause` today. This slice uses `left`, `right`, and the
summon and dismiss; the later slices use the rest, and defining the
whole set now keeps one change to the vocabulary rather than six.

A press reaches the display over the path plan 05 built. The translator
sidecar publishes the command, the command sidecar reads it, and it
drives the display over `mpv`'s IPC socket with `script-message-to`.
The display is one more consumer of the command topic, so a gamepad and
a remote both drive the scrubber with no new path.

## The OSD is summoned

The OSD is hidden while the film plays. A press summons it, and a pause
summons it. It hides again after a few idle seconds of play. The main
button stays play-pause, and pausing brings the scrubber up, so the one
press both pauses and shows where the film is.

## Set aside for this slice

* **The vertical stack.** This slice has one region, the scrubber.
  `up` and `down` walk regions in 07-b, once there is a second region
  to walk to.
* **The chapter row and the choosers.** They arrive in 07-b.
* **The header, the art, and the trickplay tile.** They need bitmaps or
  the contract, and they arrive in 07-c through 07-e.
* **The time-of-day cluster.** A small module, added with the other
  layout modules in a later slice.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A film plays from a `Play`.

The drill checks each claim:

* The film plays under a `liken` scrubber, not the plain `mpv` OSD. The
  script `liken` owns is on screen.
* A press summons the scrubber, and it hides again after a few idle
  seconds. The summon path runs.
* `left` and `right` scrub, and a hold accelerates the seek. The
  playhead and the time labels follow. The remote drives the display
  over the bus.
* The operator is killed, and a press still summons the scrubber and
  scrubs, because the bridge and the translator run in the pod. The
  data plane outlives the control plane.
