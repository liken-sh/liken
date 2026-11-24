// The drawing API of the display: `Brush` draws shapes, pictures, and
// text into an iced canvas frame, and `measure` and `clip` size text with
// the same shaping the frame uses.

use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::Once;

use iced::advanced::graphics::text::Paragraph;
use iced::advanced::image::{FilterMethod, Handle, Image};
use iced::advanced::text::{self, LineHeight, Paragraph as _, Shaping, Wrapping};
use iced::alignment::Vertical;
use iced::widget::canvas::{Frame, Path, Text};
use iced::{Color, Pixels, Point, Rectangle, Size};

use super::raster::{Paint, raster};
use super::{Canvas, Picture, shape};
use crate::theme;

/// The point of a line's box that is placed at `Line::at`. Each variant
/// places text the way the ASS alignment tag in its doc does, and a line
/// anchored by its top or bottom edge puts its baseline where libass puts
/// it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Anchor {
    /// `\an7`.
    TopLeft,
    /// `\an9`.
    TopRight,
    /// `\an6`, centred vertically.
    Right,
    /// `\an4`, centred vertically.
    Left,
    /// `\an5`.
    Centre,
    /// `\an2`.
    BottomCentre,
}

/// One line of text: its content, an anchor position in canvas units, a
/// size from the type scale, and a colour whose alpha sets its opacity.
#[derive(Debug, Clone, PartialEq)]
pub struct Line {
    pub content: String,
    pub at: Point,
    pub anchor: Anchor,
    /// The line box height, which matches the ASS `\fs` value.
    pub size: f32,
    pub color: Color,
    /// Draws the line in the brand font's italic face, which the cast
    /// panel uses for a character's name.
    pub italic: bool,
}

impl Line {
    /// A fully opaque line. `alpha` makes it translucent.
    pub fn new(
        content: impl Into<String>,
        at: Point,
        anchor: Anchor,
        size: f32,
        color: Color,
    ) -> Self {
        Self {
            content: content.into(),
            at,
            anchor,
            size,
            color: theme::at(color, theme::alpha::OPAQUE),
            italic: false,
        }
    }

    /// This line in the italic face.
    pub fn italic(self) -> Self {
        Self {
            italic: true,
            ..self
        }
    }

    /// This line with its colour's alpha set to `alpha`.
    pub fn alpha(self, alpha: f32) -> Self {
        Self {
            color: theme::at(self.color, alpha),
            ..self
        }
    }
}

// Loads the brand fonts on first use, because a test measures text before
// any window has loaded them.
fn faces() {
    static FACES: Once = Once::new();
    FACES.call_once(liken_iced::font::load);
}

// The text parameters shared by measuring and drawing, so a measured
// width matches the width of the drawn line.
fn shaped(content: &str, size: f32, italic: bool) -> text::Text<&str> {
    text::Text {
        content,
        bounds: Size::INFINITE,
        size: Pixels(theme::type_size(size)),
        line_height: LineHeight::Absolute(Pixels(size)),
        font: face(italic),
        align_x: text::Alignment::Left,
        align_y: Vertical::Top,
        shaping: Shaping::Advanced,
        wrapping: Wrapping::None,
    }
}

/// The width of `content` at line box `size`, in canvas units, as iced
/// shapes it.
///
// A fade rebuilds the layer on every frame, which measures the same text
// again, so widths are cached until `forget_measurements` clears them.
pub fn measure(content: &str, size: f32) -> f32 {
    measure_face(content, size, false)
}

/// The width of `content` in the italic face, for a word such as a
/// track's role that follows regular text on one line.
pub fn measure_italic(content: &str, size: f32) -> f32 {
    measure_face(content, size, true)
}

fn measure_face(content: &str, size: f32, italic: bool) -> f32 {
    MEASURED.with_borrow_mut(|cache| {
        let runs = cache.entry((size.to_bits(), italic)).or_default();
        if let Some(width) = runs.get(content) {
            return *width;
        }
        faces();
        let width = Paragraph::with_text(shaped(content, size, italic)).min_width();
        runs.insert(content.to_string(), width);
        width
    })
}

fn face(italic: bool) -> iced::Font {
    match italic {
        true => liken_iced::font::ITALIC,
        false => liken_iced::font::REGULAR,
    }
}

/// Clears the cached widths and clips. The window calls it when a new
/// item starts, so the caches hold only the current item's text.
pub fn forget_measurements() {
    MEASURED.with_borrow_mut(HashMap::clear);
    CLIPPED.with_borrow_mut(HashMap::clear);
}

