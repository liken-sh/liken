// The frame: the side margin every screen keeps, and the region a
// screen's content draws in. A page, a wall, and the home rows all take
// their region from here, so no screen states a margin of its own and
// the screens cannot drift apart.

use iced_winit::core::Rectangle;

use super::area;

/// The side margin in logical pixels. A person reads this panel from the
/// far end of a room, so every flush edge of every screen is this far
/// inside the panel. 96 is 5 percent of a 1920-wide frame, the usual
/// title-safe inset for a screen read from across a room.
///
/// Three programs draw on one panel and keep one margin: this browser,
/// the player's `mpv` display, and the idle screen between films. One
/// replaces another while a person watches, so a different margin would
/// show as a jump. The media operator states the same number in
/// `display/theme.lua` and in `idle/src/look.rs`.
pub const MARGIN_X: f32 = 96.0;

/// The part of a frame a screen's content draws in: the frame less the
/// margin at each side. A screen reads `x` and `width` from the answer
/// and never reads the margin. The vertical extent is the frame's own,
/// because what a screen keeps off the top and the bottom differs from
/// screen to screen and is not one measure.
pub fn region(bounds: Rectangle) -> Rectangle {
    area(
        bounds.x + MARGIN_X,
        bounds.y,
        (bounds.width - 2.0 * MARGIN_X).max(0.0),
        bounds.height,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_region_keeps_the_margin_at_both_sides() {
        let region = region(area(0.0, 0.0, 1920.0, 1080.0));

        assert_eq!(region.x, MARGIN_X);
        assert_eq!(region.x + region.width, 1920.0 - MARGIN_X);
        assert_eq!(region.y, 0.0);
        assert_eq!(region.height, 1080.0);
    }

    #[test]
    fn a_frame_narrower_than_two_margins_leaves_no_width() {
        let region = region(area(0.0, 0.0, 100.0, 1080.0));

        assert_eq!(region.width, 0.0);
    }
}
