//! This module holds the look. Every other module draws through it, so the
//! palette, the type scale, and the geometry constants are defined in one
//! place.

use std::time::Duration;

use iced::Color;

/// The display draws in one space 1080 rows tall. The canvas module scales
/// the whole layer to the real output, so the same layout serves 720, 1080,
/// and 4K with no branch.
pub const CANVAS_HEIGHT: f32 = 1080.0;

/// The width a 16:9 surface gives, which is the width this space always
/// held. The canvas module takes the width from the surface's own ratio, so
/// a canvas pixel is square.
pub const CANVAS_WIDTH: f32 = 1920.0;

/// The values are liken brand tokens from the brand theme's `liken.css`. The
/// accent fill is the dark-scheme lichen green `--link`, the text is
/// `--ink`, and the muted grey is `--ink-muted`. The bar track is the
/// light-scheme `--link`, a deep green dark enough that the bright elapsed
/// fill reads over it.
pub mod color {
    use super::Color;

    /// The colour of body text.
    pub fn text() -> Color {
        liken_iced::palette::dark().ink
    }

    /// The accent, and the colour of the elapsed fill.
    pub fn fill() -> Color {
        liken_iced::palette::dark().link
    }

    /// The bar's unplayed track.
    pub fn track() -> Color {
        liken_iced::palette::light().link
    }

    /// The colour of text that reads under body text.
    pub fn muted() -> Color {
        liken_iced::palette::dark().ink_muted
    }

    /// The playhead, which takes the accent.
    pub fn playhead() -> Color {
        fill()
    }

    /// The scrim and every shadow.
    pub const SHADOW: Color = Color::BLACK;
}

/// How much of the ground each surface covers, from 1 opaque to 0 clear.
/// Every value states the ASS alpha byte the look was drawn with, so the
/// display covers what the Lua display covered, and nothing downstream reads
/// a byte whose 0 means opaque.
pub mod alpha {
    use super::opacity;

    pub const OPAQUE: f32 = opacity(0x00);
    pub const SUBDUED: f32 = opacity(0x80);
    pub const TRACK: f32 = opacity(0x50);
    pub const DIM: f32 = opacity(0xA8);
    pub const PANEL: f32 = opacity(0x14);
    pub const HIGHLIGHT: f32 = opacity(0x30);
    /// The scrim's dark plateau, and the volume row's own surface.
    pub const SCRIM_EDGE: f32 = opacity(0x34);
}

/// One ASS alpha byte as the fraction of the ground it covers. An ASS alpha
/// runs from 00, opaque, to FF, transparent.
pub const fn opacity(alpha: u8) -> f32 {
    1.0 - alpha as f32 / 255.0
}

/// One colour at one coverage. Every drawing call takes a colour that already
/// carries the fraction it covers.
pub fn at(color: Color, alpha: f32) -> Color {
    Color { a: alpha, ..color }
}

/// One colour under the fade. The fade scales every alpha the display draws,
/// so the whole layer fades as one. At a fade of 1 the colour covers what it
/// states.
pub fn faded(color: Color, fade: f32) -> Color {
    Color {
        a: color.a * fade,
        ..color
    }
}

/// The fade timing lives here because two things fade on clocks of their
/// own, the OSD and the volume indicator, and the two must look the same. A
/// fade takes `FADE_IN` to reach full and `FADE_OUT` to reach clear, and the
/// out is longer than the in, so anything on the display leaves more slowly
/// than it arrives.
pub const FADE_IN: Duration = Duration::from_millis(350);
pub const FADE_OUT: Duration = Duration::from_millis(600);

/// A fade steps on this period, about sixty times a second, and redraws on
/// each step.
pub const FADE_TICK: Duration = Duration::from_nanos(16_666_667);

/// An element the display summons for one action leaves this many seconds
/// after the last one. The OSD and the volume indicator wait out the same
/// window, each on its own timer.
pub const IDLE_HIDE: Duration = Duration::from_secs(4);

/// The type scale, in canvas pixels. The sizes are large enough to read from
/// a couch at 1080. Each number is a line box, the measure the display
/// states as an ASS `\\fs`, and [`type_size`] turns one into the size the
/// toolkit takes.
pub mod type_scale {
    pub const TITLE: f32 = 64.0;
    pub const LABEL: f32 = 40.0;
    pub const SMALL: f32 = 34.0;
    pub const TINY: f32 = 28.0;
}

/// The face's metric: its bounding height over its em, 1326 units over 1000.
/// The bounding height is `usWinAscent` plus `usWinDescent` and the em is
/// `unitsPerEm`, from the `OS/2` and `head` tables of
/// `SourceSans3-Regular.otf`, the face the brand crate carries and both
/// renderers draw.
///
/// libass scales a face so that its bounding height fills the size an ASS
/// `\\fs` states, so one `\\fs` number states two measures: the line box the
/// text draws in, in canvas pixels, and the type size, which is that box
/// divided by this metric. A layout measure the display writes in `\\fs`
/// units is a canvas measure here and passes through unchanged; only a type
/// size goes through the metric.
const FACE_METRIC: f32 = 1326.0 / 1000.0;