// The keys are the bits of the size, for a measure also whether the face
// is italic, and for a clip also the bits of the room,
// then the content.
thread_local! {
    static MEASURED: RefCell<HashMap<(u32, bool), HashMap<String, f32>>> =
        RefCell::new(HashMap::new());
    static CLIPPED: RefCell<HashMap<(u32, u32), HashMap<String, String>>> =
        RefCell::new(HashMap::new());
}

/// The character that ends a clipped run, U+2026.
pub const ELLIPSIS: &str = "\u{2026}";

/// Cuts `content` to its longest prefix that fits in `room` canvas units
/// with an ellipsis after it. A run that fits is returned whole. Each
/// prefix is measured with `measure`, so kerning counts, and the result is
/// cached until `forget_measurements` runs.
pub fn clip(content: &str, size: f32, room: f32) -> String {
    let box_ = (size.to_bits(), room.to_bits());
    if let Some(cached) =
        CLIPPED.with_borrow(|cache| cache.get(&box_).and_then(|cuts| cuts.get(content)).cloned())
    {
        return cached;
    }
    let cut = cut(content, size, room);
    CLIPPED.with_borrow_mut(|cache| {
        cache
            .entry(box_)
            .or_default()
            .insert(content.to_string(), cut.clone());
    });
    cut
}

fn cut(content: &str, size: f32, room: f32) -> String {
    if measure(content, size) <= room {
        return content.to_string();
    }
    let room = room - measure(ELLIPSIS, size);
    // A longer prefix is never narrower than a shorter one, so a binary
    // search finds the longest prefix that fits.
    let ends: Vec<usize> = content
        .char_indices()
        .map(|(at, _)| at)
        .chain(std::iter::once(content.len()))
        .collect();
    let (mut low, mut high) = (0, ends.len() - 1);
    while low < high {
        let middle = low + (high - low).div_ceil(2);
        if measure(&content[..ends[middle]], size) > room {
            high = middle - 1;
        } else {
            low = middle;
        }
    }
    format!("{}{ELLIPSIS}", &content[..ends[low]])
}

/// Draws into one iced canvas `Frame`. Modules draw only through a brush,
/// so the mapping from canvas units to output pixels and the fade apply in
/// one place.
///
// Every shape is rasterised by `raster` and drawn as an image, so the
// frame holds only images and text and iced runs no multisampled mesh
// pass. Decoded pictures, such as the logo and the tile, are queued, and
// `finish` draws them after every shape, so they cover the shapes whatever
// order the calls came in.
pub struct Brush<'a> {
    frame: &'a mut Frame,
    canvas: Canvas,
    fade: f32,
    /// The window's scale factor, used to rasterise at output pixels.
    density: f32,
    /// The decoded pictures queued for `finish`.
    pictures: Vec<(Rectangle, Image)>,
}

impl<'a> Brush<'a> {
    pub fn new(frame: &'a mut Frame, canvas: Canvas, fade: f32, density: f32) -> Self {
        Self {
            frame,
            canvas,
            fade,
            density,
            pictures: Vec::new(),
        }
    }

    /// Draws the queued pictures over the layer's shapes. A brush that is
    /// dropped without `finish` draws no pictures.
    pub fn finish(self) {
        for (bounds, picture) in self.pictures {
            self.frame.draw_image(bounds, picture);
        }
    }

    /// The canvas mapping of this frame, which a module uses to snap a
    /// moving value to a whole output pixel.
    pub fn canvas(&self) -> Canvas {
        self.canvas
    }

    /// Draws a scrim picture now, unlike `image`, which queues, so shapes
    /// and text drawn after it cover it. It is drawn at the brush's fade with
    /// no filtering, so each row of the picture lands on the output row it
    /// was made for, and its single column stretches across the width.
    pub fn scrim(&mut self, scrim: &Picture) {
        self.frame
            .draw_image(scrim.bounds, picture(&scrim.handle, self.fade));
    }

    /// Draws a soft darkening over `bounds` now, its ramps as long as
    /// `reach` says and `peak` dark at its middle, at output resolution.
    pub fn shade(&mut self, bounds: Rectangle, reach: super::shade::Reach, peak: f32) {
        let per_unit = self.canvas.scale() * self.density;
        for (strip, handle) in super::shade::strips(bounds, reach, peak, per_unit) {
            self.frame.draw_image(strip, picture(&handle, self.fade));
        }
    }

    /// The fade factor, from 0 (invisible) to 1 (fully drawn).
    pub fn fade(&self) -> f32 {
        self.fade
    }

