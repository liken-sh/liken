# The power key's browser half is not proved

Open problem. [Plan 46](../completed/46-the-power-key-brings-the-room-up.md)
built the receiver half on 2026-09-07: a press of the X6's power key
raises the panel desire, the `Receiver` session carries an `awake`
flag, and the equipment operator powers the receiver on and selects
the input. The plan's other half, that the media browser is the thing
on the screen when the panel lights and holds the focus, is drilled in
neither case the plan names.

## The evidence

Nothing in this repository reads an `awake` edge. The word appears in
no Go file and in no Rust file here. The browser's cue is the shade
moment the `media-screen` crate makes: a press while the screen sleeps
sets `Moment::Wake`, and the browser answers it with
`refresh.shade(false)`, `refresh.cover(false)`, `presented()`, and
`lifted()`, which reads the home page again. The same crate publishes
the panel `ON` desire on that press, and the desire is what the media
operator turns into the session's `awake` flag.

So the browser has a cue on the press, and neither of plan 46's two
cases is drilled:

* The panel wakes while a `Play` still stands. The film should be on
  top and hold the focus, and the browser should stay covered.
  `Moment::Wake` calls `refresh.cover(false)` whatever the activity
  is, and the next status is what covers the browser again.
* The panel wakes after a `Play` ended while the panel was dark. The
  browser should be presented and hold the focus, with no film under
  it.

## Why the plan's shape no longer fits

Plan 46's third contract bullet says the browser reads the `awake`
edge from the bus and re-presents itself on it when no `Play` stands.
[Plan 54](../completed/54-the-browser-returns-on-the-status-edge.md)
removed the re-present: `Moment::Present`, `surface_due`,
`surface_pending`, `represent`, and the held `wgpu::Instance` are
gone, the browser reads nothing off the commands topic, and under
ivi-shell the compositor shows the browser's window again the moment
the film's surface goes. The browser maps no fresh window, and it
passes no app-id, so there is nothing for an `awake` edge to
re-present. Focus is the compositor's, not the browser's.

## What would settle it

A drill on the living room, in both cases. Dark panel with no `Play`:
press power, and read whether the browser draws and takes the presses.
Dark panel with a film paused: press power, and read whether the film
holds the screen and the browser stays covered. If both hold, the
answer is that the shade moment already does the browser's half, and
the open problem closes with the drill written down. If one fails, the
fix is a new contract bullet in the shape plan 54 left: an edge the
browser already reads, and no window of its own.
