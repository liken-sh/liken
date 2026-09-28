//! `mark::draw` fills fourteen paths on a canvas frame. A frame
//! needs a renderer, and the renderers `iced` ships draw through a GPU or a
//! window. The recorder below is a renderer that keeps every call instead, so
//! the test reads the calls the mark made on the frame and opens nothing.

use std::cell::RefCell;
use std::rc::Rc;

use iced_widget::canvas::Frame;
use iced_widget::core::{
    Background, Color, Point, Radians, Rectangle, Size, Transformation, Vector, image, renderer,
};
use iced_widget::graphics::geometry::path::lyon_path;
use iced_widget::graphics::geometry::{self, Fill, Image, Path, Stroke, Style, Svg, Text, frame};
use liken_iced::mark;

/// One call the mark made on the frame.
#[derive(Debug, PartialEq)]
enum Call {
    Fill {
        style: Style,
        segments: usize,
        points: Vec<Point>,
    },
    Stroke {
        style: Style,
        width: f32,
        join: String,
    },
}

type Log = Rc<RefCell<Vec<Call>>>;

/// A renderer that draws nothing and keeps the calls.
#[derive(Default)]
struct Recorder {
    log: Log,
}

/// The frame the recorder hands out. Every frame writes to the recorder's one
/// log, so a nested frame lands in the same list in the order it was drawn.
struct Recording {
    log: Log,
}

impl geometry::Renderer for Recorder {
    type Geometry = ();
    type Frame = Recording;

    fn new_frame(&self, _bounds: Rectangle) -> Self::Frame {
        Recording {
            log: self.log.clone(),
        }
    }

    fn draw_geometry(&mut self, _geometry: Self::Geometry) {}
}

impl frame::Backend for Recording {
    type Geometry = ();

    fn fill(&mut self, path: &Path, fill: impl Into<Fill>) {
        self.log.borrow_mut().push(Call::Fill {
            style: fill.into().style,
            segments: path.raw().iter().count(),
            // The corner of the path each segment lands on, in the order
            // the path draws them. `move_to` opens the path and every
            // `line_to` adds one, so the close carries no corner of its
            // own and this list is one shorter than `segments`.
            points: path
                .raw()
                .iter()
                .filter_map(|event| match event {
                    lyon_path::Event::Begin { at } => Some(Point::new(at.x, at.y)),
                    lyon_path::Event::Line { to, .. } => Some(Point::new(to.x, to.y)),
                    _ => None,
                })
                .collect(),
        });
    }

    fn stroke<'a>(&mut self, _path: &Path, stroke: impl Into<Stroke<'a>>) {
        let stroke = stroke.into();

        self.log.borrow_mut().push(Call::Stroke {
            style: stroke.style,
            width: stroke.width,
            join: format!("{:?}", stroke.line_join),
        });
    }

    fn width(&self) -> f32 {
        0.0
    }
    fn height(&self) -> f32 {
        0.0
    }
    fn size(&self) -> Size {
        Size::ZERO
    }
    fn center(&self) -> Point {
        Point::ORIGIN
    }
    fn push_transform(&mut self) {}
    fn pop_transform(&mut self) {}
    fn translate(&mut self, _translation: Vector) {}
    fn rotate(&mut self, _angle: impl Into<Radians>) {}
    fn scale(&mut self, _scale: impl Into<f32>) {}
    fn scale_nonuniform(&mut self, _scale: impl Into<Vector>) {}
    fn draft(&mut self, _clip_bounds: Rectangle) -> Self {
        Recording {
            log: self.log.clone(),
        }
    }
    fn paste(&mut self, _frame: Self) {}
    fn stroke_rectangle<'a>(
        &mut self,
        _top_left: Point,
        _size: Size,
        _stroke: impl Into<Stroke<'a>>,
    ) {
    }
    fn stroke_text<'a>(&mut self, _text: impl Into<Text>, _stroke: impl Into<Stroke<'a>>) {}
    fn fill_text(&mut self, _text: impl Into<Text>) {}
    fn fill_rectangle(&mut self, _top_left: Point, _size: Size, _fill: impl Into<Fill>) {}
    fn draw_image(&mut self, _bounds: Rectangle, _image: impl Into<Image>) {}
    fn draw_svg(&mut self, _bounds: Rectangle, _svg: impl Into<Svg>) {}
    fn into_geometry(self) -> Self::Geometry {}
}

impl renderer::Renderer for Recorder {
    fn start_layer(&mut self, _bounds: Rectangle) {}
    fn end_layer(&mut self) {}
    fn start_transformation(&mut self, _transformation: Transformation) {}
    fn end_transformation(&mut self) {}
    fn reset(&mut self, _new_bounds: Rectangle) {}
    fn fill_quad(&mut self, _quad: renderer::Quad, _background: impl Into<Background>) {}
    fn allocate_image(
        &mut self,
        _handle: &image::Handle,
        _callback: impl FnOnce(Result<image::Allocation, image::Error>) + Send + 'static,
    ) {
    }
}

/// Where the tests place the mark: the middle of a 1920 by 1080 canvas, at the
/// third of the width `liken`'s idle screen gives it.
const CENTER: Point = Point::new(960.0, 540.0);
const SPAN: f32 = 640.0;

/// The calls one drawing of the mark made.
fn calls(energy: f64, phase: f64, alpha: f32) -> Vec<Call> {
    let recorder = Recorder::default();
    let mut frame = Frame::new(&recorder, Size::new(1920.0, 1080.0));

    mark::draw(&mut frame, CENTER, SPAN, energy, phase, alpha);

    recorder.log.take()
}

