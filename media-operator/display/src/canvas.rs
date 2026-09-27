//! The layout space and the one shape drawn in it so far. The canvas is
//! 1080 rows tall and as wide as the surface's own ratio makes it, and every
//! value that moves with the position snaps to a whole output pixel.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use crate::theme;

pub mod brush;
pub mod raster;
pub mod shape;

pub use brush::{Anchor, Brush, ELLIPSIS, Line, clip, forget_measurements, measure};

/// How the layout space maps to the real surface. `scale` maps a canvas
/// length to output pixels, and the two axes share it because the width
/// follows the surface's own ratio.
///
/// The fields are private and [`Canvas::for_output`] is the one constructor,
/// because a scale of nothing divides through every conversion below and
/// turns every position it touches into NaN.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Canvas {
    width: f32,
    scale: f32,
}

impl Default for Canvas {
    fn default() -> Self {
        Self {
            width: theme::CANVAS_WIDTH,
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
            scale: output.height / theme::CANVAS_HEIGHT,
        }
    }

    /// The canvas the surface gives: as wide as its ratio makes it, and always
    /// [`theme::CANVAS_HEIGHT`] rows tall.
    pub fn width(&self) -> f32 {
        self.width
    }

    pub fn height(&self) -> f32 {
        theme::CANVAS_HEIGHT
    }

    /// How many output pixels one canvas length covers.
    pub fn scale(&self) -> f32 {
        self.scale
    }

    /// One canvas length in whole output pixels. Every request, every
    /// placement, and every snap rounds one this way, so the two halves of a
    /// position never disagree by a pixel.
    pub fn to_pixels(&self, value: f32) -> f32 {
        (value * self.scale + 0.5).floor()
    }

    /// One length in output pixels as the canvas length it covers.
    pub fn to_canvas(&self, pixels: f32) -> f32 {
        pixels / self.scale
    }

    /// One canvas value on the output pixel grid, so what the display draws
    /// stands still between two frames that land on the same pixel.
    pub fn snap(&self, value: f32) -> f32 {
        self.to_canvas(self.to_pixels(value))
    }

    /// The column every flush-right element ends on.
    pub fn right(&self) -> f32 {
        self.width - theme::MARGIN_X
    }

