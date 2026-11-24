# The up-next focus panel can cover the current time

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

In `display/src/upnext/column.rs`, the comment on `ART_Y` says that
near the credits the scrubber draws the current time under the
column's right end, "from y 870 down to the bar, and the focus panel
has to end above it". `FOCUS_BOTTOM`, the bottom edge of the focus
panel, is 882. The scrubber's line box for the current time starts at
`scrubber::TOP`, which is `theme::BAR_Y` (928) minus `TIME_ABOVE` (18)
minus `theme::type_scale::LABEL` (40), so 870. The panel and the time
overlap by 12 pixels when both draw at the same x.

`upnext.rs` says the box around the up-next label spans y 824 to 870,
so 870 is also another element's edge, and the comment may name the
wrong one.

## Evidence

- `column.rs`: `const FOCUS_BOTTOM: f32 = 882.0;` and the compile-time
  assert checks only that `FOCUS_BOTTOM` is below the end-time line.
- The test `the_art_top_and_the_focus_panel_bottom_keep_their_positions`
  compares the constants with the same literals, so it does not check
  the edge that the comment names.

## The reproduction

A test in `upnext/column.rs` asserts that `FOCUS_BOTTOM` is at or
above `scrubber::TOP`, the top of the current time's text. It failed
with the numbers of 2026-11-19, before the scrubber moved 24 pixels
down:

```
the focus panel ends at y 858, want at or above y 846, where the scrubber draws the current time
```

The test is not in the tree, because a failing test cannot land on
`main`. It compares the two constants and does not show that the
panel and the time draw at the same x. A capture of the display with
the up-next column focused near the credits, at a position where the
current time is under the column, would show the overlap.

## What a fix has to decide

Near the credits, the focus panel of the up-next column can cover the
top of the current time.

Which is right, the comment or the constant. If the comment is right,
the panel ends at 870 or above, and the art and the lines above it
move up, as the comment says a new line does.
