// The button row a page draws under its text. A button is a box with a
// glyph and one word in it, drawn in the proportions the status line's
// marks take at their smaller size. The row draws no focus mark: the
// browser's outline goes around the box `button` answers for the button
// that holds focus.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::icon::{self, Icon};
use super::{area, clock, extent, label, text};
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

/// Draw the row of buttons, each a glyph and its word. The glyph and the
/// word take the full ink whether or not the button holds focus, because
/// every button of the row plays something. The answer is the row's
/// height, so the caller stacks the next block under it.
pub fn draw(frame: &mut canvas::Frame<Renderer>, row: &[(Icon, &str)], at: Point) -> f32 {
    let names: Vec<&str> = row.iter().map(|(_, name)| *name).collect();
    for (index, (glyph, name)) in row.iter().enumerate() {
        let bounds = button(&names, index, at);
        frame.fill_rectangle(bounds.position(), extent(bounds), look::slot());
        let (shape, words) = content(bounds);
        icon::draw(frame, *glyph, shape, look::text());
        crate::views::ink::ink(
            frame,
            label(
                name,
                words,
                look::BUTTON,
                look::text(),
                Alignment::Left,
                Vertical::Center,
                f32::INFINITY,
            ),
        );
    }
    HEIGHT
}

// The gap between the row's last button and the words beside it, the gap
// the status line keeps between its words and its first mark.
const ASIDE_GAP: f32 = 28.0;

/// The box the words beside a row of these buttons draw in: a gap after
/// the last button, as tall as a button, and as wide as the words at the
/// status line's size. Nothing where the words would end past `right`, so
/// a wide row drops its aside and never pushes it off the text column,
/// and nothing where there are no words.
pub fn aside(names: &[&str], at: Point, words: &str, right: f32) -> Option<Rectangle> {
    let start = match names.len() {
        0 => at.x,
        count => {
            let last = button(names, count - 1, at);
            last.x + last.width + ASIDE_GAP
        }
    };
    let width = text::measured(words, look::STATUS);
    (!words.is_empty() && start + width <= right).then(|| area(start, at.y, width, HEIGHT))
}

/// Draw words beside a row of buttons in the muted ink, such as the time a
/// play the row starts would end, where [`aside`] gives them room. `halo`
/// draws them over dark copies, for a row that stands over art past the
/// scrim's full shade.
pub fn draw_aside(
    frame: &mut canvas::Frame<Renderer>,
    names: &[&str],
    at: Point,
    words: &str,
    right: f32,
    halo: bool,
) {
    let Some(bounds) = aside(names, at, words, right) else {
        return;
    };
    let start = Point::new(bounds.x, bounds.center_y());
    let ink = |point: Point, color| {
        label(
            words,
            point,
            look::STATUS,
            color,
            Alignment::Left,
            Vertical::Center,
            f32::INFINITY,
        )
    };
    if halo {
        for point in clock::halo(start) {
            crate::views::ink::ink(frame, ink(point, look::BACKGROUND));
        }
    }
    crate::views::ink::ink(frame, ink(start, look::muted()));
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
    fn the_words_beside_the_row_stand_a_gap_after_its_last_button() {
        let names = ["Resume", "Start over"];
        let at = Point::new(96.0, 600.0);
        let last = button(&names, 1, at);
        let words = aside(&names, at, "ends 10:42 pm", 1920.0).expect("room");
        assert_eq!(words.x, last.x + last.width + ASIDE_GAP);
        assert_eq!(words.center_y(), last.center_y());
        assert_eq!(words.width, text::measured("ends 10:42 pm", look::STATUS));
    }

    #[test]
    fn words_that_would_run_past_the_room_draw_nowhere() {
        let names = ["Play"];
        let at = Point::new(96.0, 600.0);
        let fits = aside(&names, at, "ends 10:42 pm", 1920.0).expect("room");
        let right = fits.x + fits.width;
        assert_eq!(aside(&names, at, "ends 10:42 pm", right), Some(fits));
        assert_eq!(aside(&names, at, "ends 10:42 pm", right - 1.0), None);
        assert_eq!(aside(&names, at, "", 1920.0), None);
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