    /// Runs `draw` at `fade` in place of this brush's fade, for a part that
    /// fades on its own timer, such as the volume row.
    pub fn at_fade(&mut self, fade: f32, draw: impl FnOnce(&mut Brush<'_>)) {
        let mut inner = Brush {
            frame: &mut *self.frame,
            canvas: self.canvas,
            fade,
            density: self.density,
            pictures: Vec::new(),
        };
        draw(&mut inner);
        let queued = inner.pictures;
        self.pictures.extend(queued);
    }

    pub fn rect(&mut self, shape: Rectangle, color: Color) {
        self.paint(
            &Path::rectangle(
                Point::new(shape.x, shape.y),
                Size::new(shape.width, shape.height),
            ),
            Paint::Fill,
            color,
        );
    }

    pub fn rounded(&mut self, shape: Rectangle, radius: f32, color: Color) {
        self.paint(&shape::rounded(shape, radius), Paint::Fill, color);
    }

    // The border is drawn first and the fill over it, so a translucent fill
    // shows the border through it.
    pub fn hexagon(
        &mut self,
        at: Point,
        radius: f32,
        color: Color,
        border: f32,
        border_color: Color,
    ) {
        if border > 0.0 {
            let (outline, grown) = shape::outside_hexagon(at, radius, border);
            self.paint(
                &shape::hexagon(outline, grown),
                Paint::Stroke(border),
                border_color,
            );
        }
        self.paint(&shape::hexagon(at, radius), Paint::Fill, color);
    }

    /// A panel: a translucent dark fill inside a solid border in the accent
    /// color. The fill dims the video behind the text without hiding it.
    pub fn panel(&mut self, shape: Rectangle) {
        self.panel_rounded(shape, PANEL_RADIUS);
    }

    /// A panel with corner radius `corner`, for a button that rounds at
    /// half its height.
    pub fn panel_rounded(&mut self, shape: Rectangle, corner: f32) {
        let (outline, radius) = shape::outside_rounded(shape, corner, PANEL_BORDER);
        self.paint(
            &shape::rounded(outline, radius),
            Paint::Stroke(PANEL_BORDER),
            theme::at(theme::color::fill(), theme::alpha::OPAQUE),
        );
        self.paint(
            &shape::rounded(shape, corner),
            Paint::Fill,
            theme::at(theme::color::SHADOW, theme::alpha::PANEL),
        );
    }

    /// The panel's dark fill without its border. The track panel marks
    /// its focus inside the plate, so a bordered plate would show a
    /// second frame around it.
    pub fn plate(&mut self, shape: Rectangle) {
        self.paint(
            &shape::rounded(shape, PANEL_RADIUS),
            Paint::Fill,
            theme::at(theme::color::SHADOW, theme::alpha::PANEL),
        );
    }

    /// Fills a path in canvas coordinates, for a mark that the shapes above
    /// do not cover, such as the track panel's dot.
    pub fn shape(&mut self, path: &Path, color: Color) {
        self.paint(path, Paint::Fill, color);
    }

    /// Fills a path with a border around it, so a mark over another mark
    /// of the same color stays distinct. libass draws a border outside the
    /// shape, but a stroke is centred on its path, so the stroke is drawn at
    /// twice the width and the fill covers the half that falls inside.
    pub fn bordered(&mut self, path: &Path, color: Color, border: f32, border_color: Color) {
        self.paint(path, Paint::Stroke(border * 2.0), border_color);
        self.shape(path, color);
    }

    // The raster is at full strength, and each band is drawn at the
    // colour's alpha times the fade, which gives the opacity a mesh of the
    // same colour would have.
    fn paint(&mut self, path: &Path, paint: Paint, color: Color) {
        let alpha = color.a * self.fade;
        if alpha <= 0.0 {
            return;
        }
        for band in raster(path, paint, color, self.canvas.scale(), self.density) {
            self.frame.draw_image(
                band.bounds,
                Image::new(band.handle)
                    .filter_method(FilterMethod::Nearest)
                    .opacity(alpha)
                    .snap(true),
            );
        }
    }

    /// Queues a decoded picture at `bounds`. The display decodes every
    /// picture at the pixel size the screen needs, so it is drawn unscaled,
    /// unfiltered, and snapped to the pixel grid.
    ///
    /// The picture is drawn at the brush's fade, so it fades with the text
    /// around it. A picture that must stay at full strength, such as the
    /// album cover, is drawn through `at_fade(1.0, ..)`.
    ///
    /// It is drawn over every shape and under every line of text, whatever
    /// order the calls came in, because `finish` draws it after the shapes
    /// and the renderer draws all text after all pictures.
    pub fn image(&mut self, bounds: Rectangle, handle: &Handle) {
        self.pictures.push((bounds, picture(handle, self.fade)));
    }

    /// Draws one line of text with no outline or shadow, because the scrim
    /// provides the contrast.
    pub fn text(&mut self, line: Line) {
        self.frame.fill_text(drawn(line, self.fade));
    }
}

/// The toolkit's text for a line, at `fade`.
///
/// The toolkit lays a line out in a box as wide as `max_width`, and it
/// places a right-to-left line, such as a track titled in Arabic, from the
/// right edge of that box. With the default, an unbounded width, that edge
/// is at infinity, and the glyph cache overflows when it turns the place
/// into a pixel. So each line is laid out in a box as wide as the line
/// itself, and the display places the box by the line's measured width
/// for a right or centred anchor.
fn drawn(line: Line, fade: f32) -> Text {
    let width = measure_face(&line.content, line.size, line.italic);
    let left = match line.anchor {
        Anchor::TopLeft | Anchor::Left => line.at.x,
        Anchor::TopRight | Anchor::Right => line.at.x - width,
        Anchor::Centre | Anchor::BottomCentre => line.at.x - width / 2.0,
    };
    Text {
        content: line.content,
        position: Point::new(left, line.at.y),
        // The toolkit aligns a right-to-left line to the right of the box
        // even when told left, so the box has only half a pixel of room
        // over the measured width. The measure shapes the line the same
        // way, so the line does not wrap.
        max_width: width + 0.5,
        color: theme::faded(line.color, fade),
        size: Pixels(theme::type_size(line.size)),
        line_height: LineHeight::Absolute(Pixels(line.size)),
        font: face(line.italic),
        align_x: text::Alignment::Left,
        align_y: match line.anchor {
            Anchor::TopLeft | Anchor::TopRight => Vertical::Top,
            Anchor::Left | Anchor::Right | Anchor::Centre => Vertical::Center,
            Anchor::BottomCentre => Vertical::Bottom,
        },
        shaping: Shaping::Advanced,
    }
}

fn picture(handle: &Handle, fade: f32) -> Image {
    Image::new(handle.clone())
        .filter_method(FilterMethod::Nearest)
        .opacity(fade)
        .snap(true)
}

/// The corner radius and the border width of a panel.
const PANEL_RADIUS: f32 = 14.0;
const PANEL_BORDER: f32 = 2.0;

#[cfg(test)]
mod tests {
    use super::*;

