# The height estimate of a known object uses the wrong gap

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test read the `gap` of the last `.known-object` rule in `known.css`,
6 px, and wanted `center` for a kind and a name to be the frame, one
line, the name, and that gap. It failed with:

```
the box of a kind and a name is 77 px high, want 79 because known.css sets a gap of 6 px on .known-object
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should `fit.rs` keep its own copy of each size in `known.css`, with a
test that fails when they differ, or should the server set those
sizes in the CSS?

Whether `fit.rs` keeps its own copy of each CSS size, with a test that
reads `known.css` and fails when they differ, or the CSS takes its
sizes from the server.
