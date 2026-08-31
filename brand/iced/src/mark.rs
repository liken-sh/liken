//! The `liken` mark: fourteen hexagons in a mosaic, drawn onto an `iced`
//! canvas.
//!
//! `liken.svg` is the original, and this module embeds it and parses it. The
//! geometry comes out in the SVG's own coordinate space with its bounding box,
//! so a caller maps it onto a canvas of any size.

use std::sync::LazyLock;

use iced_widget::canvas::{Frame, Path};
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
/// The mark is one filled path per hexagon, so a frame that carries a
/// transform of its own needs no further argument here: the geometry goes
/// through that transform the way every other path does. [`outline`] says why
/// the corners are geometry rather than a stroke.
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

        frame.fill(
            &outline(&placed),
            Color {
                a: alpha,
                ..placed.fill
            },
        );
    }
}

/// How many straight segments draw one rounded corner. Six leaves the arc
/// within a fortieth of a pixel of true on a mark that fills a third of a 1080
/// row screen, which is finer than the rasterizer resolves.
const ARC_STEPS: usize = 6;

/// The path one placed hexagon draws, corners and all.
///
/// `liken.svg` rounds each corner by stroking the polygon in its own fill
/// color with a round join, so the drawn shape is the hexagon grown outward by
/// half the stroke width, with an arc of that radius at each vertex. This
/// builds that shape, and the caller fills it once.
///
/// One filled shape rather than a fill under a stroke, for two reasons. A
/// stroke is centered on the path it follows, so half of it lands on the fill,
/// and two translucent layers composite brighter than one: a mark drawn at
/// half opacity showed its own outline as a rim half again as bright as its
/// interior. And the toolkit tessellates a stroke at the width it is given
/// after it has transformed the path, so a stroke width has to carry the
/// frame's own scale while the geometry does not, and a mark drawn from a
/// canvas of another size closed up its gaps.
fn outline(placed: &Placed) -> Path {
    let round = placed.stroke_width / 2.0;
    let points = placed.points;

    let mut cx = 0.0;
    let mut cy = 0.0;
    for point in points {
        cx += point.x / points.len() as f32;
        cy += point.y / points.len() as f32;
    }

    // The outward normal of one edge. Two point away from an edge, and the one
    // that leads away from the centroid is the outward one, whichever way the
    // file wound its vertices.
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

    let edge = |i: usize| {
        let (a, b) = (points[i], points[(i + 1) % points.len()]);
        let (nx, ny) = normal(a, b);
        (
            Point::new(a.x + nx * round, a.y + ny * round),
            Point::new(b.x + nx * round, b.y + ny * round),
        )
    };

    Path::new(|builder| {
        builder.move_to(edge(0).0);

        for i in 0..points.len() {
            builder.line_to(edge(i).1);

            // The arc around the vertex the next edge starts at, from where
            // this edge's offset ends to where the next one begins. Both lie
            // at `round` from that vertex, so the arc is the round join.
            let vertex = points[(i + 1) % points.len()];
            let from = edge(i).1;
            let to = edge((i + 1) % points.len()).0;

            let start = (from.y - vertex.y).atan2(from.x - vertex.x);
            let mut sweep = (to.y - vertex.y).atan2(to.x - vertex.x) - start;
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
