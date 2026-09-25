# The power key's effect on the media browser is not tested

Open problem. [Plan 46](../completed/46-the-power-key-brings-the-room-up.md)
built the receiver half on 2026-09-07. A press of the X6's power key
publishes a panel desire of `ON`, which is a request to turn the panel
on. The `Receiver` session gets an `awake` flag, and the equipment
operator powers the receiver on and selects the input. The plan's other
half says that the media browser is visible and has focus when the
panel turns on. No drill has tested that half in either of the two
cases that the plan names.

## The evidence

Nothing in this repository reads the change of the `awake` flag to on.
The word `awake` appears in no Go file and in no Rust file here. The
browser reacts instead to a shade moment from the `media-screen` crate.
The shade is the overlay that covers a sleeping screen, and a
shade moment is the crate's signal that the shade changed. A press
while the screen sleeps sets `Moment::Wake`. The browser handles it
with `refresh.shade(false)`, `refresh.cover(false)`, `presented()`, and
`lifted()`, which reads the home page again. On that same press, the
crate publishes the panel desire `ON`, and the media operator turns
that desire into the session's `awake` flag.

So the browser reacts to the press, and no drill has tested either of
plan 46's two cases:

* The panel wakes while a `Play` still exists. The film should be on
  top and have the focus, and the browser should stay covered.
  `Moment::Wake` calls `refresh.cover(false)` whatever the activity
  is, and the next status covers the browser again.
* The panel wakes after a `Play` ended while the panel was dark. The
  browser should be presented and have the focus, with no film under
  it.

## Why plan 46's design no longer fits

Plan 46's third contract bullet says that the browser reads the change
of `awake` to on from the bus and presents itself again when no `Play`
exists.
[Plan 54](../completed/54-the-browser-returns-on-the-status-edge.md)
removed that re-present path. `Moment::Present`, `surface_due`,
`surface_pending`, `represent`, and the retained `wgpu::Instance` are
gone, and the browser reads nothing from the commands topic. Under
ivi-shell, the compositor shows the browser's window again as soon as
the film's surface closes. The browser maps no new window and passes no
app-id, so a change of `awake` would have nothing to present again. The
compositor sets the focus, and the browser does not.

## The drill that would close this problem

A drill on the living room screen, in both cases. With the panel dark
and no `Play`: press power, and check whether the browser draws and
receives the presses. With the panel dark and a film paused: press
power, and check whether the film stays on the screen and the browser
stays covered. If both pass, the shade moment already does the
browser's half, and the open problem closes with the drill recorded. If
one fails, the fix is a new contract bullet that fits the design plan
54 left: the browser reacts to a signal that it already reads, and it
opens no window of its own.
