// The liken look in one place. Every view reads its colors and its measures
// from here, so a change to either lands in one file.
//
// The colors are the brand's, through the liken-iced crate, which parses them
// out of liken.css. The measures below are this display's own: a type scale
// and margins for a screen a person reads from a couch, which no stylesheet
// for a page states.

use iced_winit::core::Color;
use liken_iced::palette;

/// The ground the client fills. It is the clear color of every frame and every
/// capture.
///
/// The ground is black rather than the brand's `--page`, because the idle
/// screen shares one output with a film. A film's black and the screen's black
/// have to be the same black, or the panel shows a seam where one ends.
pub const BACKGROUND: Color = Color::BLACK;

/// The color of every line of text on the screen, the dark scheme's `--ink`.
pub fn text() -> Color {
    palette::dark().ink
}

/// The accent that fills a bar, the dark scheme's `--link`. It is the palest
/// lichen green, so it reads over the track below at a distance.
pub fn fill() -> Color {
    palette::dark().link
}

/// The track a bar's fill runs over, the light scheme's `--link`. It is the
/// darkest green of the family, dark enough that the fill reads over it.
pub fn track() -> Color {
    palette::light().link
}

/// The color of text that reads under the rest, the dark scheme's
/// `--ink-muted`.
pub fn muted() -> Color {
    palette::dark().ink_muted
}

/// The same color at another opacity, for an element that fades on its own.
pub const fn faded(color: Color, alpha: f32) -> Color {
    Color { a: alpha, ..color }
}

// The canvas. The display draws in one space 1080 rows tall, and the width
// follows the real surface's own ratio, so a canvas pixel is square. A 16:9
// surface gives 1920, the width this space has always held. A fixed 1920 on a
// 21:9 screen stretches every vector drawing by a third and pulls every margin
// inside where it belongs.
pub const CANVAS_HEIGHT: f32 = 1080.0;

/// The canvas width for one surface size, rounded the way the display rounds
/// it. Every element measures against the screen that is there now, so a width
/// read once at startup is the width of another screen.
pub fn canvas_width(surface: (u32, u32)) -> f32 {
    if surface.0 == 0 || surface.1 == 0 {
        return CANVAS_HEIGHT * 16.0 / 9.0;
    }
    (CANVAS_HEIGHT * surface.0 as f32 / surface.1 as f32 + 0.5).floor()
}

// The side margin every flush-left and flush-right element keeps, and the top
// margin. The bottom margin is the top one by symmetry: the clock and the
// activity line hang from it, and the identity block stands the same distance
// off the bottom edge.
pub const MARGIN_X: f32 = 140.0;
pub const MARGIN_Y: f32 = 90.0;

// The type scale, in canvas pixels. The sizes are large enough to read from a
// couch at 1080.
pub const TITLE: f32 = 64.0;
pub const LABEL: f32 = 40.0;
pub const SMALL: f32 = 34.0;
pub const TINY: f32 = 28.0;

/// The drop from one line of the top-right column to the next. The clock hangs
/// at the top margin, the activity line one pitch under it, and the volume row
/// one pitch under that, so the three read as a column and no two of them
/// touch.
pub const LINE_PITCH: f32 = SMALL + 12.0;

/// The one family the whole display draws in. The image installs the face, and
/// the toolkit resolves it by name. With no installed match the toolkit falls
/// back and the look drifts.
pub const FONT: &str = "Source Sans 3";

/// The opacity for an ASS alpha byte, which runs 0x00 opaque to 0xFF
/// transparent. The Lua display the client replaces states its transparency in
/// those bytes, so the port reads the same numbers and converts them here
/// rather than restating each one as a fraction.
pub const fn opacity(ass_alpha: u8) -> f32 {
    (255 - ass_alpha) as f32 / 255.0
}

/// A part of the screen one step under full brightness.
pub const SUBDUED: f32 = opacity(0x80);
/// A part of the screen far enough under full brightness that a glance tells
/// it from the rest, and bright enough to read.
pub const DIM: f32 = opacity(0xA8);
/// A bar's track, under the fill that reads against it.
pub const TRACK_OPACITY: f32 = opacity(0x50);
/// The dark surface an element carries when it draws over a frame with no
/// scrim under it.
pub const SURFACE: f32 = opacity(0x34);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_ground_is_black() {
        assert_eq!(BACKGROUND, Color::from_rgb(0.0, 0.0, 0.0));
    }

    #[test]
    fn fading_keeps_the_color() {
        let half = faded(fill(), 0.5);
        assert_eq!((half.r, half.g, half.b), (fill().r, fill().g, fill().b));
        assert_eq!(half.a, 0.5);
    }

    #[test]
    fn the_text_is_the_brand_ink() {
        assert_eq!(text(), palette::dark().ink);
        assert_ne!(text(), muted());
    }

    #[test]
    fn the_fill_reads_over_the_track() {
        let brightness = |color: Color| color.r + color.g + color.b;
        assert!(brightness(fill()) > brightness(track()));
    }
}