    /// A line of right-to-left text, such as a subtitle track titled in
    /// Arabic or Hebrew, lays out its glyphs at finite places near its
    /// position. With an unbounded width, the toolkit placed them from the
    /// right edge of an infinite line, and the glyph cache overflowed.
    #[test]
    fn a_right_to_left_line_lays_out_near_its_position() {
        let cases = [
            (
                "Arabic",
                "\u{627}\u{644}\u{639}\u{631}\u{628}\u{64a}\u{629}",
                Anchor::Left,
            ),
            (
                "Hebrew",
                "\u{5e2}\u{5d1}\u{5e8}\u{5d9}\u{5ea}",
                Anchor::Left,
            ),
            ("mixed", "Title \u{5e2}\u{5d1}", Anchor::Left),
            (
                "Hebrew, right-aligned",
                "\u{5e2}\u{5d1}\u{5e8}\u{5d9}\u{5ea}",
                Anchor::Right,
            ),
            (
                "Hebrew, centred",
                "\u{5e2}\u{5d1}\u{5e8}\u{5d9}\u{5ea}",
                Anchor::Centre,
            ),
        ];
        for (name, content, anchor) in cases {
            let line = Line::new(
                content,
                Point::new(984.0, 600.0),
                anchor,
                38.5,
                Color::WHITE,
            );
            let width = measure(content, 38.5);
            let (low, high) = match anchor {
                Anchor::Right => (984.0 - width, 984.0),
                Anchor::Centre => (984.0 - width / 2.0, 984.0 + width / 2.0),
                _ => (984.0, 984.0 + width),
            };
            let mut xs = Vec::new();
            drawn(line, 1.0).draw_with(|path, _| {
                for event in path.raw().iter() {
                    xs.push(event.from().x);
                }
            });
            assert!(!xs.is_empty(), "{name}: no glyph drew");
            assert!(
                xs.iter()
                    .all(|x| x.is_finite() && *x >= low - 2.0 && *x <= high + 2.0),
                "{name}: glyphs from {:?} to {:?}, want between {low} and {high}",
                xs.iter().cloned().fold(f32::INFINITY, f32::min),
                xs.iter().cloned().fold(f32::NEG_INFINITY, f32::max)
            );
        }
    }

