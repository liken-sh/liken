//! The wall-clock time, drawn at the top right.

use iced_winit::core::Point;

use super::{Frame, Layout};
use crate::clock::now;
use crate::look;
use crate::unit::Unit;

/// Draws the time at the top right margin, in canvas units. The
/// activity line and the volume row draw below it.
pub fn draw(frame: &mut Frame, layout: &Layout, _unit: &Unit, _at: f64, light: f32) {
    frame.fill_text(look::line(
        now().twelve_hour(),
        Point::new(layout.right(), look::MARGIN_Y),
        look::Anchor::TopRight,
        look::SMALL,
        look::under(look::text(), light),
    ));
}

/// The second at which the displayed time next changes, for
/// [`super::Idle::next_frame`]. The time shows hours and minutes, so it
/// changes when the wall clock's minute turns. `into_minute` is how many
/// seconds the wall clock is into its minute at `at`.
pub fn next_frame(at: f64, into_minute: f64) -> f64 {
    at + 60.0 - into_minute.clamp(0.0, 60.0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::core::Size;

    #[test]
    fn the_next_frame_is_when_the_wall_clocks_minute_turns() {
        assert_eq!(next_frame(0.0, 0.0), 60.0);
        assert_eq!(next_frame(0.25, 45.25), 15.0);
        assert_eq!(next_frame(11.75, 59.5), 12.25);
    }

    #[test]
    fn the_time_is_anchored_at_the_top_right_margin() {
        let layout = Layout::for_surface(Size::new(1920.0, 1080.0));
        assert_eq!(layout.right(), 1824.0);
        assert_eq!(look::MARGIN_Y, 90.0);
    }
}
