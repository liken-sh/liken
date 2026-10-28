# 44, The display arranges its claim

Built on 2026-10-27. This is step 3 of
[root plan 85](../../../plans/completed/85-motion.md), motion on the screen. The
display binds `liken_arrangement_v1` on its own Wayland connection,
and when the up-next card rises for a film or an episode, it asks the
compositor to step the film back into a frame on the left half of the
screen. The display then draws the next work in the right half. The
unit tests and a fake compositor on a socket pair prove the decisions
and the binding. A drill on `liken-1` on 2026-10-27 proved the binding
inside the playback pod and the step-back on the screen. That the
close ends the binding's thread before winit disconnects is not
drilled.

## The problem

The up-next card is a 440 by 416 box in the lower right corner, over
the film. The design for
[library-operator plan 84](../../../library-operator/plans/84-a-larger-up-next-card.md)
gives the next work the right half of the screen at the credits, with
the film still playing in a frame on the left. The display cannot move
the film: it is a separate client, and mpv's own `video-zoom` moves
the picture only on the film's frames. Root plan 85 has the compositor
scale the film instead, and `liken-layout.so` offers
`liken_arrangement_v1` for that. This plan is the client side.

## The design

**The display is its claim's arranger.** The protocol gives a claim's
surfaces to one arranger: the first client of the claim that asks.
The display asks once, at start, so on a screen with the protocol it
is the arranger of its own playback pod. mpv owns no part of the
arrangement and receives no configure while its film moves.

**Two app_ids tell the surfaces apart.** An arrangement names surfaces
by their xdg app_id. `playerArgv` in `player.go` passes
`--wayland-app-id=liken-media-video` to mpv, and the display sets its
own window's app_id to `liken-media-overlay` through iced's window
settings (`settings` in `display/src/window.rs`). Both values are
constants in `display/src/arranger.rs`.

**The binding uses winit's connection.** The compositor tells clients
apart by their connection, so a second connection would be a second
client of the claim, and the display's own window would move with the
film. `arranger::link::attach` runs inside `iced::window::run`, on
winit's thread. It wraps winit's `wl_display` with
`Backend::from_foreign_display` and `Connection::from_backend`, makes a
new event queue on that connection, and starts a thread that blocks on
the queue. The thread lists the globals, binds
`liken_arranger_manager_v1`, asks for an arranger, and sends each event
to the frame loop over a channel. The frame loop sends each
arrangement from its own thread, because a proxy sends from any thread.
The binding attaches no object to the display's own `wl_surface`,
because winit already attaches a `wp_viewport` to it. The client code
is generated from the protocol's XML by `wayland-scanner` at build
time.

**The decisions are pure.** `display/src/arranger.rs` holds what the
compositor said and where the film stands, and answers each event and
each request with the arrangement to commit and the line to log. Its
tests open no connection.

| When | Arrangement | Tween |
|---|---|---|
| The film plays | none: every surface fills the region | |
| The card rises on a film or an episode | `stepped-back`: `liken-media-video` at 5 %, 25 %, 50 % by 50 % of the region (96, 270, 960 by 540 on a 1920 by 1080 region), then `liken-media-overlay` over the whole region, both `scale` | 900 ms, smoothstep |
| back with the OSD down, over the stepped-back film | `restored`: no entries | 600 ms, smoothstep |
| The region changes size while the film stands back | `stepped-back` at the new size | 0 ms |
| A select on the column | no commit: the film stays where it stands for the hand-off | |
| The offer leaves while the film stands back | `restored` | 600 ms, smoothstep |

The rectangle is a share of the region that the `region` event states,
so a 720p, a 4K, or an ultrawide region puts the film at the same
place. The display steps the film back only after `granted`, after a
`region` event, and while `surfaces` counts at least one
`liken-media-video` surface. Music and pictures keep the corner card,
because the display draws over the whole frame for them.

**The column waits for the landing.** The display draws no frame while
the compositor moves the film: the step back cancels the card's hide
window and puts the card's fade at its end at once, and the column
draws nothing until `landed` arrives for the step back's serial. Then
the column fades in on the display's usual fade. The column
(`display/src/upnext/column.rs`) takes the card's lines and the
card's picture at a larger size, on the 1920 by 1080 canvas: the art
at 704 by 396 at 1120, 222, the reason at 642, the title in the
`TITLE` type at 682, the detail at 762, and the line
"OK  Play now   ·   Back  Keep watching" at 808. The column's right
edge is the right margin, and the art decodes at the column's box
(`Art::frame_next`). A select takes the offer, as on the card, and
the taken column draws dimmed with "Starting" and no line of keys.

**back keeps watching first.** `Focus::nav` tries back's meanings in
order: close a chooser, cancel a scan, hide the OSD, return a
stepped-back film, and end the run. So over the stepped-back film, the
first back at the bare video returns the film and leaves it playing,
and the next back ends the run. The column and the card leave at once,
before the film moves. A taken offer still ends the run on back, as
before.