    /// The middle of the screen, which a centred element measures from.
    pub fn centre_x(&self) -> f32 {
        self.width / 2.0
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

/// The falloff stops widening here, so the two scrims fall off over the same
/// distance although one asks for a wider blur than the other.
/// The distance is the display's own: both scrims soften over 85 rows of
/// the 1080-row canvas, so the header's scrim and the scrubber's read as
/// one family whatever reach each one states.
const EDGE_SIGMA_LIMIT: f32 = 85.0;

/// How many stops one band carries, the number the toolkit's gradient takes.
const STOPS: usize = 8;

/// How far past the fading edge the scrim is drawn at all. Beyond three
/// standard deviations the plateau covers under half a percent of the
/// ground.
const TAIL: f32 = 3.0;

/// The dark gradient behind a cluster of text, so the text reads against one
/// background whatever the frame behind it.
///
/// This is the vertical profile one blurred rectangle has. A dark plateau
/// covers the text at the screen edge, and the edge falls off to clear over
/// the reach. The rectangle bleeds past the screen edge by the blur width, so
/// the very edge reads a little under the plateau.
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
            peak: theme::alpha::SCRIM_EDGE,
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

    /// The gradient bands the scrim's profile is resolved into, at full
    /// strength. The band splits in two at the rectangle's inner edge,
    /// because eight stops over the whole span would leave the profile up to
    /// two parts in a hundred off the blurred shape's own, and eight over each
    /// half hold it inside seven parts in a thousand, which is under two steps
    /// of an eight-bit channel. The split lands on a whole output pixel, so the
    /// two bands meet with no row counted twice.
    pub fn bands(&self, canvas: &Canvas) -> Vec<Band> {
        let (start, end) = self.span();
        let split = canvas.snap(self.half());
        [(start, split), (split, end)]
            .into_iter()
            .filter(|(from, to)| to > from)
            .map(|(from, to)| {
                let mut stops = [0.0; STOPS];
                for (stop, alpha) in stops.iter_mut().enumerate() {
                    let offset = stop as f32 / (STOPS - 1) as f32;
                    *alpha = self.opacity_at(from + offset * (to - from));
                }
                Band { from, to, stops }
            })
            .collect()
    }

    /// The scrim as the picture the display draws: a tile [`TILE`] pixels
    /// wide with one row for each row of the output, which the layer repeats
    /// across the width.
    ///
    /// The scrim is a picture and not a gradient fill because of what a
    /// gradient costs. The toolkit fills a gradient with a shader that
    /// searches an array of its stops for every pixel it covers, and the two
    /// scrims cover about half the screen. Measured on Intel graphics at 3840
    /// by 2160, the two gradients took two thirds of the GPU time of a frame
    /// with the OSD up. A picture costs one texture read per pixel. So the
    /// profile is resolved here, once per surface, and the frame only draws
    /// it.
    ///
    /// `density` is the window's scale factor, the output pixels one logical
    /// pixel covers. A row of the picture then covers exactly one row of the
    /// output, and each row carries the value the bands give at the middle of
    /// that row. The picture's edge at the screen edge lands on a whole pixel,
    /// so the rows line up with the output's own.
    ///
    /// Each pixel carries its row's value with noise added before it is
    /// rounded to a byte, so the rounding lands above the value on some
    /// pixels and below it on others. Without the noise, every pixel of a row
    /// rounds the same way, and the falloff shows as flat bands with a step
    /// between them on an eight-bit screen. The noise is the hash and the
    /// amplitude of the toolkit's gradient shader, the grain the scrims were
    /// judged by eye with.
    pub fn picture(&self, canvas: &Canvas, density: f32) -> Option<Picture> {
        let (start, end) = self.span();
        let per_row = canvas.scale() * density;
        let rows = ((end - start) * per_row).ceil();
        if !rows.is_finite() || rows < 1.0 {
            return None;
        }
        let height = rows / per_row;
        let top = match self.edge {
            Edge::Top => start,
            Edge::Bottom => end - height,
        };
        let bands = self.bands(canvas);
        let [red, green, blue] = [
            theme::color::SHADOW.r,
            theme::color::SHADOW.g,
            theme::color::SHADOW.b,
        ]
        .map(|channel| (channel * 255.0).round() as u8);
        let pixels: Vec<u8> = (0..rows as u32)
            .flat_map(|index| {
                let row = top + (index as f32 + 0.5) / per_row;
                let alpha = bands
                    .iter()
                    .find(|band| band.from <= row && row < band.to)
                    .map_or(0.0, |band| band.alpha_at(row));
                let y = (top * per_row + index as f32 + 0.5) / density;
                (0..TILE).flat_map(move |column| {
                    let x = (column as f32 + 0.5) / density;
                    let level = alpha * 255.0 + DITHER * (2.0 * noise(x, y) - 1.0);
                    [red, green, blue, level.round().clamp(0.0, 255.0) as u8]
                })
            })
            .collect();
        Some(Picture {
            handle: Handle::from_rgba(TILE, rows as u32, pixels),
            bounds: Rectangle::new(Point::new(0.0, top), Size::new(canvas.width(), height)),
            tile: TILE as f32 / per_row,
        })
    }
}

/// How many output pixels wide one scrim tile is. The layer repeats the tile
/// across the row, and the toolkit draws every repeat in one call, so a
/// narrow tile costs no more to draw than one picture as wide as the screen.
/// A tile this wide holds both scrims in 0.6 MB at 1080 rows and in 1.1 MB
/// at 2160, where a picture as wide as the screen would hold 8 MB and 34 MB
/// on machines whose graphics share one gigabyte of memory. Each tile also
/// stays under the size the toolkit uploads within the frame that asks.
pub const TILE: u32 = 128;

/// How far the noise moves a pixel's value, in steps of one byte: up to this
/// much above or below. It is the amplitude of the toolkit's gradient shader
/// in its own units, 0.3 of a step, applied to the alpha the picture stores.
const DITHER: f32 = 0.3;

/// The noise at one position in logical pixels, from 0 to 1. It is the hash
/// the toolkit's gradient shader dithers with, so the grain matches that
/// shader's.
fn noise(x: f32, y: f32) -> f32 {
    ((x * 12.9898 + y * 78.233).sin() * 43758.547).fract().abs()
}

/// The pictures of both scrims, for one canvas and one scale factor. Only the
/// fade changes between two frames, and the picture takes the fade as its
/// opacity, so the pictures are resolved once per surface.
#[derive(Debug, Clone, Default)]
pub struct Scrims {
    pub top: Option<Picture>,
    pub bottom: Option<Picture>,
}

impl Scrims {
    pub fn for_canvas(canvas: &Canvas, density: f32) -> Self {
        Self {
            top: Scrim::top().picture(canvas, density),
            bottom: Scrim::bottom().picture(canvas, density),
        }
    }
}

/// One scrim as the toolkit draws it: the tile, the canvas rectangle the
/// repeats of the tile cover, and the canvas width of one repeat.
#[derive(Debug, Clone)]
pub struct Picture {
    pub handle: Handle,
    pub bounds: Rectangle,
    pub tile: f32,
}

impl Picture {
    /// The alpha bytes of the tile, one row of [`TILE`] per output row, top
    /// to bottom. A test reads the picture through this and not through the
    /// handle, because a handle takes an id of its own on every call.
    pub fn rows(&self) -> Vec<Vec<u8>> {
        match &self.handle {
            Handle::Rgba { pixels, .. } => pixels
                .chunks(4 * TILE as usize)
                .map(|row| row.chunks(4).map(|pixel| pixel[3]).collect())
                .collect(),
            _ => Vec::new(),
        }
    }
}

/// One gradient band of the scrim: the rows it covers, and the fraction of
/// the ground it covers at each of its evenly spaced stops.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Band {
    pub from: f32,
    pub to: f32,
    pub stops: [f32; STOPS],
}

