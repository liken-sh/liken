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

/// The color of the part under a name in a stripe: the muted ink at a
/// lower opacity, so a character's name reads as an aside to the
/// person's.
pub fn faint() -> Color {
    Color { a: 0.72, ..muted() }
}

/// The accent that marks focus, the dark scheme's `--link`.
pub fn accent() -> Color {
    palette::dark().link
}

/// The track the volume row's fill runs over: the light scheme's `--link`,
/// the darkest green of the family, so the fill over it reads at a glance.
pub fn track() -> Color {
    palette::light().link
}

/// The fill of a placeholder art slot and a focused row's ground: the
/// dark scheme's `--page`, slightly lighter than the black ground.
pub fn slot() -> Color {
    palette::dark().page
}

/// The darkest end of the scrim panel, at the left edge where the text
/// column starts.
pub fn shade() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.94)
}

/// The opacity art draws at where a screen drew it but the person did not
/// choose it, such as the siblings in a set strip. The dim is the image's
/// own opacity and not a veil over it, because a veil is a fill and a fill
/// draws under every image of its layer.
pub const DIM: f32 = 0.42;

/// The width of the stroke that marks focus, in logical pixels, thick
/// enough to read from a couch and thin enough that the art it frames
/// stays the larger thing.
pub const MARK: f32 = 4.0;

/// The space between the art and the inner edge of the focus stroke, so
/// the stroke frames the art and does not touch it.
pub const MARK_GAP: f32 = 4.0;

/// The color of the focus stroke and of the underline that marks the
/// current member of a strip: the accent, a little translucent, so the
/// art shows through it.
pub fn mark() -> Color {
    Color {
        a: 0.75,
        ..accent()
    }
}

/// The ground under a page's own art, such as the episode wall of a
/// series. It is nearly opaque, so the stills sit on near-black and the
/// backdrop shows through only as a trace.
pub fn ground() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.9)
}

/// The far end of every gradient over art. It leaves the art as it is.
pub const CLEAR: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.0);

/// The size of the focused title's name under its poster, in logical
/// pixels, large enough to read from a couch.
pub const NAME: f32 = 23.0;

/// The size of the one line under every slot of a wall.
pub const CAPTION: f32 = 18.0;

/// The size of the two lines under a headshot in a stripe. A headshot is
/// narrower than a poster, so its lines are smaller than a wall's caption
/// to fit a full name.
pub const FACE: f32 = 16.0;

/// The size of a page's title, where the item has no logo.
pub const TITLE: f32 = 53.0;

/// The size of a title inside a header of a fixed height, where the item
/// has no logo.
pub const HEAD_TITLE: f32 = 38.0;

/// The size of a page's facts line.
pub const FACTS: f32 = 21.0;

/// The size of a page's tagline.
pub const TAGLINE: f32 = 23.0;

/// The size of a page's plot.
pub const PLOT: f32 = 20.0;

/// The size of the word in a button.
pub const BUTTON: f32 = 21.0;

/// The size of the heading over a strip, over a stripe, and over a season's
/// divider.
pub const HEADING: f32 = 20.0;

/// The size of the word in a control of a wall's band.
pub const CONTROL: f32 = 18.0;

/// The size of a score on the ratings line, a step over the facts line
/// it sits under, so the number reads first and the site's scale after
/// it reads second.
pub const SCORE: f32 = 24.0;

/// The size of the credits and the cast on a page.
pub const CREDITS: f32 = 18.0;

/// The size of the number in the volume row.
pub const ROW_NAME: f32 = 28.0;

/// The size of secondary text: details and placeholder titles.
pub const DETAIL: f32 = 20.0;

/// How long the page takes to leave when a person chooses a title, in
/// seconds.
pub const DEPARTURE: f64 = 0.35;

/// How long the page takes to return, in seconds: about a third of the
/// departure, so the way back is quicker than the way out.
pub const RETURN: f64 = 0.12;

/// The one family the whole display draws in, and the italic face of that
/// family, which the second caption line of a two-line card draws in. Both
/// come from the brand crate, which carries the files and loads them into
/// the toolkit at startup, so this screen and every other liken display
/// draw the same face.
pub use liken_iced::font::{FAMILY as FONT, ITALIC};

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
    fn the_veil_over_art_is_black_and_only_the_alpha_differs() {
        assert_eq!(shade().r, 0.0);
        assert_eq!(shade().g, 0.0);
        assert_eq!(shade().b, 0.0);
        assert!(shade().a > CLEAR.a);
        assert_eq!(CLEAR.a, 0.0);
    }

    #[test]
    fn the_way_back_is_shorter_than_the_way_out() {
        const { assert!(RETURN > 0.0) };
        const { assert!(RETURN * 2.0 < DEPARTURE) };
    }

    #[test]
    fn art_the_person_did_not_choose_draws_under_full_brightness() {
        const { assert!(DIM > 0.0) };
        const { assert!(DIM < 1.0) };
    }
}
