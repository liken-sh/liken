// The liken look in one place. Every view reads its colors and its measures
// from here, so a change to either lands in one file.
//
// The colors are the brand's, through the liken-iced crate, which parses them
// out of liken.css. The one measure below is this screen's own: a size for a
// person reading from a couch, which no stylesheet for a page states.

use iced_winit::core::Color;
use liken_iced::palette;

/// The ground the client fills. It is the clear color of every frame and every
/// capture.
///
/// The ground is black rather than the brand's `--page`, because the browser
/// shares one output with a film. A film's black and the browser's black have
/// to be the same black, or the panel shows a seam where one ends.
pub const BACKGROUND: Color = Color::BLACK;

/// The color of every line of text on the screen, the dark scheme's `--ink`.
pub fn text() -> Color {
    palette::dark().ink
}

/// The size of the one line the browser draws, in logical pixels, large
/// enough to read from a couch.
pub const TITLE: f32 = 64.0;

/// The one family the whole display draws in. The image installs the face, and
/// the toolkit resolves it by name. With no installed match the toolkit falls
/// back and the look drifts.
pub const FONT: &str = "Source Sans 3";

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_ground_is_black() {
        assert_eq!(BACKGROUND, Color::from_rgb(0.0, 0.0, 0.0));
    }

    #[test]
    fn the_text_is_the_brand_ink() {
        assert_eq!(text(), palette::dark().ink);
    }
}
