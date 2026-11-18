// The colors, type scale, and margins of the idle screen.
//
// The colors are the brand's, which `liken-iced` parses from `liken.css`.
// The sizes and opacities are given as the ASS values they equal, the
// units of the `display` crate's theme, so this screen matches it. ASS
// alpha counts transparency, so an alpha byte `a` is the opacity
// `(255 - a) / 255`.

use iced_widget::canvas::{Path, Text};
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle, Size};
use liken_iced::palette;

/// The color that every frame and every capture is cleared to. It is
/// black and not the brand's `--page`, because the idle screen shares an
/// output with a film, and a different black shows on the panel when one
/// replaces the other.
pub const BACKGROUND: Color = Color::BLACK;

/// The color of text: the dark scheme's `--ink`.
pub fn text() -> Color {
    palette::dark().ink
}

/// The color of a bar's fill: the dark scheme's `--link`, the palest
/// green of the palette.
pub fn fill() -> Color {
    palette::dark().link
}

/// The color of a bar's track: the light scheme's `--link`, the darkest
/// green of the palette, so the fill shows against it.
pub fn track() -> Color {
    palette::light().link
}

/// The color of secondary text: the dark scheme's `--ink-muted`.
pub fn muted() -> Color {
    palette::dark().ink_muted
}

/// Returns `color` with its alpha set to `alpha`.
pub const fn faded(color: Color, alpha: f32) -> Color {
    Color { a: alpha, ..color }
}

/// The canvas is 1080 rows tall, and its width follows the aspect ratio
/// of the real surface, so a canvas pixel is square. A fixed width of
/// 1920 on a 21:9 surface would stretch every vector drawing and every
/// margin horizontally by a third.
pub const CANVAS_HEIGHT: f32 = 1080.0;

/// The canvas width for a surface size in pixels, or the 16:9 width when
/// either dimension is 0. The surface can change size while the client
/// runs, so the elements take the width from the current size and not
/// from a width read at startup.
pub fn canvas_width(surface: (u32, u32)) -> f32 {
    if surface.0 == 0 || surface.1 == 0 {
        return CANVAS_HEIGHT * 16.0 / 9.0;
    }
    (CANVAS_HEIGHT * surface.0 as f32 / surface.1 as f32 + 0.5).floor()
}

/// The distance from the left and right edges to the elements placed
/// against them.
pub const MARGIN_X: f32 = 96.0;

/// The distance from the top edge to the clock, and from the bottom edge
/// to the identity block.
pub const MARGIN_Y: f32 = 90.0;

// The bounding height of `SourceSans3-Regular.otf` divided by its em:
// `usWinAscent` 1000 plus `usWinDescent` 326 from the `OS/2` table, over
// `unitsPerEm` 1000 from the `head` table. This screen and the `display`
// crate both draw in this font.
//
// libass scales a font so that its bounding height equals the `\fs`
// size. So an `\fs` number is the height of a line box in canvas pixels,
// and the font size that iced takes is that height divided by this
// ratio. A layout measure in `\fs` units needs no conversion.
const FACE_METRIC: f32 = 1326.0 / 1000.0;

/// The height in canvas pixels of the line box of font size `size`,
/// which is also the ASS `\fs` of that size. [`line`] gives each line
/// this height, so an anchor places an edge of the box.
pub const fn line_box(size: f32) -> f32 {
    size * FACE_METRIC
}

/// The font size whose line box is `height` canvas pixels tall, the
/// inverse of [`line_box`].
pub const fn type_size(height: f32) -> f32 {
    height / FACE_METRIC
}

// The font sizes in canvas pixels: `type_size` of `\fs40`, `\fs34`, and
// `\fs28`, rounded to a whole pixel. These are the `LABEL`, `SMALL`, and
// `TINY` of the `display` crate's `type_scale`.
pub const LABEL: f32 = 30.0;
pub const SMALL: f32 = 26.0;
pub const TINY: f32 = 21.0;

/// The line box of `SMALL`, `\fs34`. It is written out and not computed
/// as `line_box(SMALL)`, because `SMALL` is rounded to a whole pixel, so
/// its line box can differ from 34 by up to half of `FACE_METRIC`.
pub const SMALL_BOX: f32 = 34.0;

/// The distance from the top of one line of the top-right column to the
/// next: one `SMALL_BOX` and 12 canvas pixels of space. From the top
/// margin down, the column is the clock, the activity line, and the
/// volume row.
pub const LINE_PITCH: f32 = SMALL_BOX + 12.0;

/// The font family of every line. `liken-iced` loads its font files
/// into iced before the first frame.
pub use liken_iced::font::FAMILY as FONT;

/// The corner of a line's box that its position gives, with the ASS
/// `\an` tag of the same corner.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Anchor {
    /// `\an1`. The identity block stacks its lines upward from the
    /// bottom margin.
    BottomLeft,
    /// `\an3`. The preview legend.
    BottomRight,
    /// `\an9`. The clock, the activity line, and the volume row.
    TopRight,
}