impl Band {
    /// The fraction of the ground the band covers at one canvas row, eased
    /// from one stop to the next with a smoothstep and not a straight line.
    /// That is the ease the toolkit's gradient shader applies between two
    /// stops, and the scrims were judged by eye as that shader draws them, so
    /// the picture keeps the same ease.
    pub fn alpha_at(&self, row: f32) -> f32 {
        let last = STOPS - 1;
        let place = ((row - self.from) / (self.to - self.from)).clamp(0.0, 1.0) * last as f32;
        let low = (place.floor() as usize).min(last - 1);
        let between = place - low as f32;
        let eased = between * between * (3.0 - 2.0 * between);
        self.stops[low] + (self.stops[low + 1] - self.stops[low]) * eased
    }
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
        assert_eq!(
            Canvas::for_output(Size::new(1920.0, 1080.0)).width(),
            1920.0
        );
        assert_eq!(
            Canvas::for_output(Size::new(3840.0, 2160.0)).width(),
            1920.0
        );
        assert_eq!(
            Canvas::for_output(Size::new(2560.0, 1080.0)).width(),
            2560.0
        );
        assert_eq!(Canvas::for_output(Size::new(1280.0, 720.0)).width(), 1920.0);
        assert_eq!(Canvas::for_output(Size::new(1024.0, 768.0)).width(), 1440.0);
    }

    #[test]
    fn the_scale_maps_a_canvas_length_to_output_pixels() {
        assert_eq!(Canvas::for_output(Size::new(1920.0, 1080.0)).scale(), 1.0);
        assert_eq!(Canvas::for_output(Size::new(3840.0, 2160.0)).scale(), 2.0);
        assert_eq!(
            Canvas::for_output(Size::new(1280.0, 720.0)).scale(),
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
        assert_eq!(Canvas::default().width(), 1920.0);
        assert_eq!(Canvas::default().scale(), 1.0);
    }

    #[test]
    fn a_snapped_value_lands_on_a_whole_output_pixel() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        assert_eq!(canvas.snap(270.6), 271.0);
        assert_eq!(canvas.snap(763.2), 763.0);

        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        assert_eq!(half.snap(270.6), 270.0);
        assert_eq!(half.snap(271.0), 271.5);
        assert_eq!((half.snap(271.0) * half.scale()).fract(), 0.0);
    }

    /// The scrim rows: the top scrim's rectangle runs from -123 to 270.6, and
    /// the bottom's from 763.2 to 1224.
    #[test]
    fn the_scrim_rectangles_stand_at_their_own_rows() {
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
            for band in scrim.bands(&canvas) {
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
            let bands = scrim.bands(&canvas);
            assert_eq!(bands.len(), 2);
            assert_eq!(bands[0].to, bands[1].from);
            assert_eq!(bands[0].to.fract(), 0.0);
            assert_eq!(bands[0].stops[STOPS - 1], bands[1].stops[0]);
        }
    }

    /// Between two stops the band eases with a smoothstep, the curve the
    /// toolkit's gradient shader drew, so it reads each stop at the stop, the
    /// mean of two stops halfway between them, and less than a straight line
    /// a quarter of the way.
    #[test]
    fn a_band_eases_between_its_stops_the_way_the_gradient_drew() {
        let band = Band {
            from: 0.0,
            to: 70.0,
            stops: [0.8, 0.8, 0.6, 0.4, 0.2, 0.1, 0.0, 0.0],
        };
        let near = |row: f32, alpha: f32| (band.alpha_at(row) - alpha).abs() < 1e-5;
        assert!(near(20.0, 0.6));
        assert!(near(25.0, 0.5));
        assert!(near(22.5, 0.6 - 0.2 * 0.156_25));
        assert!(near(-5.0, 0.8));
        assert!(near(75.0, 0.0));
    }

    /// The value the bands give at the middle of each output row the picture
    /// covers, in steps of one byte, and the tile's row for it.
    fn rows_and_values(scrim: Scrim, density: f32) -> Vec<(Vec<u8>, f32)> {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        let picture = scrim.picture(&canvas, density).expect("a scrim with rows");
        let bands = scrim.bands(&canvas);
        picture
            .rows()
            .into_iter()
            .enumerate()
            .map(|(index, row)| {
                let at = picture.bounds.y + (index as f32 + 0.5) / density;
                let value = bands
                    .iter()
                    .find(|band| band.from <= at && at < band.to)
                    .map_or(0.0, |band| band.alpha_at(at));
                (row, value * 255.0)
            })
            .collect()
    }

    /// Every pixel of a scrim's picture carries the value the bands give at
    /// the middle of its output row, moved by the noise and rounded, so it
    /// stays within 0.3 of a step and the rounding of that value. Nothing
    /// past the rows the bands cover draws.
    #[test]
    fn each_pixel_carries_its_rows_value_within_the_noise() {
        for (scrim, density) in [
            (Scrim::top(), 1.0),
            (Scrim::bottom(), 1.0),
            (Scrim::top(), 2.0),
        ] {
            for (row, value) in rows_and_values(scrim, density) {
                assert_eq!(row.len(), TILE as usize);
                for alpha in row {
                    assert!(
                        (f32::from(alpha) - value).abs() <= 0.8,
                        "{alpha} for {value}"
                    );
                }
            }
        }
    }

    /// A row whose value falls near the middle of two steps rounds up on some
    /// pixels and down on others, which is the dither that keeps the falloff
    /// from showing as bands. A row whose value is a whole step rounds the
    /// same way on every pixel.
    #[test]
    fn a_row_between_two_steps_carries_both() {
        let rows = rows_and_values(Scrim::top(), 1.0);
        let between: Vec<_> = rows
            .iter()
            .filter(|(_, value)| (value.fract() - 0.5).abs() < 0.1)
            .collect();
        assert!(!between.is_empty());
        for (row, value) in between {
            let mut levels = row.clone();
            levels.sort_unstable();
            levels.dedup();
            assert_eq!(levels.len(), 2, "{value}");
        }
        let whole = rows
            .iter()
            .find(|(_, value)| value.fract() < 0.05 && *value > 1.0)
            .expect("a row on a whole step");
        assert!(whole.0.iter().all(|alpha| *alpha == whole.0[0]));
    }

    /// The picture is the shadow colour at every pixel.
    #[test]
    fn the_picture_is_the_shadow_colour() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        for (scrim, density) in [
            (Scrim::top(), 1.0),
            (Scrim::bottom(), 1.0),
            (Scrim::top(), 2.0),
        ] {
            let picture = scrim.picture(&canvas, density).expect("a scrim with rows");
            let Handle::Rgba { pixels, .. } = &picture.handle else {
                panic!("a scrim is RGBA");
            };
            assert!(pixels.chunks(4).all(|pixel| pixel[..3] == [0, 0, 0]));
        }
    }

    /// The picture covers the whole width, and it starts at the screen edge
    /// on a whole output pixel, so each of its rows lands on one output row.
    /// A window at twice the scale carries twice the rows over the same
    /// canvas rows, and its tile covers half the canvas width, so each repeat
    /// still covers [`TILE`] output pixels.
    #[test]
    fn the_picture_lands_on_whole_output_rows_from_the_screen_edge() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        let top = Scrim::top().picture(&canvas, 1.0).expect("the top scrim");
        assert_eq!(top.bounds.y, 0.0);
        assert_eq!(top.bounds.width, 1920.0);
        assert_eq!(top.rows().len(), 526);
        assert_eq!(top.tile, TILE as f32);

        let bottom = Scrim::bottom()
            .picture(&canvas, 1.0)
            .expect("the bottom scrim");
        assert_eq!(bottom.bounds.y + bottom.bounds.height, 1080.0);
        assert_eq!(bottom.bounds.y.fract(), 0.0);

        let doubled = Scrim::top().picture(&canvas, 2.0).expect("the top scrim");
        assert_eq!(doubled.rows().len(), 1052);
        assert_eq!(doubled.bounds.height, 526.0);
        assert_eq!(doubled.tile, TILE as f32 / 2.0);
    }

    /// Both scrims of one surface are the pictures its two scrims resolve to,
    /// however many times they are asked for.
    #[test]
    fn the_scrims_of_one_surface_are_its_two_pictures() {
        let canvas = Canvas::for_output(Size::new(1920.0, 1080.0));
        let scrims = Scrims::for_canvas(&canvas, 1.0);
        for (held, scrim) in [(scrims.top, Scrim::top()), (scrims.bottom, Scrim::bottom())] {
            let held = held.expect("a scrim with rows");
            let fresh = scrim.picture(&canvas, 1.0).expect("a scrim with rows");
            assert_eq!(held.bounds, fresh.bounds);
            assert_eq!(held.rows(), fresh.rows());
            assert!(held.rows().iter().flatten().any(|alpha| *alpha > 0));
        }
    }

    /// A scale of nothing would divide every row of the picture away, so it
    /// makes no picture.
    #[test]
    fn a_scale_of_nothing_makes_no_picture() {
        assert!(Scrim::top().picture(&Canvas::default(), 0.0).is_none());
    }

    /// A band with no rows in it is left out, so a scrim whose plateau falls
    /// off the canvas draws one gradient and not an empty one.
    #[test]
    fn a_band_with_no_rows_is_left_out() {
        let canvas = Canvas::default();
        let scrim = Scrim::new(0.0, 0.0, Edge::Top);
        assert!(scrim.bands(&canvas).len() < 2);
    }
}
