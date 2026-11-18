//! The palette, type scale, timings, and layout constants. Every other
//! module draws through these, so a change to the look happens here.

use std::time::Duration;

use iced::{Color, Point, Rectangle, Size};

/// The display lays out in a canvas 1080 pixels tall. The canvas module
/// scales the whole layer to the real output, so the same layout serves
/// 720p, 1080p, and 4K.
pub const CANVAS_HEIGHT: f32 = 1080.0;

/// The canvas width for a 16:9 surface. The canvas module takes the real
/// width from the surface's own aspect ratio, so a canvas pixel is square.
pub const CANVAS_WIDTH: f32 = 1920.0;

/// Colors from the brand theme's `liken.css`. `fill` is the dark scheme's
/// `--link` (lichen green), `text` is `--ink`, and `muted` is
/// `--ink-muted`. `track` is the light scheme's `--link`, a deep green
/// dark enough for the bright elapsed fill to stand out against it.
pub mod color {
    use super::Color;

    /// The color of body text.
    pub fn text() -> Color {
        liken_iced::palette::dark().ink
    }

    /// The accent color, also used for the elapsed part of a bar.
    pub fn fill() -> Color {
        liken_iced::palette::dark().link
    }

    /// The unplayed part of a bar.
    pub fn track() -> Color {
        liken_iced::palette::light().link
    }

    /// The color of secondary text.
    pub fn muted() -> Color {
        liken_iced::palette::dark().ink_muted
    }

    /// The playhead, drawn in the accent color.
    pub fn playhead() -> Color {
        fill()
    }

    /// The underline of the focused stop of the control strip and of the
    /// focused row of a chooser, drawn in the accent color.
    pub fn focus() -> Color {
        fill()
    }

    /// The color of the scrim and of every shadow.
    pub const SHADOW: Color = Color::BLACK;
}

/// Opacities, from 1 for opaque to 0 for clear, each written as the ASS
/// alpha byte that `opacity` converts.
pub mod alpha {
    use super::opacity;

    pub const OPAQUE: f32 = opacity(0x00);
    pub const SUBDUED: f32 = opacity(0x80);
    pub const TRACK: f32 = opacity(0x50);
    pub const DIM: f32 = opacity(0xA8);
    pub const PANEL: f32 = opacity(0x14);
    pub const HIGHLIGHT: f32 = opacity(0x30);
    /// The opacity of the scrim where it is darkest, and of the volume
    /// row's background.
    pub const SCRIM_EDGE: f32 = opacity(0x34);
}

/// Converts an ASS alpha byte to an opacity. An ASS alpha runs from 00,
/// opaque, to FF, transparent.
pub const fn opacity(alpha: u8) -> f32 {
    1.0 - alpha as f32 / 255.0
}

/// Returns `color` with its alpha set to `alpha`.
pub fn at(color: Color, alpha: f32) -> Color {
    Color { a: alpha, ..color }
}

/// Returns `color` with its alpha multiplied by `fade`. Every color the
/// display draws goes through this, so the whole layer fades together. A
/// `fade` of 1 leaves the color unchanged.
pub fn faded(color: Color, fade: f32) -> Color {
    Color {
        a: color.a * fade,
        ..color
    }
}

/// Fade times. The OSD and the volume indicator each run their own fade
/// timer and must look the same, so both read these. A fade takes
/// `FADE_IN` to reach full opacity and `FADE_OUT` to reach clear.
pub const FADE_IN: Duration = Duration::from_millis(350);
pub const FADE_OUT: Duration = Duration::from_millis(600);

/// A fade advances and redraws on this period, about 60 times a second.
pub const FADE_TICK: Duration = Duration::from_nanos(16_666_667);

/// How long the up-next offer takes to fade out when the viewer selects it.
/// It is shorter than `FADE_OUT` because the select requests the next
/// work and mpv quits soon after, so the screen must be clear by then.
/// The compositor fades the film out in its frame over the same time.
pub const HAND_OFF: Duration = Duration::from_millis(300);

/// How long after the last action that raised them the OSD and the
/// volume indicator start to fade out. Each runs its own timer.
pub const IDLE_HIDE: Duration = Duration::from_secs(4);

/// How long the up-next card stays up after it appears on its own near
/// the end of a work. No key press raised it, so the viewer may not be
/// looking when it appears. This is long enough to notice it and read the
/// title. Then it fades so that it does not cover the end of the work. A
/// key press that raises the OSD after that shows the card again.
pub const CARD_HIDE: Duration = Duration::from_secs(5);