/// One line of text at a position in canvas units.
pub fn line(content: String, at: Point, anchor: Anchor, size: f32, color: Color) -> Text {
    Text {
        content,
        position: at,
        color,
        size: Pixels(size),
        line_height: LineHeight::Absolute(Pixels(line_box(size))),
        font: Font::with_name(FONT),
        align_x: match anchor {
            Anchor::BottomLeft => Alignment::Left,
            Anchor::BottomRight | Anchor::TopRight => Alignment::Right,
        },
        align_y: match anchor {
            Anchor::BottomLeft | Anchor::BottomRight => Vertical::Bottom,
            Anchor::TopRight => Vertical::Top,
        },
        ..Text::default()
    }
}

/// A rounded rectangle whose radius is capped at half the shape's width
/// and half its height, so a bar with only a few pixels of fill still
/// draws as a rounded shape.
pub fn rounded(shape: Rectangle, radius: f32) -> Path {
    let radius = radius.min(shape.width / 2.0).min(shape.height / 2.0);
    Path::rounded_rectangle(
        Point::new(shape.x, shape.y),
        Size::new(shape.width, shape.height),
        radius.into(),
    )
}

/// Returns `color` with its alpha scaled by the shade's `light`, which is
/// 1 at full brightness and 0 when the shade has darkened the screen.
///
/// Each element darkens its own colors this way, because a black
/// rectangle drawn over the frame would not cover the text: `iced_wgpu`
/// draws a layer's text after all its other primitives, and a canvas
/// frame is one layer. Over the black background, the scaled alpha gives
/// the same pixels as a black cover of opacity `1 - light`.
pub const fn under(color: Color, light: f32) -> Color {
    faded(color, color.a * light)
}

/// The opacity of a dim part of the screen, low enough to tell apart at
/// a glance and high enough to read. ASS alpha 0xA8.
pub const DIM: f32 = 87.0 / 255.0;
/// The opacity of a bar's track. ASS alpha 0x50.
pub const TRACK_OPACITY: f32 = 175.0 / 255.0;
/// The opacity of the black panel that the volume row draws behind
/// itself. ASS alpha 0x34.
pub const SURFACE: f32 = 203.0 / 255.0;

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
    fn the_shade_scales_an_alpha_and_leaves_the_colour_alone() {
        let full = faded(text(), 1.0);

        assert_eq!(under(full, 1.0), full);
        assert_eq!(under(full, 0.0).a, 0.0);
        assert_eq!(under(full, 0.5).a, 0.5);
        assert_eq!(
            (under(full, 0.5).r, under(full, 0.5).g),
            (text().r, text().g)
        );
    }

    #[test]
    fn the_shade_scales_a_part_that_is_already_faded() {
        let dim = faded(text(), DIM);
        assert_eq!(under(dim, 0.5).a, DIM / 2.0);
    }

    #[test]
    fn the_type_scale_is_the_ass_scale_through_the_face_metric() {
        assert_eq!(LABEL, type_size(40.0).round());
        assert_eq!(SMALL, type_size(34.0).round());
        assert_eq!(TINY, type_size(28.0).round());
    }

    #[test]
    fn a_type_size_and_its_line_box_convert_to_each_other() {
        let box_ = line_box(SMALL);

        assert!(
            (box_ - 34.0).abs() < FACE_METRIC / 2.0,
            "the line box is {box_}, want within half of FACE_METRIC of 34.0"
        );
        assert!((type_size(box_) - SMALL).abs() < 0.001, "{box_}");
    }

    #[test]
    fn a_line_uses_the_font_the_size_and_the_matching_line_box() {
        let drawn = line(
            "9:01 am".into(),
            Point::new(1780.0, 90.0),
            Anchor::TopRight,
            SMALL,
            text(),
        );

        assert_eq!(drawn.size, Pixels(SMALL));
        assert_eq!(
            drawn.line_height,
            LineHeight::Absolute(Pixels(line_box(SMALL)))
        );
        assert_eq!(drawn.font, Font::with_name(FONT));
    }

    #[test]
    fn each_anchor_places_a_line_by_the_corner_it_names() {
        let placed = |anchor| {
            let drawn = line(String::new(), Point::ORIGIN, anchor, SMALL, text());
            (drawn.align_x, drawn.align_y)
        };

        assert_eq!(
            placed(Anchor::BottomLeft),
            (Alignment::Left, Vertical::Bottom)
        );
        assert_eq!(
            placed(Anchor::BottomRight),
            (Alignment::Right, Vertical::Bottom)
        );
        assert_eq!(placed(Anchor::TopRight), (Alignment::Right, Vertical::Top));
    }

    #[test]
    fn the_fill_reads_over_the_track() {
        let brightness = |color: Color| color.r + color.g + color.b;
        assert!(
            brightness(fill()) > brightness(track()),
            "the fill is not brighter than the track, want the fill to read over the track"
        );
    }
}
