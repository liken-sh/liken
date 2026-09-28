//! The clock: the wall-clock time at the top right.
//!
//! A viewer reads the hour without leaving the screen. An idle client plays no
//! film and has no duration to read, so it draws the time alone.

use iced_winit::core::Point;

use super::{Frame, Layout};
use crate::clock::now;
use crate::look;
use crate::unit::Unit;

/// Draw the element into the frame, in canvas units.
///
/// The time hangs from the top margin at the right, the corner the activity
/// line and the volume row hang under. The anchor is the top of the line box,
/// so the tallest glyph of the face lands on the margin.
pub fn draw(frame: &mut Frame, layout: &Layout, _unit: &Unit, _at: f64, light: f32) {
    frame.fill_text(look::line(
        now().twelve_hour(),
        Point::new(layout.right(), look::MARGIN_Y),
        look::Anchor::TopRight,
        look::SMALL,
        look::under(look::text(), light),
    ));
}

/// The second the clock next changes, for [`super::Idle::next_frame`].
///
/// The reading shows hours and minutes, so it changes when the wall clock's
/// minute turns and at no other time. `into_minute` is how far the wall clock
/// is into its minute at `at`, and the answer is the second on the screen's
/// own clock where that minute ends, so a settled screen draws once a
/// minute. A frame every second would draw 59 identical frames a minute, and
/// each one wakes the GPU on a machine with nothing else to do.
///
/// The harness wakes more often than this to read the bus, and a wake with
/// nothing due draws nothing. `harness::timeline::BACKSTOP` states that
/// bound.
pub fn next_frame(at: f64, into_minute: f64) -> f64 {
    at + 60.0 - into_minute.clamp(0.0, 60.0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::core::Size;

    #[test]
    fn the_next_frame_is_where_the_wall_clocks_minute_turns() {
        assert_eq!(next_frame(0.0, 0.0), 60.0);
        assert_eq!(next_frame(0.25, 45.25), 15.0);
        assert_eq!(next_frame(11.75, 59.5), 12.25);
    }

    #[test]
    fn the_time_hangs_from_the_top_right_margin() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!(layout.right(), 1824.0);
        assert_eq!(look::MARGIN_Y, 90.0);
    }
}
