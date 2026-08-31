//! `mark::draw` fills and strokes fourteen paths on a canvas frame. A frame
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

#[test]
fn the_mark_fills_and_strokes_every_hexagon() {
    let calls = calls(0.0, 0.0, 1.0);

    assert_eq!(calls.len(), mark::hexagons().len() * 2);

    for (hexagon, pair) in mark::hexagons().iter().zip(calls.chunks(2)) {
        let color = Style::Solid(hexagon.fill);
        let width = hexagon.place(CENTER, SPAN, 0.0, 0.0).stroke_width;

        assert_eq!(
            pair,
            [
                // A hexagon is seven path events: the start at the first
                // vertex, a line to each of the other five, and the end that
                // closes the sixth back to the first.
                Call::Fill {
                    style: color,
                    segments: 7,
                },
                Call::Stroke {
                    style: color,
                    width,
                    // The SVG rounds each corner with a round join, and the
                    // stroke is what rounds it.
                    join: "Round".to_string(),
                },
            ]
        );
    }
}

#[test]
fn the_alpha_reaches_the_fill_and_the_stroke() {
    for call in calls(0.5, 41.75, 0.25) {
        let style = match call {
            Call::Fill { style, .. } | Call::Stroke { style, .. } => style,
        };
        let Style::Solid(color) = style else {
            panic!("the mark draws a gradient");
        };

        assert_eq!(color.a, 0.25);
    }
}

#[test]
fn a_mark_in_motion_draws_the_same_shapes() {
    // The energy moves the vertices. It adds no shape and drops none, so the
    // call list is the same length and the same colors in the same order.
    let still = calls(0.0, 0.0, 1.0);
    let moving = calls(1.0, 41.75, 1.0);

    assert_eq!(still.len(), moving.len());
    assert_eq!(
        still.iter().map(style_of).collect::<Vec<Style>>(),
        moving.iter().map(style_of).collect::<Vec<Style>>()
    );
}

fn style_of(call: &Call) -> Style {
    match call {
        Call::Fill { style, .. } | Call::Stroke { style, .. } => *style,
    }
}

#[test]
fn a_fading_mark_keeps_its_colors() {
    let opaque = calls(0.0, 0.0, 1.0);
    let faded = calls(0.0, 0.0, 0.4);

    for (opaque, faded) in opaque.iter().zip(&faded) {
        let (Style::Solid(opaque), Style::Solid(faded)) = (style_of(opaque), style_of(faded))
        else {
            panic!("the mark draws a gradient");
        };

        assert_eq!((opaque.r, opaque.g, opaque.b), (faded.r, faded.g, faded.b));
    }
}

/// A hexagon that draws in a color the palette does not hold would mean the
/// parser read a fill from the wrong element.
#[test]
fn every_color_the_mark_draws_is_a_hexagon_fill() {
    let fills: Vec<Color> = mark::hexagons().iter().map(|it| it.fill).collect();

    for call in calls(0.0, 0.0, 1.0) {
        let Style::Solid(color) = style_of(&call) else {
            panic!("the mark draws a gradient");
        };

        assert!(fills.contains(&color), "{color:?} is no hexagon fill");
    }
}
