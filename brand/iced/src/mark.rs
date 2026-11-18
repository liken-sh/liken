//! The `liken` mark, drawn onto an `iced` canvas.
//!
//! `liken.svg` is the original. This module embeds it, parses its
//! fourteen polygons, and returns their geometry in the SVG's own
//! coordinates, with a bounding box. A caller maps that geometry onto a
//! canvas of any size.

use std::sync::LazyLock;

use iced_widget::canvas::{Frame, Path};
use iced_widget::core::{Color, Point, Rectangle};
use iced_widget::graphics::geometry;

use crate::palette;
use crate::pulse::Pulse;

/// The SVG is compiled in, so a consumer needs no file at run time.
const SVG: &str = include_str!("../../liken.svg");

/// One hexagon of the mark, in the SVG's coordinate space.
///
/// The SVG rounds the corners by stroking each polygon in its own fill
/// color. The drawn shape is therefore the polygon plus that stroke,
/// and filling the polygon alone gives sharp corners.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Hexagon {
    /// The six vertices, in the order the `points` attribute lists
    /// them.
    pub points: [Point; 6],
    /// The mean of the six vertices. For a regular hexagon this is the
    /// center, so scaling about it resizes the hexagon in place.
    pub centroid: Point,
    /// The fill, which is also the stroke color.
    pub fill: Color,
    /// The stroke width, in SVG units.
    pub stroke_width: f32,
    /// The size animation of this hexagon, chosen by its index in the
    /// file.
    pub pulse: Pulse,
}

/// One hexagon mapped onto a canvas and sized for one instant.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Placed {
    /// The six vertices, in canvas units.
    pub points: [Point; 6],
    /// The fill, which is also the stroke color.
    pub fill: Color,
    /// The stroke width, in canvas units.
    pub stroke_width: f32,
}

struct Mark {
    hexagons: Vec<Hexagon>,
    bounds: Rectangle,
}

static MARK: LazyLock<Mark> = LazyLock::new(|| read(SVG));

/// The fourteen hexagons, in the order `liken.svg` lists them.
pub fn hexagons() -> &'static [Hexagon] {
    &MARK.hexagons
}

/// The smallest rectangle that contains every vertex, in SVG units.
///
/// It is computed from the vertices because the `viewBox` is square and
/// larger than the mark, so centering the `viewBox` would not center
/// the mark.
pub fn bounds() -> Rectangle {
    MARK.bounds
}

impl Hexagon {
    /// This hexagon mapped onto a canvas, sized for one instant.
    ///
    /// `center` is the canvas point where the middle of [`bounds`]
    /// goes, and `span` is the width that [`bounds`] takes, both in
    /// canvas units. The height scales by the same factor, so the mark
    /// keeps its proportions. The drawn mark is wider than `span`,
    /// because the rounded corners extend half a stroke width past the
    /// outermost vertices, so a caller that clips to `span` cuts off the
    /// outer edge of the mark.
    ///
    /// `energy` runs from 0 at rest to 1 at full swing, and `phase` is
    /// the animation clock in seconds.
    ///
    /// The SVG and the canvas both have y increasing downward, so the
    /// mapping does not flip y.
    pub fn place(&self, center: Point, span: f32, energy: f64, phase: f64) -> Placed {
        let box_ = bounds();
        let scale = span / box_.width;
        let center_of_box = box_.center();
        let onto_canvas = |point: Point| {
            Point::new(
                center.x + (point.x - center_of_box.x) * scale,
                center.y + (point.y - center_of_box.y) * scale,
            )
        };

        let mut points = self.points.map(onto_canvas);

        // The pulse scale is exactly 1.0 at energy 0, but scaling a
        // vertex about the centroid by 1.0 can still change its last
        // bits in floating point. Skipping the scaling keeps a resting
        // mark identical to the SVG.
        if energy != 0.0 {
            let centroid = onto_canvas(self.centroid);
            let grow = self.pulse.scale_at(energy, phase) as f32;

            for point in &mut points {
                *point = Point::new(
                    centroid.x + (point.x - centroid.x) * grow,
                    centroid.y + (point.y - centroid.y) * grow,
                );
            }
        }

        Placed {
            points,
            fill: self.fill,
            // The stroke width does not follow the pulse, so the corner
            // radius stays fixed while the hexagon grows and shrinks.
            stroke_width: self.stroke_width * scale,
        }
    }
}

