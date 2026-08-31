//! The clock: the wall-clock time at the top right.
//!
//! A viewer reads the hour without leaving the screen. `display/clock.lua`
//! draws the end of the film beside the time as well, from the duration and the
//! position `mpv` reports. An idle client plays no film and has no duration to
//! read, so it draws the time alone.

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
/// The reading turns at a minute, and the answer is the next whole second.
/// The harness counts its clock from the first frame, and the wall clock's
/// minute lands anywhere inside that second, so a screen that woke once a
/// minute would draw the new minute up to a second late. The client also
/// drains the broker in `tick`, and `tick` runs on a frame, so a screen that
/// slept longer than this would stop reading the bus. The screen takes the
/// earliest second its elements name, so this one holds the loop's sleep to a
/// second whatever else the screen is drawing.
pub fn next_frame(at: f64) -> f64 {
    at.floor() + 1.0
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::core::Size;

    #[test]
    fn the_next_frame_is_the_next_whole_second() {
        assert_eq!(next_frame(0.0), 1.0);
        assert_eq!(next_frame(0.25), 1.0);
        assert_eq!(next_frame(11.75), 12.0);
    }

    #[test]
    fn the_time_hangs_from_the_top_right_margin() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!(layout.right(), 1780.0);
        assert_eq!(look::MARGIN_Y, 90.0);
    }
}
