// The card: the two lines under one piece of art, which a strip slot and
// a wall cell both draw here. Line one is bright at the caption size and
// says what the art cannot; line two is smaller, faint, and italic and
// says the facts. Focus is the mark alone, so a card adds no word and
// changes no color when it takes focus. Both lines are cut by the shaper
// at the read, each at its own size, and this module holds those cuts as
// well as the draw.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::{Card, area, text};
use crate::look;

/// How many lines a card draws under its art.
pub const LINES: usize = 2;

/// The height this many lines of a card take: the first at the caption
/// size and every line under it at the smaller face size.
pub fn height(lines: usize) -> f32 {
    match lines {
        0 => 0.0,
        lines => text::height(1, look::CAPTION) + text::height(lines - 1, look::FACE),
    }
}

/// The band the second line draws in: right under the first, and as tall
/// as the smaller size it draws at.
pub fn under(band: Rectangle) -> Rectangle {
    area(
        band.x,
        band.y + band.height,
        band.width,
        text::height(1, look::FACE),
    )
}

/// The first line cut by the shaper to a band of this width.
pub fn cut(caption: &str, width: f32) -> String {
    text::measured_cut(caption, look::CAPTION, width)
}

/// The second line cut by the shaper to the same band at its own smaller
/// size. The measure runs in the roman face: the italic the line draws in
/// is a hair narrower, so a cut that fits the roman fits the italic.
pub fn under_cut(line: &str, width: f32) -> String {
    text::measured_cut(line, look::FACE, width)
}

/// Draw one card's two lines: the first in the band it is given, and the
/// second in the band under it. Both are drawn as they stand, because the
/// read cut them to this band already.
pub fn draw<T: Card>(frame: &mut canvas::Frame<Renderer>, card: &T, band: Rectangle) {
    // A tagline is the film's own words and draws in the italic face,
    // bright at the caption size; a title draws in the roman one. The cut
    // measures in the roman face either way, the wider of the two.
    match card.leads_with_tagline() {
        true => text::faced(
            frame,
            card.fitted(),
            band,
            look::CAPTION,
            look::text(),
            look::ITALIC,
        ),
        false => text::shown(frame, card.fitted(), band, look::CAPTION, look::text()),
    }
    text::faced(
        frame,
        card.under_fitted(),
        under(band),
        look::FACE,
        look::faint(),
        look::ITALIC,
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    const BAND: f32 = 200.0;

    #[test]
    fn a_cards_second_line_is_shorter_than_its_first() {
        assert_eq!(height(0), 0.0);
        assert_eq!(height(1), text::height(1, look::CAPTION));
        assert_eq!(
            height(2),
            text::height(1, look::CAPTION) + text::height(1, look::FACE)
        );
        assert!(height(2) - height(1) < text::height(1, look::CAPTION));
    }

    #[test]
    fn the_second_line_stands_under_the_first_at_the_smaller_size() {
        let band = area(10.0, 20.0, BAND, text::height(1, look::CAPTION));
        let under = under(band);
        assert_eq!(under.x, band.x);
        assert_eq!(under.width, band.width);
        assert_eq!(under.y, band.y + band.height);
        assert_eq!(under.height, text::height(1, look::FACE));
        assert_eq!(band.height + under.height, height(2));
    }

    #[test]
    fn each_line_is_cut_at_the_size_it_draws_at() {
        let long = "W".repeat(60);
        let first = cut(&long, BAND);
        let second = under_cut(&long, BAND);
        assert!(first.ends_with('\u{2026}'));
        assert!(second.ends_with('\u{2026}'));
        assert!(text::measured(&first, look::CAPTION) <= BAND);
        assert!(text::measured(&second, look::FACE) <= BAND);
        assert!(second.chars().count() > first.chars().count());
        assert_eq!(cut("Film one", BAND), "Film one");
        assert_eq!(under_cut("1987 · 1h 37m", BAND), "1987 · 1h 37m");
    }
}
