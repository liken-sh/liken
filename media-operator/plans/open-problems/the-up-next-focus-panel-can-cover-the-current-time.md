# The up-next focus panel can cover the current time

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

In `display/src/upnext/column.rs`, the comment on `ART_Y` says that
near the credits the scrubber draws the current time under the
column's right end, "from y 846 down to the bar, and the focus panel
has to end above it". `FOCUS_BOTTOM`, the bottom edge of the focus
panel, is 858. The scrubber's line box for the current time starts at
`scrubber::TOP`, which is `theme::BAR_Y` (904) minus `TIME_ABOVE` (18)
minus `theme::type_scale::LABEL` (40), so 846. The panel and the time
overlap by 12 pixels when both draw at the same x.

`upnext.rs` says the box around the up-next label spans y 800 to 846,
so 846 is also another element's edge, and the comment may name the
wrong one.

## Evidence

- `column.rs`: `const FOCUS_BOTTOM: f32 = 858.0;` and the compile-time
  assert checks only that `FOCUS_BOTTOM` is below the end-time line.
- The test `the_art_top_and_the_focus_panel_bottom_keep_their_positions`
  compares the constants with the same literals, so it does not check
  the edge that the comment names.

## What would confirm it

A capture of the display with the up-next column focused near the
credits, at a position where the current time is under the column. A
test can compare `FOCUS_BOTTOM` with `scrubber::TOP`.

## What a fix has to decide

Which is right, the comment or the constant. If the comment is right,
the panel ends at 846 or above, and the art and the lines above it
move up, as the comment says a new line does.