/// Draws the whole mark into a canvas frame.
///
/// `center`, `span`, `energy`, and `phase` mean what they mean in
/// [`Hexagon::place`]. `alpha` runs from 0 (transparent) to 1 (opaque)
/// and applies to every hexagon.
///
/// Each hexagon is one filled path, so a transform on the frame scales
/// its rounded corners with the rest of the mark.
pub fn draw<Renderer>(
    frame: &mut Frame<Renderer>,
    center: Point,
    span: f32,
    energy: f64,
    phase: f64,
    alpha: f32,
) where
    Renderer: geometry::Renderer,
{
    for hexagon in hexagons() {
        let placed = hexagon.place(center, span, energy, phase);

        frame.fill(
            &outline(&placed),
            Color {
                a: alpha,
                ..placed.fill
            },
        );
    }
}

/// How many straight segments approximate one rounded corner.
///
/// The arc at each vertex turns 60 degrees, so six segments each cover
/// 10 degrees. A chord of that size lies r * (1 - cos 5 degrees) inside
/// the true arc, which is a few hundredths of a pixel at the sizes the
/// mark is drawn.
const ARC_STEPS: usize = 6;

/// The outline of one placed hexagon with its corners rounded, as a
/// single path to fill.
///
/// `liken.svg` strokes each polygon in its own fill color with round
/// joins. That makes the drawn shape the hexagon offset outward by half
/// the stroke width, with an arc of that radius around each vertex.
/// This function builds that shape as geometry.
///
/// The mark uses one fill and no stroke. A stroke is centered on the
/// path, so half of it overlaps the fill, and on a translucent mark the
/// overlap composites brighter and shows as a rim around each hexagon.
/// Also, `iced` applies the frame's transform to a path but tessellates
/// a stroke at the width it was given, so in a scaled frame the stroke
/// would keep its width while the hexagons scale, and the gaps between
/// hexagons would change.
fn outline(placed: &Placed) -> Path {
    let round = placed.stroke_width / 2.0;
    let points = placed.points;

    let mut cx = 0.0;
    let mut cy = 0.0;
    for point in points {
        cx += point.x / points.len() as f32;
        cy += point.y / points.len() as f32;
    }

    // The outward unit normal of edge a-b. Either perpendicular could
    // be outward, depending on whether the polygon winds clockwise, so
    // this picks the one that points away from the centroid.
    let normal = |a: Point, b: Point| {
        let (dx, dy) = (b.x - a.x, b.y - a.y);
        let length = dx.hypot(dy);
        let (nx, ny) = (dy / length, -dx / length);
        let (mx, my) = ((a.x + b.x) / 2.0 - cx, (a.y + b.y) / 2.0 - cy);
        if nx * mx + ny * my >= 0.0 {
            (nx, ny)
        } else {
            (-nx, -ny)
        }
    };

    // Each edge moved outward by `round`, as its two endpoints.
    let edges: [(Point, Point); 6] = std::array::from_fn(|i| {
        let (a, b) = (points[i], points[(i + 1) % points.len()]);
        let (nx, ny) = normal(a, b);
        (
            Point::new(a.x + nx * round, a.y + ny * round),
            Point::new(b.x + nx * round, b.y + ny * round),
        )
    });

    Path::new(|builder| {
        builder.move_to(edges[0].0);

        for i in 0..points.len() {
            builder.line_to(edges[i].1);

            // The round join at the vertex after this edge is an arc of
            // radius `round` around the vertex, from the end of this
            // offset edge to the start of the next one.
            let vertex = points[(i + 1) % points.len()];
            let from = edges[i].1;
            let to = edges[(i + 1) % points.len()].0;

            let start = (from.y - vertex.y).atan2(from.x - vertex.x);
            let mut sweep = (to.y - vertex.y).atan2(to.x - vertex.x) - start;
            // atan2 returns angles in (-PI, PI], so the difference can
            // be up to a full turn off. Wrapping it makes the arc take
            // the short way around.
            let turn = std::f32::consts::TAU;
            while sweep > std::f32::consts::PI {
                sweep -= turn;
            }
            while sweep < -std::f32::consts::PI {
                sweep += turn;
            }

            for step in 1..=ARC_STEPS {
                let angle = start + sweep * step as f32 / ARC_STEPS as f32;
                builder.line_to(Point::new(
                    vertex.x + round * angle.cos(),
                    vertex.y + round * angle.sin(),
                ));
            }
        }

        builder.close();
    })
}

