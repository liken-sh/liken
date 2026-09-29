// The small stroke glyphs that stand before the words of a button: the
// playback row's buttons and the status line's marks. They are drawn on
// the canvas and not taken from a font, so every glyph has the same
// stroke, caps, and joins, and the set grows with a few lines of path.
// Each glyph is drawn on a grid of 24 units a side, the grid the brand's
// icons are drawn on, so the strokes match theirs.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Point, Radians, Rectangle, Size};

/// A glyph a button draws before its words.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Icon {
    /// A check: the work watched.
    Check,
    /// An arrow that turns back on itself: the work returned to its start.
    Undo,
    /// A triangle that points right: play the work.
    Play,
    /// A bar with a triangle that points to it: play from the start.
    Start,
    /// A film camera, two reels over a body and a lens: the trailer.
    Camera,
    /// A bar with a triangle that points away from it: play from here
    /// onward.
    Onward,
}

/// The side of an icon beside words of this size. An icon is a pixel
/// taller than words of 19, and every row of words keeps that proportion,
/// so a playback button and a mark read as one family at their two sizes.
pub fn side(size: f32) -> f32 {
    (size * 20.0 / 19.0).round()
}

/// The space between an icon and the words after it: half the icon's
/// side.
pub fn gap(size: f32) -> f32 {
    (side(size) / 2.0).round()
}

/// Draw one glyph in its square box, in this ink. The stroke is a tenth
/// of the box's side, 2.4 units of the grid, so a larger icon draws a
/// heavier stroke and keeps the weight its words have.
pub fn draw(frame: &mut canvas::Frame<Renderer>, glyph: Icon, at: Rectangle, ink: Color) {
    let unit = at.width / 24.0;
    let point = |x: f32, y: f32| Point::new(at.x + x * unit, at.y + y * unit);
    let path = canvas::Path::new(|path| match glyph {
        Icon::Check => {
            path.move_to(point(5.0, 12.5));
            path.line_to(point(9.5, 17.0));
            path.line_to(point(19.0, 7.5));
        }
        // Three quarters and more of a circle, from its left edge round
        // under and up to the upper left, and the arrowhead at its start.
        Icon::Undo => {
            path.arc(canvas::path::Arc {
                center: point(12.0, 12.0),
                radius: 8.0 * unit,
                start_angle: Radians(std::f32::consts::PI),
                end_angle: Radians(-2.348),
            });
            path.move_to(point(4.0, 4.0));
            path.line_to(point(4.0, 8.5));
            path.line_to(point(8.5, 8.5));
        }
        // The triangle's point is right of the grid's middle, so its
        // weight, and not its box, centres in the icon.
        Icon::Play => {
            path.move_to(point(7.0, 4.0));
            path.line_to(point(19.0, 12.0));
            path.line_to(point(7.0, 20.0));
            path.close();
        }
        Icon::Start => {
            path.move_to(point(18.0, 5.0));
            path.line_to(point(9.0, 12.0));
            path.line_to(point(18.0, 19.0));
            path.close();
            path.move_to(point(5.5, 5.0));
            path.line_to(point(5.5, 19.0));
        }
        // The bar is the episode the person picked, and the triangle
        // leaves it to the right: play from here onward. The bar and the
        // triangle keep Start's sizes, with the triangle turned round, so
        // the two read as a pair on one row.
        Icon::Onward => {
            path.move_to(point(5.5, 5.0));
            path.line_to(point(5.5, 19.0));
            path.move_to(point(10.0, 5.0));
            path.line_to(point(19.0, 12.0));
            path.line_to(point(10.0, 19.0));
            path.close();
        }
        // The two reels stand on the body, and the lens flares out of
        // its right side.
        Icon::Camera => {
            path.circle(point(6.0, 6.5), 3.5 * unit);
            path.circle(point(13.5, 6.5), 3.5 * unit);
            path.rounded_rectangle(
                point(2.0, 12.0),
                Size::new(13.0 * unit, 8.0 * unit),
                (1.5 * unit).into(),
            );
            path.move_to(point(15.0, 14.5));
            path.line_to(point(21.5, 12.0));
            path.line_to(point(21.5, 20.0));
            path.line_to(point(15.0, 17.5));
        }
    });
    frame.stroke(
        &path,
        canvas::Stroke::default()
            .with_color(ink)
            .with_width(at.width / 10.0)
            .with_line_cap(canvas::LineCap::Round)
            .with_line_join(canvas::LineJoin::Round),
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_icon_keeps_the_marks_proportions_at_every_size() {
        let cases = [(19.0, 20.0, 10.0), (21.0, 22.0, 11.0)];
        for (size, icon, space) in cases {
            assert_eq!((side(size), gap(size)), (icon, space), "{size}");
        }
    }
}
