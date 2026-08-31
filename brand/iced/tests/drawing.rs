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
use iced_widget::graphics::geometry::{self, Fill, Image, Path, Stroke, Style, Svg, Text, frame};
use liken_iced::mark;

/// One call the mark made on the frame.
#[derive(Debug, PartialEq)]
enum Call {
    Fill {
        style: Style,
        segments: usize,
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
        let Call::Fill { style, segments } = call else {
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