/// Parses the `<polygon>` elements of an SVG document and computes
/// their bounding box.
///
/// It reads nothing else, because the mark is only polygons. The SVG is
/// compiled in, so a document this parser rejects is a defect in
/// `liken.svg`. Each failure panics and says what it expected.
fn read(svg: &str) -> Mark {
    let hexagons: Vec<Hexagon> = svg
        .split("<polygon")
        .skip(1)
        .enumerate()
        .map(|(index, tail)| {
            let element = tail
                .split_once('>')
                .expect("a <polygon> element has no closing >")
                .0;

            read_hexagon(index, element)
        })
        .collect();

    assert!(
        !hexagons.is_empty(),
        "the document has no <polygon> element"
    );
    assert_eq!(
        hexagons.len(),
        14,
        "the document has a different number of <polygon> elements than the mark's 14"
    );

    let mut left = f32::MAX;
    let mut top = f32::MAX;
    let mut right = f32::MIN;
    let mut bottom = f32::MIN;

    for hexagon in &hexagons {
        for point in hexagon.points {
            left = left.min(point.x);
            top = top.min(point.y);
            right = right.max(point.x);
            bottom = bottom.max(point.y);
        }
    }

    Mark {
        hexagons,
        bounds: Rectangle {
            x: left,
            y: top,
            width: right - left,
            height: bottom - top,
        },
    }
}

/// Parses one `<polygon>` element. `index` is its position in the file.
fn read_hexagon(index: usize, element: &str) -> Hexagon {
    let number = |text: &str| {
        text.parse::<f32>()
            .unwrap_or_else(|_| panic!("{text:?} is not a number"))
    };
    let vertices: Vec<Point> = attribute(element, "points")
        .split_whitespace()
        .map(|pair| {
            let (x, y) = pair
                .split_once(',')
                .unwrap_or_else(|| panic!("{pair:?} is not an x,y pair"));

            Point::new(number(x), number(y))
        })
        .collect();

    let points: [Point; 6] = vertices.try_into().unwrap_or_else(|found: Vec<Point>| {
        panic!("a polygon of the mark has {} vertices, want 6", found.len())
    });
    let sum = points.iter().fold(Point::ORIGIN, |sum, point| {
        Point::new(sum.x + point.x, sum.y + point.y)
    });

    Hexagon {
        points,
        centroid: Point::new(sum.x / 6.0, sum.y / 6.0),
        fill: palette::hex(attribute(element, "fill")),
        stroke_width: number(attribute(element, "stroke-width")),
        pulse: Pulse::for_index(index),
    }
}

/// The value of one attribute of an element.
///
/// The search includes the equals sign and the opening quote, so
/// `stroke` does not match inside `stroke-width`.
fn attribute<'a>(element: &'a str, name: &str) -> &'a str {
    element
        .split_once(&format!("{name}=\""))
        .unwrap_or_else(|| panic!("a polygon of the mark has no {name} attribute"))
        .1
        .split_once('"')
        .unwrap_or_else(|| panic!("the {name} attribute of a polygon has no closing quote"))
        .0
}
