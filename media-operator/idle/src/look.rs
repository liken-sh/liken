// The liken look in one place. Every view reads its colors and its measures
// from here, so a change to either lands in one file.
//
// The colors are the brand's, through the liken-iced crate, which parses them
// out of liken.css. The measures below are this display's own: a type scale
// and margins for a screen a person reads from a couch, which no stylesheet
// for a page states.

use iced_widget::canvas::Text;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight};
use iced_winit::core::{Color, Font, Pixels, Point};
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

/// The canvas width for one surface size, rounded the way `display/theme.lua`
/// rounds it. The surface can change size while the client runs, so every
/// element measures against the screen that is there now rather than a width
/// read once at startup.
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

/// The face's metric: its bounding height over its em, 1362 units over 1000.
/// The bounding height is `usWinAscent` plus `usWinDescent` from the `OS/2`
/// table of `NotoSans-Regular.ttf` in `fonts-noto-core`, the package both this
/// image and the player image install.
///
/// libass scales a face so that its bounding height fills the size an ASS `\fs`
/// states. One `\fs` number therefore states two measures. The first is the
/// line box the text draws in, in canvas pixels. The second is the type size,
/// which is that box divided by this metric. So a layout measure
/// `display/theme.lua` writes in `\fs` units is a canvas measure here and
/// passes through unchanged, and only a type size goes through the metric.
const FACE_METRIC: f32 = 1362.0 / 1000.0;

/// The line box one type size draws in, in canvas pixels. A line's anchor falls
/// on that box in both renderers, so a line placed by its top or its bottom
/// puts its baseline where libass puts it. It is also the `\fs` number
/// `display/theme.lua` states the size as.
pub const fn line_box(size: f32) -> f32 {
    size * FACE_METRIC
}

/// The type size that draws in a line box `height` canvas pixels tall, which is
/// the other direction of [`line_box`].
pub const fn type_size(height: f32) -> f32 {
    height / FACE_METRIC
}

// The type scale, in canvas pixels. The sizes are large enough to read from a
// couch at 1080. They are `display/theme.lua`'s `\fs40`, `\fs34`, and `\fs28`
// through `type_size`, each rounded to a whole pixel.
pub const LABEL: f32 = 29.0;
pub const SMALL: f32 = 25.0;
pub const TINY: f32 = 21.0;

/// The drop from one line of the top-right column to the next: one line box of
/// the small size, and 12 canvas pixels of air. It is `theme.lua`'s
/// `theme.type.small + 12`, where the `\fs` is the line box and the 12 is
/// canvas pixels. The clock hangs at the top margin, the activity line one
/// pitch under it, and the volume row one pitch under that, so the three read
/// as a column and no two of them touch.
pub const LINE_PITCH: f32 = line_box(SMALL) + 12.0;

/// The one family the whole display draws in, the same family
/// `display/theme.lua` names for the playback overlay. The image installs it
/// and the toolkit resolves it by name. It is Noto Sans because that is also
/// what the toolkit falls back to, so a face an image failed to install draws
/// as itself rather than as a look that quietly drifted.
pub const FONT: &str = "Noto Sans";

/// Where a line's position falls on the line, in the ASS anchors
/// `display/theme.lua` states. The display draws with three of the nine.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Anchor {
    /// `\an1`. The identity block stacks upward from the bottom margin, so
    /// each of its lines is placed by its own bottom.
    BottomLeft,
    /// `\an3`. The preview legend draws on the bottom margin at the right.
    BottomRight,
    /// `\an9`. The clock, the activity line, and the volume row hang from the
    /// top margin at the right, so each is placed by its own top.
    TopRight,
}

/// One line of text, the way `display/theme.lua`'s `theme.text` states one:
/// an anchor, a position in canvas units, a size from the type scale, and a
/// colour.
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

/// One colour under the shade. `light` runs from 1 at full brightness to 0 on
/// a screen the quiet window has darkened.
///
/// The shade is a factor every element applies to its own colours, and not a
/// black rectangle over them. `iced_wgpu` renders a layer in four passes,
/// quads then meshes then images then text, and a canvas frame is one layer,
/// so a rectangle drawn last still lands under every line of text. Over the
/// black ground this display fills, an alpha scaled by `light` composites to
/// the pixels a black cover would give. `display/theme.lua` fades its whole
/// overlay the same way, through `theme.fade`.
pub const fn under(color: Color, light: f32) -> Color {
    faded(color, color.a * light)
}

/// A part of the screen far enough under full brightness that a glance tells
/// it from the rest, and bright enough to read. `display/theme.lua` states it
/// as the ASS alpha byte 0xA8, which leaves 87 of the 255 steps of light.
pub const DIM: f32 = 87.0 / 255.0;
/// A bar's track, under the fill that reads against it. `theme.lua` states it
/// as the ASS alpha byte 0x50.
pub const TRACK_OPACITY: f32 = 175.0 / 255.0;
/// The dark surface an element carries when it draws over a frame with no
/// scrim under it. `theme.lua` states it as `scrim_edge_alpha`, the ASS alpha
/// byte 0x34.
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
    fn the_type_scale_is_the_lua_scale_through_the_face_metric() {
        assert_eq!(LABEL, type_size(40.0).round());
        assert_eq!(SMALL, type_size(34.0).round());
        assert_eq!(TINY, type_size(28.0).round());
    }

    #[test]
    fn a_size_and_its_line_box_are_the_two_readings_of_one_ass_size() {
        // `theme.lua` states the small size as `\fs34`, so the line box the
        // small size draws in is 34 canvas pixels.
        let box_ = line_box(SMALL);

        assert!((box_ - 34.0).abs() < 0.1, "{box_}");
        assert!((type_size(box_) - SMALL).abs() < 0.001, "{box_}");
    }

    #[test]
    fn a_line_takes_the_face_the_size_and_the_box_that_size_states() {
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
        assert!(brightness(fill()) > brightness(track()));
    }
}