/// The type size that draws in a line box `height` canvas pixels tall. A
/// line's anchor falls on that box in both renderers, so a line placed by its
/// top or its bottom puts its baseline where libass puts it.
pub fn type_size(height: f32) -> f32 {
    height / FACE_METRIC
}

/// The side margin every flush-left and flush-right element keeps.
pub const MARGIN_X: f32 = 96.0;

/// The top margin, which the header, the clock, and the volume row all
/// measure down from.
pub const MARGIN_Y: f32 = 90.0;

/// The scrubber bar's center line, which the image counter shares.
pub const BAR_Y: f32 = 904.0;

/// The baseline a chooser or an adjuster panel grows upward from.
pub const PANEL_BOTTOM: f32 = 876.0;

/// The pitch of one line in the top-right column.
pub const LINE_PITCH: f32 = type_scale::SMALL + 12.0;

/// The heights of the scrim's top band and bottom band, in canvas pixels.
pub const SCRIM_TOP_HEIGHT: f32 = 410.0;
pub const SCRIM_BOTTOM_HEIGHT: f32 = 480.0;

/// The dark plateau covers this fraction of the scrim height, at the screen
/// edge, over the text. The fade softens its inner edge.
pub const SCRIM_SOLID: f32 = 0.66;

/// How far the fade carries the plateau inward, as a fraction of the scrim
/// height.
pub const SCRIM_REACH: f32 = 0.3;

#[cfg(test)]
mod tests {
    use super::*;

    /// The four tokens the palette reads out of `liken.css`.
    #[test]
    fn the_palette_holds_the_colours() {
        let hex = |color: Color| {
            let byte = |channel: f32| (channel * 255.0).round() as u8;
            format!(
                "#{:02X}{:02X}{:02X}",
                byte(color.r),
                byte(color.g),
                byte(color.b)
            )
        };

        assert_eq!(hex(color::text()), "#E8E8E8");
        assert_eq!(hex(color::fill()), "#B4C49A");
        assert_eq!(hex(color::track()), "#4A5D3A");
        assert_eq!(hex(color::muted()), "#A0A6AD");
        assert_eq!(hex(color::playhead()), "#B4C49A");
        assert_eq!(hex(color::SHADOW), "#000000");
    }

    /// Every coverage is the ASS alpha byte the look was drawn with, read as
    /// the fraction of the ground it covers.
    #[test]
    fn every_coverage_is_the_ass_alpha_byte_it_was_drawn_with() {
        for (coverage, byte) in [
            (alpha::OPAQUE, 0x00),
            (alpha::SUBDUED, 0x80),
            (alpha::TRACK, 0x50),
            (alpha::DIM, 0xA8),
            (alpha::PANEL, 0x14),
            (alpha::HIGHLIGHT, 0x30),
            (alpha::SCRIM_EDGE, 0x34),
        ] {
            assert_eq!(coverage, 1.0 - f32::from(byte as u8) / 255.0, "{byte:#04x}");
        }
        assert_eq!(alpha::OPAQUE, 1.0);
        assert_eq!(opacity(0xFF), 0.0);
        assert!((alpha::SUBDUED - 0.498).abs() < 0.001);
        assert!((alpha::TRACK - 0.686).abs() < 0.001);
        assert!((alpha::DIM - 0.341).abs() < 0.001);
        assert!((alpha::PANEL - 0.922).abs() < 0.001);
        assert!((alpha::HIGHLIGHT - 0.812).abs() < 0.001);
        assert!((alpha::SCRIM_EDGE - 0.796).abs() < 0.001);
    }

    /// The display states a size as an ASS `\\fs`, which is the line box. The
    /// toolkit takes the type size, which is that box through the metric.
    #[test]
    fn a_type_size_is_its_line_box_through_the_face_metric() {
        assert!((type_size(type_scale::SMALL) - 25.641).abs() < 0.001);
        assert!((type_size(type_scale::TINY) - 21.116).abs() < 0.001);
        assert!((type_size(type_scale::LABEL) - 30.166).abs() < 0.001);
        assert!((type_size(type_scale::TITLE) - 48.265).abs() < 0.001);
    }

    #[test]
    fn a_faded_colour_keeps_its_channels_and_scales_its_alpha() {
        let full = faded(at(color::text(), alpha::OPAQUE), 1.0);
        assert_eq!(
            (full.r, full.g, full.b),
            (color::text().r, color::text().g, color::text().b)
        );
        assert_eq!(full.a, 1.0);
        assert_eq!(faded(at(color::fill(), alpha::OPAQUE), 0.5).a, 0.5);
        assert_eq!(faded(at(color::fill(), alpha::SUBDUED), 0.0).a, 0.0);
        assert!((faded(at(color::fill(), alpha::SUBDUED), 1.0).a - 0.498).abs() < 0.001);
    }
}