/// Every hexagon is one filled path, and its corners are in the path rather
/// than in a stroke over it. `mark::outline` says why: a stroke is centered on
/// its path, so half of it would land on the fill, and two translucent layers
/// composite brighter than one.
#[test]
fn the_mark_fills_every_hexagon_once() {
    let calls = calls(0.0, 0.0, 1.0);

    assert_eq!(calls.len(), mark::hexagons().len());

    for (hexagon, call) in mark::hexagons().iter().zip(&calls) {
        let Call::Fill {
            style, segments, ..
        } = call
        else {
            panic!("the mark strokes where it should fill: {call:?}");
        };

        assert_eq!(*style, Style::Solid(hexagon.fill));
        // Six offset edges, an arc of six segments at each of the six
        // vertices, the start, and the close.
        assert_eq!(*segments, 6 + 6 * 6 + 2);
    }
}

fn style_of(call: &Call) -> Style {
    match call {
        Call::Fill { style, .. } | Call::Stroke { style, .. } => *style,
    }
}

/// A mark in motion, drawn under an alpha. The energy moves the vertices, and
/// it adds no shape and drops none, so the call list holds one fill for each
/// hexagon. Each call carries the hexagon's own fill at the alpha the caller
/// asked for, so a fade changes the alpha and no other channel.
#[test]
fn the_alpha_reaches_every_fill() {
    let calls = calls(0.5, 41.75, 0.25);

    assert_eq!(calls.len(), mark::hexagons().len());

    for (index, (hexagon, call)) in mark::hexagons().iter().zip(&calls).enumerate() {
        let faded = Style::Solid(Color {
            a: 0.25,
            ..hexagon.fill
        });

        assert_eq!(style_of(call), faded, "hexagon {index} fills");
    }
}

fn points_of(call: &Call) -> &[Point] {
    match call {
        Call::Fill { points, .. } => points,
        Call::Stroke { .. } => panic!("the mark strokes where it should fill: {call:?}"),
    }
}

// The tolerance is a hundredth of a pixel. The derived numbers hold to
// under 0.0005 px: liken.svg rounds its vertices to two decimals, so the
// hexagon is 0.005 degrees off regular and an arc midpoint lands about
// 0.0003 px from the whole-degree position. The errors this test exists
// for move a corner by whole pixels: an inverted normal by 14, a doubled
// offset by 7, a dropped sweep normalization by 14.
const TOLERANCE: f64 = 0.01;

// The numbers below are derived by hand from the first polygon of
// liken.svg. Its six vertices are (-4.33,-9.27), (2.77,-5.17),
// (2.77,3.03), (-4.33,7.13), (-11.43,3.03), and (-11.43,-5.17), and its
// stroke-width is 1.40. mark::bounds is 64.03 wide, so SPAN 640 scales
// by 9.995315, and the placed vertices are v0 (956.45166,453.94034),
// v1 (1027.41850,494.92114), v2 (1027.41850,576.88270),
// v3 (956.45166,617.86350), v4 (885.48490,576.88270), and
// v5 (885.48490,494.92114), with round = 1.40 * 9.995315 / 2 = 6.996720.
//
// The hexagon is pointy-top and regular, so each edge normal stands 60
// degrees from the last. Edge 0 (v0 to v1) runs (7.1,4.1) over a length
// of 8.198780, which gives the outward normal (0.500074,-0.865982) and
// the displacement (3.498881,-6.059036). Edge 1 (v1 to v2) is the
// vertical right side, normal (1,0), displacement (6.996720,0). Edge 2
// (v2 to v3) mirrors edge 0 across the horizontal, displacement
// (3.498881,6.059036). Edge 4 (v4 to v5) is the vertical left side,
// normal (-1,0), displacement (-6.996720,0), and its sign is what an
// unoriented normal would get wrong.
//
// Each arc turns 60 degrees on a circle of radius `round` about its
// vertex, so its midpoint sits 30 degrees along. At v1 the arc runs from
// -60 to 0 degrees, and its midpoint is v1 + 6.996720 * (cos -30,
// sin -30) = (1033.47784,491.42278). At v5 the arc runs from 180 to 240
// degrees, which atan2 reports as +180 and -120, so the raw sweep is
// -300 degrees and only the normalization turns it back into +60. Its
// midpoint is v5 + 6.996720 * (cos 210, sin 210) =
// (879.42556,491.42278), the one number here that a dropped
// normalization moves.
//
// `outline` opens at edge 0's start, then writes each edge's end and six
// arc corners, so edge i's end is corner 1+7i and the arc midpoint after
// it is corner 4+7i.
#[test]
fn the_outline_offsets_each_edge_outward_and_rounds_each_vertex() {
    let calls = calls(0.0, 0.0, 1.0);
    let points = points_of(&calls[0]);

    // Each row is a corner of the path, counting from the one `move_to`
    // opens with, and where the offset outline puts it.
    let expected = [
        (0, 959.95054, 447.88130),
        (1, 1030.91738, 488.86210),
        (4, 1033.47784, 491.42278),
        (7, 1034.41522, 494.92114),
        (8, 1034.41522, 576.88270),
        (14, 1030.91738, 582.94174),
        (15, 959.95054, 623.92254),
        (28, 878.48818, 576.88270),
        (29, 878.48818, 494.92114),
        (32, 879.42556, 491.42278),
    ];

    for (at, x, y) in expected {
        let drawn = points[at];

        assert!(
            (f64::from(drawn.x) - x).abs() < TOLERANCE
                && (f64::from(drawn.y) - y).abs() < TOLERANCE,
            "corner {at} draws {drawn:?} where the outline puts ({x}, {y})"
        );
    }
}
