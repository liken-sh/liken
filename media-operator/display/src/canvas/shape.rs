//! The paths the display draws on its canvas. Each function takes the
//! top-left corner of the box that holds the shape.

use iced::widget::canvas::Path;
use iced::{Point, Rectangle, Size};

/// The half-width of a pointy-top hexagon, which is cos(30 degrees).
const HEXAGON_HALF_WIDTH: f32 = 0.866_025_4;

/// A rounded rectangle. The radius is limited to half the width and half
/// the height, so a bar with only a few pixels of fill is still drawn.
// Each corner is a cubic Bezier whose two control points are both the
// corner of the rectangle. That curve is tighter than a circular arc.
pub fn rounded(shape: Rectangle, radius: f32) -> Path {
    let r = radius.min(shape.width / 2.0).min(shape.height / 2.0);
    let (x, y, w, h) = (shape.x, shape.y, shape.width, shape.height);
    let at = |dx: f32, dy: f32| Point::new(x + dx, y + dy);
    Path::new(|path| {
        path.move_to(at(r, 0.0));
        path.line_to(at(w - r, 0.0));
        path.bezier_curve_to(at(w, 0.0), at(w, 0.0), at(w, r));
        path.line_to(at(w, h - r));
        path.bezier_curve_to(at(w, h), at(w, h), at(w - r, h));
        path.line_to(at(r, h));
        path.bezier_curve_to(at(0.0, h), at(0.0, h), at(0.0, h - r));
        path.line_to(at(0.0, r));
        path.bezier_curve_to(at(0.0, 0.0), at(0.0, 0.0), at(r, 0.0));
        path.close();
    })
}

/// A regular hexagon with a vertex at the top, centered in the square of
/// side `2 * radius` whose top-left corner is `at`. `radius` is the
/// distance from the center to a vertex.
///
/// The canvas has the same aspect ratio as the screen, so a regular
/// hexagon on the canvas is regular on the screen and needs no correction.
pub fn hexagon(at: Point, radius: f32) -> Path {
    let a = HEXAGON_HALF_WIDTH * radius;
    let (x, y, r) = (at.x, at.y, radius);
    Path::new(|path| {
        path.move_to(Point::new(x + r, y));
        path.line_to(Point::new(x + r + a, y + 0.5 * r));
        path.line_to(Point::new(x + r + a, y + 1.5 * r));
        path.line_to(Point::new(x + r, y + 2.0 * r));
        path.line_to(Point::new(x + r - a, y + 1.5 * r));
        path.line_to(Point::new(x + r - a, y + 0.5 * r));
        path.close();
    })
}

/// The bounding box of the hexagon, `2a` wide by `2r` tall, where `a` is
/// `HEXAGON_HALF_WIDTH * radius`.
pub fn hexagon_bounds(at: Point, radius: f32) -> Rectangle {
    let a = HEXAGON_HALF_WIDTH * radius;
    Rectangle::new(
        Point::new(at.x + radius - a, at.y),
        Size::new(2.0 * a, 2.0 * radius),
    )
}

/// The position and radius of the hexagon path to stroke at `width` so
/// the stroke covers the same pixels as an ASS border of that width.
/// libass draws a border outside the shape, and the canvas centers a
/// stroke on its path, so the path moves out by half the width.
pub fn outside_hexagon(at: Point, radius: f32, width: f32) -> (Point, f32) {
    let grown = radius + width / 2.0 / HEXAGON_HALF_WIDTH;
    (
        Point::new(at.x + radius - grown, at.y + radius - grown),
        grown,
    )
}

/// The same adjustment as `outside_hexagon`, for a rounded rectangle.
pub fn outside_rounded(shape: Rectangle, radius: f32, width: f32) -> (Rectangle, f32) {
    let out = width / 2.0;
    (
        Rectangle::new(
            Point::new(shape.x - out, shape.y - out),
            Size::new(shape.width + 2.0 * out, shape.height + 2.0 * out),
        ),
        radius + out,
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The playhead hexagon: a radius of 15 canvas pixels gives a box 25.98
    /// wide by 30 tall.
    #[test]
    fn a_hexagon_is_two_radii_tall() {
        let bounds = hexagon_bounds(Point::new(100.0, 200.0), 15.0);
        assert!(
            (bounds.width - 25.98).abs() < 0.01,
            "the hexagon is {} wide, want 25.98",
            bounds.width
        );
        assert_eq!(bounds.height, 30.0);
        assert!(
            (bounds.x - 102.01).abs() < 0.01,
            "the hexagon starts at x {}, want 102.01",
            bounds.x
        );
        assert_eq!(bounds.y, 200.0);
        assert_eq!(bounds.center().x, 115.0);
        assert_eq!(bounds.center().y, 215.0);
    }

    /// A border of width 2 covers the 2 canvas pixels outside the shape on
    /// every side and none inside it.
    #[test]
    fn a_border_covers_only_the_pixels_outside_the_shape() {
        let (at, radius) = outside_hexagon(Point::new(100.0, 200.0), 15.0, 2.0);
        let outline = hexagon_bounds(at, radius);
        let shape = hexagon_bounds(Point::new(100.0, 200.0), 15.0);
        assert!(
            (outline.width - (shape.width + 2.0)).abs() < 0.01,
            "the outline is {} wide, want the shape width {} plus 2",
            outline.width,
            shape.width
        );
        assert_eq!(outline.center(), shape.center());

        let (grown, radius) = outside_rounded(
            Rectangle::new(Point::new(96.0, 676.0), Size::new(720.0, 200.0)),
            14.0,
            2.0,
        );
        assert_eq!(grown.x, 95.0);
        assert_eq!(grown.y, 675.0);
        assert_eq!(grown.width, 722.0);
        assert_eq!(grown.height, 202.0);
        assert_eq!(radius, 15.0);
    }
}