**Without an arranger, nothing changes.** A compositor that offers no
`liken_arranger_manager_v1`, a `refused` event, a window that is not
on Wayland, or a failed connection leaves the film at the default and
the corner card on its five-second window. A connection that fails
while the film stands back leaves the compositor to return the film,
and the offer falls back to the corner card.

**The binding closes before winit disconnects.** The display turns off
the toolkit's exit on a close request, and answers a close request
itself: `Link::close` destroys the arranger and the manager, sends a
`wl_display.sync` on the binding's queue, and waits up to one second
for the thread to end on its answer. Then the display asks the toolkit
to exit. A process that ends any other way, such as by the kubelet's
`SIGTERM`, disconnects, and the compositor returns every surface to
the default.

**One line for each change of state.** The display logs each change of
the arranger with its cause: granted, refused and why, absent, lost
and the client library's error, the region's size, the count of mpv's
surfaces, each commit with its serial, duration, and cause, and each
landing.

## What was considered and set aside

- **A second Wayland connection for the binding.** The compositor would
  read it as a second client of the claim, so the display's own surface
  would be arranged with the film, and the arranger would not be the
  client that draws the column.
- **Fade the column out while the film returns.** The column would
  draw about 36 frames during the 600 ms tween, and each one is a
  recomposite of the whole screen on a small machine. Root plan 85's
  rule is that the display draws only while something on it moves, and
  the film is what moves, so the column leaves in one frame.
- **Dispatch the binding's queue from a tokio task.** A task's end is
  tied to the runtime, not to winit's disconnect, so the order of the
  two would be harder to state. A thread that ends on a round trip
  gives one order.

## Left for library-operator plan 84

- **The rise rule.** This plan steps the film back whenever the card
  rises on a film or an episode, by the rules the card already
  follows: the credits marks, or the time rule. Plan 84 decides whether
  only confirmed credits with no scene after them step the film back,
  and what a rise on the time rule alone does.
- **The column's content.** The column reuses the card's lines. The
  runtime and the end time, the focus panel, the border and corner
  masks around the film, the staggered entrance of the art and the
  lines, and the hand-off after a select belong to plan 84's design.

## The proof

- `display/src/arranger.rs` tests the arrangement for each state, the
  rectangle on four region sizes, the serials, the landing, the
  restore, the region change, every fallback, and the line for each
  change.
- `display/src/arranger/link.rs` runs the real binding over a socket
  pair against `fakecompositor.rs`, which speaks the wire protocol: a
  grant with its region and surfaces, a refusal, a compositor with no
  global, a compositor that goes away, a commit with its entries and
  its landing, and a close that destroys the arranger before the
  manager and ends the thread.
- `display/src/arranger/protocol.rs` fails when the display's copy of
  the XML differs from `weston/layout/liken-arrangement-v1.xml`.
- `display/src/window/arranging.rs`, `focus.rs`, `upnext.rs`, and
  `upnext/column.rs` test the rise, the landing, the column, back
  twice, the select, an offer that leaves, a lost link, a music item,
  and the column's geometry.
- `player_test.go` checks the app_id in mpv's arguments, and
  `window.rs` checks the display's.

The drill on `liken-1`: play an episode with credits marks to its
credits on a screen whose compositor offers the protocol, watch the
film step back and the column fade in, press back and watch the film
return and play on, press back again to end the run, and read the
display's log for one line per change. Then repeat with a select on
the column, and once with `kubectl delete` on the playback pod while
the film stands back.

On `liken-1` on 2026-10-27, part of the drill ran on `stick-1` with
weston 16.0.2 and the built module, on an Adventure Time episode whose
`Play` carried an offer and no credits marks, so the card rose by the
series time rule. The display logged the arranger granted, the region
at 1920 by 1080, and one `liken-media-video` surface. When the card
rose it committed `stepped-back` over 900 ms, logged the landing, and
drew the next work in the right column beside the film. weston
refused no commit. The presses of Back and select and the delete of
the pod while the film stood back did not run.

A recording of the panel during a select on the column, later the same
day, showed that mpv quit about 0.5 s after the press, while the
display drew its waiting card over the media browser's loading curtain
for 5.5 s more, until the kubelet stopped the display's container. So
a select now hands the screen off: the display commits `handing-off`,
which fades the film and its backdrop to opacity 0 over 300 ms on the
ease-in curve, fades the column out on the same curve, and pauses mpv.
When mpv's socket closes, the display draws nothing. The browser's
curtain under the film names the episode that starts under the mark.
In the drill that followed, started from the browser and taken with a
select over the bus, the film and the column faded into the curtain
in about 300 ms, the curtain held for about 8 s, and the next episode's
first frame followed. The press of back over the stepped-back film and
the delete of the pod while it stood back still did not run.
