# The height estimate of a known object uses the wrong gap

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`center` in `crates/transect-map/src/map/known/fit.rs` estimates the
height of the object box in the middle of a known kind's diagram. It
adds `GAP`, 4 px, between the box's items. `known.css` beside it sets
`gap: 6px` on `.known-object`, so the estimate is 2 px short for each
gap, up to 6 px for a box with a kind, a name, a calm reason, and
chips. The module doc says that the estimates round up so that a box
has room to spare, and the server sets the box's height from the
estimate, so a box that comes out short clips its last line.

`FRAME` counts a 2 px border for every box, but `.known-box` has a 1 px
border, so `relation` estimates 2 px more than it needs. That direction
does no harm.

## What would confirm it

Render a known kind's page in a browser with an object that has a calm
reason and several chips, and compare the box's content height with the
height that `center` returns.

## What a fix has to decide

Whether `fit.rs` keeps its own copy of each CSS size, with a test that
reads `known.css` and fails when they differ, or the CSS takes its
sizes from the server.
