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

/// The color of secondary text, the dark scheme's `--ink-muted`.
pub fn muted() -> Color {
    palette::dark().ink_muted
}

/// The accent that marks focus, the dark scheme's `--link`.
pub fn accent() -> Color {
    palette::dark().link
}

/// The fill of a placeholder art slot and a focused row's ground: the
/// dark scheme's `--page`, slightly lighter than the black ground.
pub fn slot() -> Color {
    palette::dark().page
}

/// The scrim over art that is drawn but not chosen, such as the siblings
/// in a set strip. The veils are black and not the brand's page color,
/// for the reason the ground is: they sit over art.
pub fn scrim() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.62)
}

/// The darkest end of the gradient over a page's backdrop, at the corner
/// the text sits in.
pub fn shade() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.94)
}

/// The far end of every gradient over art. It leaves the art as it is.
pub const CLEAR: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.0);

/// The size of the focused title's name under its poster, in logical
/// pixels, large enough to read from a couch.
pub const NAME: f32 = 34.0;

/// The size of a page's title, where the item has no logo.
pub const TITLE: f32 = 76.0;

/// The size of a page's facts line.
pub const FACTS: f32 = 30.0;

/// The size of a page's tagline.
pub const TAGLINE: f32 = 34.0;

/// The size of a page's plot.
pub const PLOT: f32 = 28.0;

/// The size of the word in a button.
pub const BUTTON: f32 = 30.0;

/// The size of the heading over a strip, and of the controls in a wall's
/// band.
pub const HEADING: f32 = 26.0;

/// The size of the credits and the cast on a page.
pub const CREDITS: f32 = 26.0;

/// The size of a list row's name.
pub const ROW_NAME: f32 = 40.0;

/// The size of secondary text: details and placeholder titles.
pub const DETAIL: f32 = 28.0;

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

    #[test]
    fn the_secondary_colors_come_from_the_dark_scheme() {
        assert_eq!(muted(), palette::dark().ink_muted);
        assert_eq!(accent(), palette::dark().link);
        assert_eq!(slot(), palette::dark().page);
    }

    #[test]
    fn the_veils_over_art_are_black_and_only_the_alpha_differs() {
        assert_eq!(scrim().r, 0.0);
        assert_eq!(shade().r, 0.0);
        assert!(shade().a > scrim().a);
        assert_eq!(CLEAR.a, 0.0);
    }
}
