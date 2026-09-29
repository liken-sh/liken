// The button row a page draws under its text. A button is a box with a
// glyph and one word in it, drawn in the proportions the status line's
// marks take at their smaller size. The focused one carries the mark
// focus takes everywhere on this screen.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::icon::{self, Icon};
use super::{area, extent, label, mark, text};
use crate::look;

/// The height of a button.
pub const HEIGHT: f32 = 68.0;

// The gap between two buttons.
const GAP: f32 = 20.0;

// The padding at both ends of a button's glyph and word.
const PAD: f32 = 34.0;

/// The width of a button that holds this word and its glyph.
pub fn width(name: &str) -> f32 {
    let size = look::BUTTON;
    2.0 * PAD + icon::side(size) + icon::gap(size) + text::measured(name, size)
}

/// One button's box, in a row that starts at `at`.
pub fn button(names: &[&str], index: usize, at: Point) -> Rectangle {
    let left = names[..index]
        .iter()
        .fold(at.x, |left, name| left + width(name) + GAP);
    area(left, at.y, width(names[index]), HEIGHT)
}

// The box of a button's glyph, and the point its word starts at, centred
// on the button's height. The box is as wide as its content, so the
// content starts one padding in.
fn content(bounds: Rectangle) -> (Rectangle, Point) {
    let side = icon::side(look::BUTTON);
    let glyph = area(bounds.x + PAD, bounds.center_y() - side / 2.0, side, side);
    let words = Point::new(glyph.x + side + icon::gap(look::BUTTON), bounds.center_y());
    (glyph, words)
}

/// Draw the row of buttons, each a glyph and its word. `focus` names the
/// button that holds focus, or nothing while another row of the page
/// holds it. The glyph and the word take the full ink whether or not the
/// button holds focus, because every button of the row plays something.
/// The answer is the row's height, so the caller stacks the next block
/// under it.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    row: &[(Icon, &str)],
    at: Point,
    focus: Option<usize>,
) -> f32 {
    let names: Vec<&str> = row.iter().map(|(_, name)| *name).collect();
    for (index, (glyph, name)) in row.iter().enumerate() {
        let bounds = button(&names, index, at);
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        let (shape, words) = content(bounds);
        icon::draw(frame, *glyph, shape, look::text());
        frame.fill_text(label(
            name,
            words,
            look::BUTTON,
            look::text(),
            Alignment::Left,
            Vertical::Center,
            f32::INFINITY,
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

    #[test]
    fn the_icon_and_the_words_fill_the_button_between_its_padding() {
        let cases = ["Play", "Resume", "Start over", "Trailer"];
        for words in cases {
            let bounds = button(&[words], 0, Point::new(120.0, 800.0));
            let (glyph, words_at) = content(bounds);
            let ends = words_at.x + text::measured(words, look::BUTTON);
            assert_eq!(glyph.x, bounds.x + PAD, "{words}");
            assert_eq!(glyph.center_y(), bounds.center_y(), "{words}");
            assert_eq!(glyph.width, icon::side(look::BUTTON), "{words}");
            assert_eq!(words_at.x, glyph.x + glyph.width + icon::gap(look::BUTTON));
            assert!(
                (bounds.x + bounds.width - PAD - ends).abs() < 1e-3,
                "{words}"
            );
        }
    }
}
