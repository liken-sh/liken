// The button row a page draws under its text. A button is a box with one
// word in it. The focused one carries the mark focus takes everywhere on
// this screen.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{area, extent, label, mark};
use crate::look;

/// The height of a button.
pub const HEIGHT: f32 = 68.0;

// The gap between two buttons.
const GAP: f32 = 20.0;

// The padding at both ends of a button's word, and the width of an
// average glyph as a share of its size, which sizes the box to the word.
const PAD: f32 = 34.0;
const ADVANCE: f32 = 0.62;

/// The width of a button that holds this word.
pub fn width(name: &str) -> f32 {
    name.chars().count() as f32 * look::BUTTON * ADVANCE + 2.0 * PAD
}

/// One button's box, in a row that starts at `at`.
pub fn button(names: &[&str], index: usize, at: Point) -> Rectangle {
    let left = names[..index]
        .iter()
        .fold(at.x, |left, name| left + width(name) + GAP);
    area(left, at.y, width(names[index]), HEIGHT)
}

/// Draw the row. `focus` names the button that holds focus, or nothing
/// while another row of the page holds it. The answer is the row's
/// height, so the caller stacks the next block under it.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    names: &[&str],
    at: Point,
    focus: Option<usize>,
) -> f32 {
    for (index, name) in names.iter().enumerate() {
        let bounds = button(names, index, at);
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        frame.fill_text(label(
            name,
            Point::new(bounds.center_x(), bounds.center_y()),
            look::BUTTON,
            look::text(),
            Alignment::Center,
            Vertical::Center,
            bounds.width,
        ));
        if focus == Some(index) {
            mark(frame, bounds);
        }
    }
    HEIGHT
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_longer_word_takes_a_wider_button() {
        assert!(width("Trailer") > width("Play"));
    }

    #[test]
    fn the_buttons_sit_beside_each_other_in_order() {
        let names = ["Play", "Trailer"];
        let at = Point::new(120.0, 800.0);
        let first = button(&names, 0, at);
        let second = button(&names, 1, at);
        assert_eq!(first.x, 120.0);
        assert_eq!(second.x, first.x + first.width + GAP);
        assert_eq!(second.y, first.y);
        assert_eq!(first.height, HEIGHT);
    }
}
