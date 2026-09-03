// The band across the top of a wall: the library and its count at the
// left, and the three controls at the right. The controls draw dimmed
// because none of the three exists yet. The band is the place every wall
// reserves for them, so a person learns one place.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{area, extent, label, mark};
use crate::look;

/// The height the band takes off the top of the frame.
pub const HEIGHT: f32 = 84.0;

/// The three controls, in the order left and right move across them.
pub const CONTROLS: [&str; 3] = ["Sort", "Filter", "Search"];

// The margin at both ends of the band.
const PAD: f32 = 32.0;

// The width of one control's box, and the gap between two of them.
const CONTROL: f32 = 168.0;
const GAP: f32 = 20.0;

// The height of a control's box inside the band.
const BOX: f32 = 48.0;

/// One control's box, placed from the right edge of a band this wide.
pub fn control(width: f32, index: usize) -> Rectangle {
    let row = CONTROLS.len() as f32 * CONTROL + (CONTROLS.len() - 1) as f32 * GAP;
    let left = width - PAD - row;
    area(
        left + index as f32 * (CONTROL + GAP),
        (HEIGHT - BOX) / 2.0,
        CONTROL,
        BOX,
    )
}

/// Draw the band. `heading` is the library and its count. `focus` names
/// the control that holds focus, or nothing while the posters below hold
/// it.
pub fn draw(frame: &mut canvas::Frame<Renderer>, width: f32, heading: &str, focus: Option<usize>) {
    frame.fill_text(label(
        heading,
        Point::new(PAD, HEIGHT / 2.0),
        look::NAME,
        look::text(),
        Alignment::Left,
        Vertical::Center,
        width / 2.0,
    ));

    for (index, name) in CONTROLS.iter().enumerate() {
        let bounds = control(width, index);
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        frame.fill_text(label(
            name,
            Point::new(bounds.center_x(), bounds.center_y()),
            look::CONTROL,
            look::muted(),
            Alignment::Center,
            Vertical::Center,
            bounds.width,
        ));
        if focus == Some(index) {
            mark(frame, bounds);
        }
    }

    let rule = area(0.0, HEIGHT - 2.0, width, 2.0);
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_controls_sit_in_order_against_the_right_edge() {
        let first = control(1920.0, 0);
        let last = control(1920.0, CONTROLS.len() - 1);
        assert!(first.x < last.x);
        assert_eq!(last.x + last.width, 1920.0 - PAD);
        assert_eq!(first.y, last.y);
    }

    #[test]
    fn a_control_fits_inside_the_band() {
        let bounds = control(1920.0, 1);
        assert!(bounds.y > 0.0);
        assert!(bounds.y + bounds.height < HEIGHT);
    }
}
