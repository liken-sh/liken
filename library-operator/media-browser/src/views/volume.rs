// The volume row: a speaker glyph, a short bar, and the number, in the top
// right corner of the frame. It is the row the idle screen draws, at this
// client's own sizes.
//
// The row is a layer of its own over every screen, because inside one
// layer the renderer draws every mesh, then every image, then every text,
// so a surface drawn over a page's art would be painted under it.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle, Theme, mouse};
use std::convert::Infallible;

use media_screen::volume::{UNITY_LEVEL, Volume};

use super::{area, label, rounded, text};
use crate::look;

// The margins the row hangs off, the same measures a page sets its own
// blocks by.
const MARGIN_X: f32 = 120.0;
const MARGIN_Y: f32 = 56.0;

// The width the number reserves at the right margin, so the bar and the
// glyph hold their place as the number moves between one and three digits.
const NUMBER_WIDTH: f32 = 84.0;

// The bar is short because the number beside it carries the reading. The
// bar shows the level at a glance.
const BAR_WIDTH: f32 = 220.0;
const BAR_HEIGHT: f32 = 12.0;
const BAR_RADIUS: f32 = 3.0;

// The glyph's box, and the gap between the glyph and the bar.
const GLYPH_WIDTH: f32 = 26.0;
const GLYPH_HEIGHT: f32 = 30.0;
const GLYPH_GAP: f32 = 16.0;

// The number's line box. The bar and the glyph centre on the middle of it
// and the dark surface covers it, so the three parts read as one row.
const NUMBER_BOX: f32 = look::ROW_NAME * text::LEADING;

// The row draws over whatever the browser has on the screen, and on a
// bright backdrop the glyph and the number would vanish, so the row carries
// a dark surface of its own. These are the padding around the three parts
// and the radius of its corners.
const PAD_X: f32 = 24.0;
const PAD_Y: f32 = 12.0;
const SURFACE_RADIUS: f32 = 14.0;

// The opacity of that surface, and of the track the bar's fill runs over.
const SURFACE: f32 = 0.8;
const TRACK: f32 = 0.69;

// The speaker: one closed polygon of the driver box and the cone, in the
// glyph's own box. The image carries no icon font, so the mark is a path.
const SPEAKER: [(f32, f32); 6] = [
    (0.0, 10.0),
    (10.0, 10.0),
    (22.0, 0.0),
    (22.0, 30.0),
    (10.0, 20.0),
    (0.0, 20.0),
];

// The muted state draws this slash across the speaker, so one element
// carries both the level and the mute.
const SLASH: [(f32, f32); 4] = [(2.0, 24.0), (24.0, 2.0), (24.0, 8.0), (2.0, 30.0)];

// The slash draws in the speaker's own colour, and the two would read as
// one shape without an outline between them. This is the width of that
// outline outside the slash.
const SLASH_BORDER: f32 = 2.0;

/// The volume row as one frame draws it.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Row {
    /// The level and the muted flag the row reads.
    pub volume: Volume,
    /// The row's own fade, from 0 off screen to 1 full.
    pub fade: f32,
}

impl canvas::Program<Infallible, Theme, Renderer> for Row {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let fade = self.fade.clamp(0.0, 1.0);
        if fade <= 0.0 {
            return vec![frame.into_geometry()];
        }

        let row = places(bounds);
        let ink = match self.volume.muted {
            true => look::muted(),
            false => look::text(),
        };
        let faded = |color: Color, alpha: f32| Color { a: alpha, ..color };

        frame.fill(
            &rounded(row.surface, SURFACE_RADIUS),
            faded(look::BACKGROUND, SURFACE * fade),
        );
        frame.fill(
            &rounded(row.bar, BAR_RADIUS),
            faded(look::track(), TRACK * fade),
        );

        let filled = filled(self.volume.level);
        if filled >= 1.0 {
            frame.fill(
                &rounded(
                    Rectangle {
                        width: filled,
                        ..row.bar
                    },
                    BAR_RADIUS,
                ),
                faded(look::accent(), fade),
            );
        }

        frame.fill(&polygon(&SPEAKER, row.glyph), faded(ink, fade));
        if self.volume.muted {
            let slash = polygon(&SLASH, row.glyph);
            // The toolkit centres a stroke on the path it follows, and
            // the outline this glyph needs stands outside the slash, so
            // the stroke is twice the border and the fill covers the half
            // that fell inside.
            frame.stroke(
                &slash,
                canvas::Stroke {
                    width: 2.0 * SLASH_BORDER,
                    style: canvas::Style::Solid(faded(look::BACKGROUND, fade)),
                    line_join: canvas::LineJoin::Round,
                    ..canvas::Stroke::default()
                },
            );
            frame.fill(&slash, faded(ink, fade));
        }

