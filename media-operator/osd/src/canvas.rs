//! The layout space and the one shape drawn in it so far. The canvas is
//! 1080 rows tall and as wide as the surface's own ratio makes it, and every
//! value that moves with the position snaps to a whole output pixel.

use iced::widget::canvas::{Frame, Path, gradient};
use iced::{Color, Point, Size};

use crate::theme;

pub mod brush;
pub mod shape;

pub use brush::{Anchor, Brush, Line, measure};

/// How the layout space maps to the real surface. `scale` maps a canvas
/// length to output pixels, and the two axes share it because the width
/// follows the surface's own ratio.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Canvas {
    pub width: f32,
    pub height: f32,
    pub scale: f32,
}

impl Default for Canvas {
    fn default() -> Self {
        Self {
            width: theme::CANVAS_WIDTH,
            height: theme::CANVAS_HEIGHT,
            scale: 1.0,
        }
    }
}

impl Canvas {
    /// The canvas the surface the compositor configured calls for. The width
    /// follows the real surface's own ratio, so a canvas pixel is square. A
    /// fixed 1920 canvas on a 21:9 screen stretched every vector drawing by a
    /// third and pulled every margin inside where it belonged. A 16:9 surface
    /// gives 1920, the width this space always held, so a 16:9 screen draws
    /// every number it drew before.
    ///
    /// A surface with no size yet gives the default canvas, because a width
    /// of nothing would place every flush-right element off the screen.
    pub fn for_output(output: Size) -> Self {
        if output.width <= 0.0 || output.height <= 0.0 {
            return Self::default();
        }
        Self {
            width: (theme::CANVAS_HEIGHT * output.width / output.height + 0.5).floor(),
            height: theme::CANVAS_HEIGHT,
            scale: output.height / theme::CANVAS_HEIGHT,
        }
    }

    /// One canvas value on the output pixel grid, so what the display draws
    /// stands still between two frames that land on the same pixel.
    pub fn snap(&self, value: f32) -> f32 {
        (value * self.scale).round() / self.scale
    }
}

/// Which side of the screen a scrim is dark on. The far side fades to clear.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Edge {
    Top,
    Bottom,
}

/// The falloff of the blurred shape's edge, as a standard deviation per unit
/// of blur width.
const EDGE_SIGMA_PER_BLUR: f32 = 0.85;

/// The falloff stops widening here. libass caps the blur it applies, so the
/// two scrims fall off over the same distance although one asks for a wider
/// blur than the other. The cap is measured off captured frames of the Lua
/// display at a 1920 by 1080 surface, and the port carries it so both scrims
/// look as they did.
const EDGE_SIGMA_LIMIT: f32 = 85.0;

/// How many stops one gradient carries. The toolkit takes eight.
const STOPS: usize = 8;

/// How far past the fading edge the scrim is drawn at all. Beyond three
/// standard deviations the plateau covers under half a percent of the
/// ground.
const TAIL: f32 = 3.0;

/// The dark gradient behind a cluster of text, so the text reads against one
/// background whatever the frame behind it.
///
/// The Lua draws one blurred rectangle, and this is the vertical profile
/// that rectangle has. A dark plateau covers the text at the screen edge, and
/// the edge falls off to clear over the reach. The rectangle bleeds past the
/// screen edge by the blur width, so the very edge reads a little under the
/// plateau.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Scrim {
    /// The blurred rectangle's own edges, in canvas rows.
    near: f32,
    far: f32,
    /// The rectangle's edge alpha as the fraction of the ground it covers.
    peak: f32,
    /// The falloff of both edges.
    sigma: f32,
    /// The side the plateau sits on.
    edge: Edge,
}

impl Scrim {
    /// One scrim band, from the row it starts at and the height it covers.
    pub fn new(y: f32, height: f32, edge: Edge) -> Self {
        let solid = height * theme::SCRIM_SOLID;
        let blur = height * theme::SCRIM_REACH;
        let (near, far) = match edge {
            Edge::Top => (y - blur, y + solid),
            Edge::Bottom => (y + height - solid, y + height + blur),
        };
        Self {
            near,
            far,
            peak: theme::opacity(theme::SCRIM_EDGE_ALPHA),
            sigma: (blur * EDGE_SIGMA_PER_BLUR).min(EDGE_SIGMA_LIMIT),
            edge,
        }
    }

