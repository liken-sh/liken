# The power key brings the room up

Plan 46. One press of the remote's power key turns the receiver on,
selects the input the machine is on, and puts the media browser on
the screen, in focus. The key does not turn anything off yet.

## The problem

A living room has more to wake than a panel. The X6's power key
today asks the panel to wake through the media bus, and the media
operator turns that desire into an `awake` flag on the `Receiver`
session, so the equipment operator powers the receiver on and selects
the input. That half shipped on 2026-09-07. What this plan owes is the
browser's half: that the browser is the thing on the screen when the
panel lights, with the remote's focus on it, every time.

Two cases are not proved yet:

* The panel wakes while a `Play` still stands. The film should be on
  top and hold the focus, and the browser should stay covered.
* The panel wakes after a `Play` ended while the panel was dark. The
  browser should be presented and hold the focus, with no film under
  it.

## The contract

* A power press at a dark screen wakes the panel, the receiver, and
  the input, and ends with the browser presented and focused.
* A power press at a lit screen asks for the shade, as today. It
  sends the receiver nothing. Turning the receiver off is a later
  plan, because the room may be listening to something else.
* The browser reads the `awake` edge from the bus the same way the
  equipment operator reads it from the session, and re-presents
  itself on it when no `Play` stands.

## The proof

Drill on the living room: dark panel, press power, read
`kubectl get receivers` for the input and the browser pod's log for
the present, and watch the screen. Then the same with a film paused
under it.