    /// A longer run measures wider, and an empty one measures zero.
    #[test]
    fn a_measurement_grows_with_the_run() {
        assert_eq!(measure("", 40.0), 0.0);
        assert!(
            measure("Off", 40.0) > 0.0,
            "a nonempty run measures wider than 0"
        );
        assert!(
            measure("English (EN)", 40.0) > measure("Off", 40.0),
            "a longer run measures wider than a shorter one"
        );
        assert!(
            measure("Off", 64.0) > measure("Off", 40.0),
            "a run at size 64 measures wider than the same run at size 40"
        );
    }

    /// A cached measurement equals a fresh one, including after the cache
    /// is cleared.
    #[test]
    fn a_cached_measurement_equals_a_fresh_one() {
        let fresh = measure("English (EN)", 40.0);
        assert_eq!(measure("English (EN)", 40.0), fresh);
        forget_measurements();
        assert_eq!(measure("English (EN)", 40.0), fresh);
    }

    /// A run that fits is returned whole, and one that does not ends on
    /// the ellipsis and fits inside the room.
    #[test]
    fn a_clipped_run_ends_on_the_ellipsis_inside_its_room() {
        let long = "The Long Tide: The Return of the Lamp, Extended Edition";
        let room = 396.0;
        let clipped = clip(long, 40.0, room);
        assert!(
            clipped.ends_with(ELLIPSIS),
            "{clipped:?} ends with the ellipsis"
        );
        assert!(
            long.starts_with(clipped.trim_end_matches(ELLIPSIS)),
            "{clipped:?} is a prefix of the text plus the ellipsis"
        );
        assert!(
            measure(&clipped, 40.0) <= room,
            "{clipped:?} is wider than the room {room}"
        );
        assert_eq!(clip("E05", 40.0, room), "E05");
        assert_eq!(clip("", 40.0, room), "");
    }

    /// A cut keeps whole chars, and a room narrower than the ellipsis
    /// gives only the ellipsis.
    #[test]
    fn a_cut_lands_on_a_whole_glyph() {
        let dots = "\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}";
        let clipped = clip(dots, 40.0, 40.0);
        assert!(
            clipped.ends_with(ELLIPSIS),
            "{clipped:?} ends with the ellipsis"
        );
        assert!(
            clipped
                .chars()
                .all(|glyph| glyph == '\u{b7}' || glyph == '\u{2026}'),
            "{clipped:?} has only whole dots and the ellipsis"
        );
        assert_eq!(clip("E05 the Long Tide", 40.0, 1.0), ELLIPSIS);
    }

    /// The cut keeps the longest prefix that fits.
    #[test]
    fn the_cut_is_the_widest_prefix_that_fits() {
        let long = "Director's commentary with the cast and the crew (ENG)";
        for room in [60.0, 120.0, 240.0, 480.0] {
            let clipped = clip(long, 40.0, room);
            let kept = clipped.trim_end_matches(ELLIPSIS);
            let one_more = long[..long.len().min(kept.len() + 1)].to_string();
            assert!(
                measure(&clipped, 40.0) <= room,
                "the clip for room {room} is wider than the room"
            );
            assert!(
                one_more == kept || measure(&one_more, 40.0) + measure(ELLIPSIS, 40.0) > room,
                "the clip for room {room} stops before a character that still fits"
            );
        }
    }

    #[test]
    fn a_picture_is_drawn_at_the_given_fade_unfiltered_and_snapped() {
        let handle = Handle::from_rgba(1, 1, vec![0, 0, 0, 255]);
        for fade in [0.0, 0.35, 1.0] {
            let drawn = picture(&handle, fade);
            assert_eq!(drawn.opacity, fade);
            assert_eq!(drawn.filter_method, FilterMethod::Nearest);
            assert!(drawn.snap, "the picture is snapped to pixels");
        }
    }

    #[test]
    fn a_line_is_opaque_until_given_an_alpha() {
        let line = Line::new(
            "3:01 pm",
            Point::new(1824.0, 90.0),
            Anchor::TopRight,
            34.0,
            theme::color::text(),
        );
        assert_eq!(line.color.a, theme::alpha::OPAQUE);
        assert_eq!(
            line.clone().alpha(theme::alpha::SUBDUED).color.a,
            theme::alpha::SUBDUED
        );
        assert_eq!(line.color.r, theme::color::text().r);
        assert_eq!(line.at, Point::new(1824.0, 90.0));
    }
}