    /// The scrim behind the header and the clock.
    pub fn top() -> Self {
        Self::new(0.0, theme::SCRIM_TOP_HEIGHT, Edge::Top)
    }

    /// The scrim behind the scrubber and the strip.
    pub fn bottom() -> Self {
        Self::new(
            theme::CANVAS_HEIGHT - theme::SCRIM_BOTTOM_HEIGHT,
            theme::SCRIM_BOTTOM_HEIGHT,
            Edge::Bottom,
        )
    }

    /// The fraction of the ground the scrim covers at one canvas row. Each
    /// edge of the rectangle is a Gaussian, and the two multiply.
    pub fn opacity_at(&self, row: f32) -> f32 {
        self.peak
            * normal(f64::from((row - self.near) / self.sigma))
            * normal(f64::from((self.far - row) / self.sigma))
    }

    /// The row where the plateau meets the falloff, which is the edge of the
    /// rectangle that faces the middle of the screen. The scrim reads half
    /// its plateau there.
    pub fn half(&self) -> f32 {
        match self.edge {
            Edge::Top => self.far,
            Edge::Bottom => self.near,
        }
    }

    /// The rows the scrim is drawn over, from the screen edge to where the
    /// falloff runs out.
    pub fn span(&self) -> (f32, f32) {
        match self.edge {
            Edge::Top => (
                0.0,
                (self.far + TAIL * self.sigma).min(theme::CANVAS_HEIGHT),
            ),
            Edge::Bottom => (
                (self.near - TAIL * self.sigma).max(0.0),
                theme::CANVAS_HEIGHT,
            ),
        }
    }

    /// The gradient bands the scrim is drawn as, at one fade factor. The
    /// band splits in two at the rectangle's inner edge, because eight stops
    /// over the whole span would leave the profile up to two parts in a
    /// hundred off the blurred shape's own, and eight over each half hold it
    /// inside seven parts in a thousand, which is under two steps of an
    /// eight-bit channel. The split lands on a whole output pixel, so the two
    /// fills meet with no row drawn twice.
    pub fn bands(&self, canvas: &Canvas, fade: f32) -> Vec<Band> {
        let (start, end) = self.span();
        let split = canvas.snap(self.half());
        [(start, split), (split, end)]
            .into_iter()
            .filter(|(from, to)| to > from)
            .map(|(from, to)| {
                let mut stops = [0.0; STOPS];
                for (stop, alpha) in stops.iter_mut().enumerate() {
                    let offset = stop as f32 / (STOPS - 1) as f32;
                    *alpha = self.opacity_at(from + offset * (to - from)) * fade;
                }
                Band { from, to, stops }
            })
            .collect()
    }

    /// Draw the scrim at one fade factor, in canvas units.
    pub fn draw(&self, frame: &mut Frame, canvas: &Canvas, fade: f32) {
        for band in self.bands(canvas, fade) {
            let mut ramp =
                gradient::Linear::new(Point::new(0.0, band.from), Point::new(0.0, band.to));
            for (stop, alpha) in band.stops.iter().enumerate() {
                ramp = ramp.add_stop(
                    stop as f32 / (STOPS - 1) as f32,
                    Color {
                        a: *alpha,
                        ..theme::color::SHADOW
                    },
                );
            }
            frame.fill(
                &Path::rectangle(
                    Point::new(0.0, band.from),
                    Size::new(canvas.width, band.to - band.from),
                ),
                ramp,
            );
        }
    }
}

/// One gradient the scrim draws as: the rows it covers, and the alpha at
/// each of its evenly spaced stops.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Band {
    pub from: f32,
    pub to: f32,
    pub stops: [f32; STOPS],
}

