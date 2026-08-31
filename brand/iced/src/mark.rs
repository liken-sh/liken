//! The `liken` mark: fourteen hexagons in a mosaic, drawn onto an `iced`
//! canvas.
//!
//! `liken.svg` is the original, and this module embeds it and parses it. The
//! geometry comes out in the SVG's own coordinate space with its bounding box,
//! so a caller maps it onto a canvas of any size.

use std::sync::LazyLock;

use iced_widget::canvas::{Frame, LineJoin, Path, Stroke};
use iced_widget::core::{Color, Point, Rectangle};
use iced_widget::graphics::geometry;

use crate::palette;
use crate::pulse::Pulse;

/// The mark, embedded so a consumer needs no file at run time.
const SVG: &str = include_str!("../../liken.svg");

/// One hexagon of the mark, in the SVG's coordinate space.
///
/// The SVG rounds the corners by stroking each polygon in its own fill color,
/// so `fill` and `stroke_width` together are the drawn shape, and a drawing
/// that fills alone has sharp corners.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Hexagon {
    /// The six vertices, in the order the polygon writes them.
    pub points: [Point; 6],
    /// The mean of the six vertices. A regular hexagon's centroid is its
    /// center, so a change of size about this point grows and shrinks the shape
    /// in place.
    pub centroid: Point,
    /// The fill, which is also the stroke color.
    pub fill: Color,
    /// The stroke width, in SVG units.
    pub stroke_width: f32,
    /// The motion this hexagon runs on, from its position in the file.
    pub pulse: Pulse,
}

/// One hexagon placed on a canvas at one moment, ready to draw.
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

/// The fourteen hexagons, in the order `liken.svg` writes them.
pub fn hexagons() -> &'static [Hexagon] {
    &MARK.hexagons
}

/// The box that holds every vertex, in SVG units.
///
/// The box comes from the vertices and not from the `viewBox`, which is square
/// and larger than the mark. A caller that centers this box centers the mark
/// itself, and a change to `liken.svg` needs no other edit.
pub fn bounds() -> Rectangle {
    MARK.bounds
}

impl Hexagon {
    /// This hexagon on a canvas at one moment.
    ///
    /// `center` is where the center of [`bounds`] lands, and `span` is the
    /// width the whole mark fills, both in canvas units. The height follows the
    /// width, so the mark keeps its ratio. `energy` runs from 0 at rest to 1 at
    /// full swing, and `phase` is the animation clock in seconds.
    ///
    /// The SVG y axis and the canvas y axis both run down, so the shape needs
    /// no flip.
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

        // A mark at rest draws the still shape. The pulse returns exactly 1.0
        // at energy 0, and a multiply by 1.0 about the centroid does not return
        // the vertex it started from in floating point, so a resting mark takes
        // the mapped vertices as they are.
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
            // The stroke rounds the corners, and a stroke that grew with the
            // pulse would read as a change of weight rather than a change of
            // size. The width follows the canvas alone.
            stroke_width: self.stroke_width * scale,
        }
    }
}

/// Draw the whole mark into a canvas frame.
///
/// `center`, `span`, `energy`, and `phase` are [`Hexagon::place`]'s. `alpha`
/// runs from 0 clear to 1 opaque, and it applies to every hexagon, so the mark
/// fades as one shape.
///
/// The caller sets how much of its canvas the mark takes. `liken`'s idle screen
/// gives it a third of the smaller of the canvas width and the canvas height at
/// 16:9, so a screen wider than 16:9 draws the same mark with more room beside
/// it. That rule is the screen's layout, not the brand's.
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
        let color = Color {
            a: alpha,
            ..placed.fill
        };
        let path = Path::new(|builder| {
            builder.move_to(placed.points[0]);
            for point in &placed.points[1..] {
                builder.line_to(*point);
            }
            builder.close();
        });

        frame.fill(&path, color);

        // The SVG rounds each corner by stroking the polygon in its fill color
        // with a round join, and the stroke is the same path the fill took.
        //
        // The width is the SVG's, scaled onto the canvas. `logo.lua` halves it
        // because an ASS border grows outward from the shape, while an SVG
        // stroke and an `iced` stroke are centered on the path. The three
        // drawings then reach the same outer edge.
        frame.stroke(
            &path,
            Stroke::default()
                .with_color(color)
                .with_width(placed.stroke_width)
                .with_line_join(LineJoin::Round),
        );
    }
}

/// The hexagons of an SVG document, and the box that holds them.
///
/// The parser reads the `<polygon>` elements and nothing else, because the mark
/// is fourteen polygons and the geometry above comes from their vertices. The
/// file is embedded, so a document this parser cannot read is a defect in
/// `liken.svg`, and every failure panics with what it looked for.
fn read(svg: &str) -> Mark {
    let hexagons: Vec<Hexagon> = svg
        .split("<polygon")
        .skip(1)
        .enumerate()
        .map(|(index, tail)| {
            let element = tail
                .split_once('>')
                .expect("a <polygon> element does not close")
                .0;

            read_hexagon(index, element)
        })
        .collect();

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

/// One `<polygon>` element, at its position in the file.
fn read_hexagon(index: usize, element: &str) -> Hexagon {
    let number = |text: &str| {
        text.parse::<f32>()
            .unwrap_or_else(|_| panic!("{text} is no number"))
    };
    let vertices: Vec<Point> = attribute(element, "points")
        .split_whitespace()
        .map(|pair| {
            let (x, y) = pair
                .split_once(',')
                .unwrap_or_else(|| panic!("{pair} is no x,y pair"));

            Point::new(number(x), number(y))
        })
        .collect();

    let points: [Point; 6] = vertices.try_into().unwrap_or_else(|found: Vec<Point>| {
        panic!("a polygon of the mark has {} vertices", found.len())
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
/// The search carries the equals sign and the quote, so one attribute does not
/// match another whose name starts the same way, such as `stroke` and
/// `stroke-width`.
fn attribute<'a>(element: &'a str, name: &str) -> &'a str {
    element
        .split_once(&format!("{name}=\""))
        .unwrap_or_else(|| panic!("a polygon of the mark has no {name}"))
        .1
        .split_once('"')
        .unwrap_or_else(|| panic!("the {name} of a polygon does not close its quote"))
        .0
}