        frame.fill_text(label(
            &self.volume.level.to_string(),
            row.number,
            look::ROW_NAME,
            faded(look::text(), fade),
            Alignment::Right,
            Vertical::Top,
            NUMBER_WIDTH,
        ));

        vec![frame.into_geometry()]
    }
}

// How much of the bar the level fills, in logical pixels. A level above
// unity fills no further.
fn filled(level: i64) -> f32 {
    BAR_WIDTH * (level as f32 / UNITY_LEVEL as f32).clamp(0.0, 1.0)
}

// Where the parts of the row stand. The three parts hang off the right
// margin, so the row measures itself against the frame it draws in and
// holds the margin at any window size.
struct Places {
    surface: Rectangle,
    bar: Rectangle,
    glyph: Point,
    number: Point,
}

fn places(bounds: Rectangle) -> Places {
    let top = bounds.y + MARGIN_Y;
    let right = bounds.x + bounds.width - MARGIN_X;
    // The bar and the glyph centre on the middle of the number's line, so
    // the three parts read as one row.
    let middle = top + NUMBER_BOX / 2.0;

    let bar_x = right - NUMBER_WIDTH - BAR_WIDTH;
    let glyph_x = bar_x - GLYPH_GAP - GLYPH_WIDTH;
    let surface_x = glyph_x - PAD_X;

    Places {
        surface: area(
            surface_x,
            top - PAD_Y,
            right + PAD_X - surface_x,
            // The number's line is the tallest of the three parts, so the
            // surface covers it and the padding.
            NUMBER_BOX + 2.0 * PAD_Y,
        ),
        bar: area(bar_x, middle - BAR_HEIGHT / 2.0, BAR_WIDTH, BAR_HEIGHT),
        glyph: Point::new(glyph_x, middle - GLYPH_HEIGHT / 2.0),
        number: Point::new(right, top),
    }
}

// One closed polygon in the glyph's own box, placed at a point.
fn polygon(points: &[(f32, f32)], at: Point) -> canvas::Path {
    canvas::Path::new(|path| {
        for (index, (x, y)) in points.iter().enumerate() {
            let point = Point::new(at.x + x, at.y + y);
            match index {
                0 => path.move_to(point),
                _ => path.line_to(point),
            }
        }
        path.close();
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bounds() -> Rectangle {
        area(0.0, 0.0, 1920.0, 1080.0)
    }

    #[test]
    fn the_bar_fills_at_unity_and_no_further() {
        assert_eq!(filled(0), 0.0);
        assert_eq!(filled(50), BAR_WIDTH / 2.0);
        assert_eq!(filled(100), BAR_WIDTH);
        assert_eq!(filled(140), BAR_WIDTH);
    }

    #[test]
    fn the_row_stands_at_the_top_right_margin() {
        let row = places(bounds());
        assert_eq!(row.number.x, 1920.0 - MARGIN_X);
        assert_eq!(row.number.y, MARGIN_Y);
        assert_eq!(row.bar.x, row.number.x - NUMBER_WIDTH - BAR_WIDTH);
        assert_eq!(row.glyph.x, row.bar.x - GLYPH_GAP - GLYPH_WIDTH);
    }

    #[test]
    fn the_surface_covers_the_three_parts_and_the_padding() {
        let row = places(bounds());
        assert_eq!(row.surface.x, row.glyph.x - PAD_X);
        assert_eq!(row.surface.x + row.surface.width, row.number.x + PAD_X);
        assert_eq!(row.surface.height, NUMBER_BOX + 2.0 * PAD_Y);
        assert_eq!(row.surface.y, row.number.y - PAD_Y);
    }

    #[test]
    fn the_bar_and_the_glyph_centre_on_the_numbers_line() {
        let row = places(bounds());
        let middle = |shape: Rectangle| shape.y + shape.height / 2.0;
        assert_eq!(middle(row.bar), row.number.y + NUMBER_BOX / 2.0);
        assert_eq!(row.glyph.y + GLYPH_HEIGHT / 2.0, middle(row.bar));
    }

    #[test]
    fn the_row_follows_the_frame_it_draws_in() {
        let row = places(area(0.0, 0.0, 1280.0, 720.0));
        assert_eq!(row.number.x, 1280.0 - MARGIN_X);
        assert_eq!(row.surface.x + row.surface.width, row.number.x + PAD_X);
    }
}