/// The type scale, in canvas pixels, large enough to read from across a
/// room at 1080p. Each number is a line box height, the value an ASS `\fs`
/// tag takes. [`type_size`] converts it to the font size that iced takes.
pub mod type_scale {
    pub const TITLE: f32 = 64.0;
    pub const LABEL: f32 = 40.0;
    pub const SMALL: f32 = 34.0;
    pub const TINY: f32 = 28.0;
}

/// The bounding height of `SourceSans3-Regular.otf`, the font that the
/// brand crate carries, divided by its em: `usWinAscent` plus
/// `usWinDescent` from its `OS/2` table, 1326 units, over `unitsPerEm` from
/// its `head` table, 1000.
///
/// libass scales a font so that its bounding height equals the `\fs`
/// size, so a `\fs` number is a line box height in canvas pixels. iced
/// takes a size per em, which is that height divided by this ratio.
const FACE_METRIC: f32 = 1326.0 / 1000.0;

/// Returns the font size whose line box is `height` canvas pixels tall.
/// The canvas anchors a line to its line box, so a line placed by its top
/// or bottom edge has its baseline where libass would put it.
pub fn type_size(height: f32) -> f32 {
    height / FACE_METRIC
}

/// The underline that marks the focus. The control strip's focused stop
/// and a chooser's focused row both draw it, so the two look the same. It
/// is a bar under the text instead of a box around it, so the mark stays
/// as quiet as the text, and it is thick enough to see from across a room
/// at 1080p.
pub mod underline {
    /// The bar's thickness, in canvas pixels.
    pub const THICKNESS: f32 = 4.0;
    /// The space between the bottom of the text's line box and the bar.
    pub const GAP: f32 = 4.0;
}

/// Returns the underline rectangle for a run of text whose line box ends
/// at `bottom`. `left` and `width` are the drawn text's, so the bar ends
/// where the text ends.
pub fn underline(left: f32, bottom: f32, width: f32) -> Rectangle {
    Rectangle::new(
        Point::new(left, bottom + underline::GAP),
        Size::new(width, underline::THICKNESS),
    )
}

/// The side margin of every flush-left and flush-right element.
pub const MARGIN_X: f32 = 96.0;

/// The top margin that the header, the clock, and the volume row measure
/// down from.
pub const MARGIN_Y: f32 = 90.0;

/// The y position of the scrubber bar's center line, which the image
/// counter shares.
pub const BAR_Y: f32 = 904.0;

/// The y position of the bottom of a chooser or an adjuster panel. The
/// panel grows upward from it.
pub const PANEL_BOTTOM: f32 = 876.0;

/// The distance between lines in the top-right column.
pub const LINE_PITCH: f32 = type_scale::SMALL + 12.0;

/// How far each scrim reaches from its screen edge, in canvas pixels. It
/// stays at its darkest out to `HOLD` and is clear at `CLEAR`.
pub const SCRIM_TOP_HOLD: f32 = 180.0;
pub const SCRIM_TOP_CLEAR: f32 = 380.0;
pub const SCRIM_BOTTOM_HOLD: f32 = 230.0;
pub const SCRIM_BOTTOM_CLEAR: f32 = 450.0;
/// The top of the cast row's heading is 428 pixels above the bottom edge,
/// so its scrim stays at its darkest to 470 and is clear at 700.
pub const SCRIM_CAST_HOLD: f32 = 470.0;
pub const SCRIM_CAST_CLEAR: f32 = 700.0;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_palette_matches_the_brand_tokens() {
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

    #[test]
    fn every_opacity_converts_its_ass_alpha_byte() {
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

    #[test]
    fn an_underline_is_one_gap_below_the_line_box() {
        assert_eq!(
            underline(120.0, 718.0, 300.0),
            Rectangle::new(Point::new(120.0, 722.0), Size::new(300.0, 4.0))
        );
    }

    #[test]
    fn a_type_size_is_its_line_box_through_the_face_metric() {
        assert!((type_size(type_scale::SMALL) - 25.641).abs() < 0.001);
        assert!((type_size(type_scale::TINY) - 21.116).abs() < 0.001);
        assert!((type_size(type_scale::LABEL) - 30.166).abs() < 0.001);
        assert!((type_size(type_scale::TITLE) - 48.265).abs() < 0.001);
    }

    #[test]
    fn a_faded_color_keeps_its_channels_and_scales_its_alpha() {
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