/// The normal distribution's cumulative function, which is the profile one
/// blurred edge draws. The series is Abramowitz and Stegun 7.1.26, whose
/// error stays under two parts in ten million, well under one step of an
/// eight-bit channel. It runs in double precision, because the
/// coefficients carry nine digits.
fn normal(z: f64) -> f32 {
    const P: f64 = 0.327_591_1;
    const A: [f64; 5] = [
        0.254_829_592,
        -0.284_496_736,
        1.421_413_741,
        -1.453_152_027,
        1.061_405_429,
    ];

    let x = z / std::f64::consts::SQRT_2;
    let sign = if x < 0.0 { -1.0 } else { 1.0 };
    let x = x.abs();
    let t = 1.0 / (1.0 + P * x);
    let series = A.iter().rev().fold(0.0, |total, term| (total + term) * t);
    let erf = 1.0 - series * (-x * x).exp();
    (0.5 * (1.0 + sign * erf)) as f32
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A 16:9 surface gives exactly 1920, and a 21:9 surface gives 2560 at
    /// 1080 rows.
    #[test]
    fn the_canvas_width_follows_the_surface_ratio() {
        assert_eq!(Canvas::for_output(Size::new(1920.0, 1080.0)).width, 1920.0);
        assert_eq!(Canvas::for_output(Size::new(3840.0, 2160.0)).width, 1920.0);
        assert_eq!(Canvas::for_output(Size::new(2560.0, 1080.0)).width, 2560.0);
        assert_eq!(Canvas::for_output(Size::new(1280.0, 720.0)).width, 1920.0);
        assert_eq!(Canvas::for_output(Size::new(1024.0, 768.0)).width, 1440.0);
    }

    #[test]
    fn the_scale_maps_a_canvas_length_to_output_pixels() {
        assert_eq!(Canvas::for_output(Size::new(1920.0, 1080.0)).scale, 1.0);
        assert_eq!(Canvas::for_output(Size::new(3840.0, 2160.0)).scale, 2.0);
        assert_eq!(
            Canvas::for_output(Size::new(1280.0, 720.0)).scale,
            2.0 / 3.0
        );
    }

    #[test]
    fn a_surface_with_no_size_gives_the_canvas_the_display_starts_on() {
        assert_eq!(Canvas::for_output(Size::new(0.0, 0.0)), Canvas::default());
        assert_eq!(
            Canvas::for_output(Size::new(1920.0, 0.0)),
            Canvas::default()
        );
        assert_eq!(
            Canvas::for_output(Size::new(-1.0, 1080.0)),
            Canvas::default()
        );
        assert_eq!(Canvas::default().width, 1920.0);
        assert_eq!(Canvas::default().scale, 1.0);
    }

    #[test]
    fn a_snapped_value_lands_on_a_whole_output_pixel() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        assert_eq!(canvas.snap(270.6), 271.0);
        assert_eq!(canvas.snap(763.2), 763.0);

        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        assert_eq!(half.snap(270.6), 270.0);
        assert_eq!(half.snap(271.0), 271.5);
        assert_eq!((half.snap(271.0) * half.scale).fract(), 0.0);
    }

    /// The rows the Lua's own call produces: the top scrim's rectangle runs
    /// from -123 to 270.6, and the bottom's from 763.2 to 1224.
    #[test]
    fn the_scrim_rectangles_stand_where_the_lua_puts_them() {
        let top = Scrim::top();
        assert!((top.near - -123.0).abs() < 0.01);
        assert!((top.far - 270.6).abs() < 0.01);
        assert!((top.half() - 270.6).abs() < 0.01);

        let bottom = Scrim::bottom();
        assert!((bottom.near - 763.2).abs() < 0.01);
        assert!((bottom.far - 1224.0).abs() < 0.01);
        assert!((bottom.half() - 763.2).abs() < 0.01);
    }

    /// Both scrims ask for a blur past the limit, so both fall off over the
    /// same distance.
    #[test]
    fn both_scrims_fall_off_over_the_same_distance() {
        assert_eq!(Scrim::top().sigma, EDGE_SIGMA_LIMIT);
        assert_eq!(Scrim::bottom().sigma, EDGE_SIGMA_LIMIT);
        assert!((Scrim::new(0.0, 100.0, Edge::Top).sigma - 25.5).abs() < 0.001);
    }

    /// The scrim reads half its plateau at the rectangle's inner edge, and
    /// the plateau itself in the middle of the solid part.
    #[test]
    fn the_scrim_reads_half_its_plateau_at_the_inner_edge() {
        let top = Scrim::top();
        assert!((top.opacity_at(270.6) - top.peak / 2.0).abs() < 0.005);
        assert!((top.opacity_at(60.0) - 0.7784).abs() < 0.005);
        assert!((top.opacity_at(0.0) - 0.7367).abs() < 0.005);
        assert!(top.opacity_at(525.0) < 0.005);

        let bottom = Scrim::bottom();
        assert!((bottom.opacity_at(763.2) - bottom.peak / 2.0).abs() < 0.005);
        assert!((bottom.opacity_at(1020.0) - 0.7886).abs() < 0.005);
        assert!((bottom.opacity_at(1079.0) - 0.7610).abs() < 0.005);
        assert!(bottom.opacity_at(520.0) < 0.005);
    }

    #[test]
    fn the_scrim_is_drawn_from_the_screen_edge_to_where_the_falloff_runs_out() {
        let (top_start, top_end) = Scrim::top().span();
        assert_eq!(top_start, 0.0);
        assert!((top_end - 525.6).abs() < 0.01);

        let (bottom_start, bottom_end) = Scrim::bottom().span();
        assert!((bottom_start - 508.2).abs() < 0.01);
        assert_eq!(bottom_end, 1080.0);
    }

    #[test]
    fn the_normal_function_holds_its_known_values() {
        assert!((normal(0.0) - 0.5).abs() < 1e-6);
        assert!((normal(1.0) - 0.841_344_8).abs() < 1e-6);
        assert!((normal(-1.0) - 0.158_655_2).abs() < 1e-6);
        assert!((normal(1.959_963_984_540_054) - 0.975).abs() < 1e-6);
        assert!(normal(6.0) > 0.999_999);
        assert!(normal(-6.0) < 0.000_001);
    }

    /// Eight stops over each half hold the drawn profile inside seven parts
    /// in a thousand of the blurred shape's own, which is under two steps of
    /// an eight-bit channel.
    #[test]
    fn eight_stops_over_each_half_hold_the_profile() {
        let canvas = Canvas::default();
        for scrim in [Scrim::top(), Scrim::bottom()] {
            for band in scrim.bands(&canvas, 1.0) {
                for step in 0..=400 {
                    let offset = step as f32 / 400.0;
                    let row = band.from + offset * (band.to - band.from);
                    let place = offset * (STOPS - 1) as f32;
                    let low = (place.floor() as usize).min(STOPS - 2);
                    let between = place - low as f32;
                    let drawn = band.stops[low] * (1.0 - between) + band.stops[low + 1] * between;
                    assert!(
                        (drawn - scrim.opacity_at(row)).abs() < 0.007,
                        "row {row} drawn {drawn} exact {}",
                        scrim.opacity_at(row)
                    );
                }
            }
        }
    }

    /// The two bands meet on a whole output pixel and carry the same alpha
    /// there, so no row is drawn twice and no seam shows.
    #[test]
    fn the_two_bands_meet_on_a_whole_output_pixel() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        for scrim in [Scrim::top(), Scrim::bottom()] {
            let bands = scrim.bands(&canvas, 1.0);
            assert_eq!(bands.len(), 2);
            assert_eq!(bands[0].to, bands[1].from);
            assert_eq!(bands[0].to.fract(), 0.0);
            assert_eq!(bands[0].stops[STOPS - 1], bands[1].stops[0]);
        }
    }

    /// The fade scales every stop, and a scrim at nothing carries nothing.
    #[test]
    fn the_fade_scales_every_stop() {
        let canvas = Canvas::default();
        let scrim = Scrim::top();
        let full = scrim.bands(&canvas, 1.0);
        let half = scrim.bands(&canvas, 0.5);
        for (whole, part) in full.iter().zip(half.iter()) {
            for (a, b) in whole.stops.iter().zip(part.stops.iter()) {
                assert!((a / 2.0 - b).abs() < 1e-6);
            }
        }
        for band in scrim.bands(&canvas, 0.0) {
            assert!(band.stops.iter().all(|alpha| *alpha == 0.0));
        }
    }

    /// A band with no rows in it is left out, so a scrim whose plateau falls
    /// off the canvas draws one gradient and not an empty one.
    #[test]
    fn a_band_with_no_rows_is_left_out() {
        let canvas = Canvas::default();
        let scrim = Scrim::new(0.0, 0.0, Edge::Top);
        assert!(scrim.bands(&canvas, 1.0).len() < 2);
    }
}
