// What the display draws with. A brush carries the frame, the canvas it
// maps to, and the fade every alpha passes through, so a module states a
// shape or a line and nothing else.

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

/// Where a line's position falls on the line, in the ASS anchors the display
/// states. A line box carries the anchor, so a line placed by its top or its
/// bottom puts its baseline where libass puts it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Anchor {
    /// `\an7`, the anchor every shape and most lines take.
    TopLeft,
    /// `\an9`, the top-right column.
    TopRight,
    /// `\an4`, the strip's cells, which hang beside their heading.
    Left,
    /// `\an5`, the image counter, centred on its row.
    Centre,
    /// `\an2`, the scrubber's time, which sits on the row above the bar.
    BottomCentre,
}

/// One line of text: an anchor, a position in canvas units, a size from the
/// type scale, and a colour that carries the fraction of the ground it
/// covers.
#[derive(Debug, Clone, PartialEq)]
pub struct Line {
    pub content: String,
    pub at: Point,
    pub anchor: Anchor,
    /// The line box, which is the number the display states as an ASS `\fs`.
    pub size: f32,
    pub color: Color,
}

impl Line {
    /// One line at the display's own opaque alpha, which is what a caller that
    /// states no alpha draws.
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
        }
    }

    /// The same line at one coverage.
    pub fn alpha(self, alpha: f32) -> Self {
        Self {
            color: theme::at(self.color, alpha),
            ..self
        }
    }
}

// A measurement reads the shared font system, and a test measures before
// any window has loaded a face into it, so the faces load here on first
// use.
fn faces() {
    static FACES: Once = Once::new();
    FACES.call_once(liken_iced::font::load);
}

// One line as the toolkit shapes it, so a measurement and the drawn line
// read one set of glyphs.
fn shaped(content: &str, size: f32) -> text::Text<&str> {
    text::Text {
        content,
        bounds: Size::INFINITE,
        size: Pixels(theme::type_size(size)),
        line_height: LineHeight::Absolute(Pixels(size)),
        font: liken_iced::font::REGULAR,
        align_x: text::Alignment::Left,
        align_y: Vertical::Top,
        shaping: Shaping::Advanced,
        wrapping: Wrapping::None,
    }
}

// How wide one run draws, in canvas units. The toolkit shapes the same face
// and answers itself.
//
// Every layer rebuild measures the same runs again: the header, the clock,
// the strip, the chooser's rows, and the chip's label. A fade rebuilds the
// layer sixty times a second, so the measurements are held by their run and
// their size, and a new item drops them.
pub fn measure(content: &str, size: f32) -> f32 {
    MEASURED.with_borrow_mut(|held| {
        let runs = held.entry(size.to_bits()).or_default();
        if let Some(width) = runs.get(content) {
            return *width;
        }
        faces();
        let width = Paragraph::with_text(shaped(content, size)).min_width();
        runs.insert(content.to_string(), width);
        width
    })
}

// Drop every held measurement, which a new item does: its lines are not the
// last item's.
pub fn forget_measurements() {
    MEASURED.with_borrow_mut(HashMap::clear);
    CLIPPED.with_borrow_mut(HashMap::clear);
}

// The measurements and the cuts, each held under the size, and the cuts
// under the room they were cut to as well.
thread_local! {
    static MEASURED: RefCell<HashMap<u32, HashMap<String, f32>>> = RefCell::new(HashMap::new());
    static CLIPPED: RefCell<HashMap<(u32, u32), HashMap<String, String>>> =
        RefCell::new(HashMap::new());
}

/// The one mark a clipped run ends on, U+2026.
pub const ELLIPSIS: &str = "\u{2026}";

/// Drop the glyphs a run has no room for and mark the cut with an ellipsis,
/// so a long title stays inside the box it draws in.
///
/// The toolkit shapes the same face and measures the run itself, so the cut
/// is measured as it draws, and a pair of glyphs the face kerns measures as
/// the face kerns it. The cut is found by halving the run rather than walking
/// it, which measures a sixty character label six times instead of sixty, and
/// the answer is held until the item changes.
pub fn clip(content: &str, size: f32, room: f32) -> String {
    let box_ = (size.to_bits(), room.to_bits());
    if let Some(held) =
        CLIPPED.with_borrow(|held| held.get(&box_).and_then(|cuts| cuts.get(content)).cloned())
    {
        return held;
    }
    let cut = cut(content, size, room);
    CLIPPED.with_borrow_mut(|held| {
        held.entry(box_)
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
    // Every prefix of a run is at least as wide as the one before it, so the
    // widest prefix that fits is found by halving the run.
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

// The frame, the canvas, and the fade, as the modules draw through them.
// Every module draws with a brush and none touches the frame, so the
// mapping to output pixels and the fade apply once, here.
//
// A brush draws every shape as a picture that `raster` makes, so the frame
// holds pictures and text and no mesh, and the toolkit runs no
// multisampled pass for it. A decoded picture, such as the logo or the
// tile, lands over every shape in its layer, whatever order the calls come
// in: the brush draws each shape and each scrim as the call arrives, holds
// the decoded pictures back, and draws them after every shape when the
// layer is finished.
pub struct Brush<'a> {
    frame: &'a mut Frame,
    canvas: Canvas,
    fade: f32,
    /// The window's scale factor, which the rasters and the scrims read to
    /// draw at output pixels.
    density: f32,
    /// The decoded pictures this layer draws after its shapes.
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

    /// Draw the decoded pictures the layer held back, over every shape it
    /// drew. A layer that is not finished draws no picture.
    pub fn finish(self) {
        for (bounds, picture) in self.pictures {
            self.frame.draw_image(bounds, picture);
        }
    }

    // The canvas the frame maps to. A module reads it to snap a value that
    // moves with the position to a whole output pixel.
    pub fn canvas(&self) -> Canvas {
        self.canvas
    }

    // One scrim, as the picture the canvas resolved it into. It draws as the
    // call arrives, the way a shape does and a decoded picture does not, so
    // the shapes drawn after it land over it and so does every line of text.
    // It takes the brush's fade and no filtering, so each of its rows lands on
    // the output row it was resolved for.
    //
    // The tile repeats from the left edge on whole output pixels, and the
    // last repeat runs past the right edge, where the surface ends it.
    pub fn scrim(&mut self, scrim: &Picture) {
        let repeats = (scrim.bounds.width / scrim.tile).ceil() as usize;
        for repeat in 0..repeats {
            let x = scrim.bounds.x + repeat as f32 * scrim.tile;
            self.frame.draw_image(
                Rectangle::new(
                    Point::new(x, scrim.bounds.y),
                    Size::new(scrim.tile, scrim.bounds.height),
                ),
                picture(&scrim.handle, self.fade),
            );
        }
    }

    // How far the fade has moved, from 0 clear to 1 full.
    pub fn fade(&self) -> f32 {
        self.fade
    }

    /// One part of the frame at a fade of its own. The volume row and the
    /// up-next card come and go on clocks of their own, so each draws at its
    /// own factor and the caller's factor stands for the rest of the frame.
    pub fn at_fade(&mut self, fade: f32, draw: impl FnOnce(&mut Brush<'_>)) {
        let mut inner = Brush {
            frame: &mut *self.frame,
            canvas: self.canvas,
            fade,
            density: self.density,
            pictures: Vec::new(),
        };
        draw(&mut inner);
        let held = inner.pictures;
        self.pictures.extend(held);
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

    // One hexagon inside its own border. The border draws first and the fill
    // over it, so the fill's alpha shows the border through it.
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

    /// A menu panel: a faint dark fill inside a solid green border, so a
    /// chooser or an adjuster reads as one surface over the video. The border
    /// is liken's green, and the fill dims the video behind the text without
    /// hiding it.
    // The panel fades with everything else.
    pub fn panel(&mut self, shape: Rectangle) {
        let (outline, radius) = shape::outside_rounded(shape, PANEL_RADIUS, PANEL_BORDER);
        self.paint(
            &shape::rounded(outline, radius),
            Paint::Stroke(PANEL_BORDER),
            theme::at(theme::color::fill(), theme::alpha::OPAQUE),
        );
        self.paint(
            &shape::rounded(shape, PANEL_RADIUS),
            Paint::Fill,
            theme::at(theme::color::SHADOW, theme::alpha::PANEL),
        );
    }

    /// One path the caller states, for a mark the named shapes above do not
    /// cover, such as the volume glyph. The path is already where it stands,
    /// and the mark takes the same fade every other shape takes.
    pub fn shape(&mut self, path: &Path, color: Color) {
        self.paint(path, Paint::Fill, color);
    }

    /// The same mark inside a border, so a mark over another mark in the same
    /// color reads apart from it. libass draws a border outside the shape and
    /// the toolkit centers a stroke on its path, so the stroke runs at twice
    /// the width and the fill covers the half that falls inside.
    pub fn bordered(&mut self, path: &Path, color: Color, border: f32, border_color: Color) {
        self.paint(path, Paint::Stroke(border * 2.0), border_color);
        self.shape(path, color);
    }

    // One shape in one colour, as the bands of its raster. The raster holds
    // the colour at full strength, and the colour's alpha and the fade scale
    // the bands as they draw, which is the alpha a mesh in the same colour
    // would have covered its pixels with.
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

    // One decoded picture over the ground it covers. The display decodes every
    // picture to the pixel size the screen takes, so it is drawn at its own
    // size with no filtering of its own and snapped to the pixel grid.
    //
    // The picture takes the brush's fade, the way every shape and every line
    // does, so a logo, a tile, and the offer's art rise and fall with the rest
    // of the layer instead of arriving at full strength over text that is
    // still rising. The album cover is the one picture that holds the frame
    // with the OSD down, and it draws at a fade of its own.
    //
    // A picture draws over every shape in its layer and under every line of
    // text, whatever order the calls come in: the brush holds it back until
    // the layer is finished, and the renderer draws every line of text after
    // every picture.
    pub fn image(&mut self, bounds: Rectangle, handle: &Handle) {
        self.pictures.push((bounds, picture(handle, self.fade)));
    }

    /// One line of text. The scrim holds the contrast, so a line draws flat,
    /// with no outline and no shadow.
    pub fn text(&mut self, line: Line) {
        self.frame.fill_text(Text {
            content: line.content,
            position: line.at,
            color: theme::faded(line.color, self.fade),
            size: Pixels(theme::type_size(line.size)),
            line_height: LineHeight::Absolute(Pixels(line.size)),
            font: liken_iced::font::REGULAR,
            align_x: match line.anchor {
                Anchor::TopLeft | Anchor::Left => text::Alignment::Left,
                Anchor::TopRight => text::Alignment::Right,
                Anchor::Centre | Anchor::BottomCentre => text::Alignment::Center,
            },
            align_y: match line.anchor {
                Anchor::TopLeft | Anchor::TopRight => Vertical::Top,
                Anchor::Left | Anchor::Centre => Vertical::Center,
                Anchor::BottomCentre => Vertical::Bottom,
            },
            shaping: Shaping::Advanced,
            ..Text::default()
        });
    }
}

// One decoded picture as the toolkit draws it.
fn picture(handle: &Handle, fade: f32) -> Image {
    Image::new(handle.clone())
        .filter_method(FilterMethod::Nearest)
        .opacity(fade)
        .snap(true)
}

/// Every panel rounds its corners by this radius and carries a border this
/// wide.
const PANEL_RADIUS: f32 = 14.0;
const PANEL_BORDER: f32 = 2.0;

#[cfg(test)]
mod tests {
    use super::*;

    /// A longer run measures wider, and an empty one measures nothing.
    #[test]
    fn a_measurement_grows_with_the_run() {
        assert_eq!(measure("", 40.0), 0.0);
        assert!(measure("Off", 40.0) > 0.0);
        assert!(measure("English (EN)", 40.0) > measure("Off", 40.0));
        assert!(measure("Off", 64.0) > measure("Off", 40.0));
    }

    /// A run measures the same whether or not it was measured before, and a
    /// new item drops what was held.
    #[test]
    fn a_held_measurement_reads_as_the_run_measures() {
        let fresh = measure("English (EN)", 40.0);
        assert_eq!(measure("English (EN)", 40.0), fresh);
        forget_measurements();
        assert_eq!(measure("English (EN)", 40.0), fresh);
    }

    /// A run that fits keeps every glyph, and one that does not ends on the
    /// ellipsis inside the room it was given.
    #[test]
    fn a_clipped_run_ends_on_the_ellipsis_inside_its_room() {
        let long = "The Hobbit: The Desolation of Smaug, Extended Edition";
        let room = 396.0;
        let clipped = clip(long, 40.0, room);
        assert!(clipped.ends_with(ELLIPSIS));
        assert!(long.starts_with(clipped.trim_end_matches(ELLIPSIS)));
        assert!(measure(&clipped, 40.0) <= room);
        assert_eq!(clip("E05", 40.0, room), "E05");
        assert_eq!(clip("", 40.0, room), "");
    }

    /// A cut lands on a whole glyph, and a room narrower than the ellipsis
    /// leaves the ellipsis alone.
    #[test]
    fn a_cut_lands_on_a_whole_glyph() {
        let dots = "\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}\u{b7}";
        let clipped = clip(dots, 40.0, 40.0);
        assert!(clipped.ends_with(ELLIPSIS));
        assert!(
            clipped
                .chars()
                .all(|glyph| glyph == '\u{b7}' || glyph == '\u{2026}')
        );
        assert_eq!(clip("E05 the Long Tide", 40.0, 1.0), ELLIPSIS);
    }

    /// The cut the halving finds is the widest prefix that fits, which is the
    /// cut a walk of the run finds.
    #[test]
    fn the_cut_is_the_widest_prefix_that_fits() {
        let long = "Director's commentary with the cast and the crew (ENG)";
        for room in [60.0, 120.0, 240.0, 480.0] {
            let clipped = clip(long, 40.0, room);
            let kept = clipped.trim_end_matches(ELLIPSIS);
            let one_more = long[..long.len().min(kept.len() + 1)].to_string();
            assert!(measure(&clipped, 40.0) <= room, "{room} clipped too wide");
            assert!(
                one_more == kept || measure(&one_more, 40.0) + measure(ELLIPSIS, 40.0) > room,
                "{room} cut a glyph it had room for"
            );
        }
    }

    /// A picture takes the brush's own fade, so a logo, a tile, and the
    /// offer's art rise and fall with the rest of the layer. It draws at its
    /// own size, so it takes no filtering and snaps to the pixel grid.
    #[test]
    fn a_picture_takes_the_brushs_own_fade() {
        let handle = Handle::from_rgba(1, 1, vec![0, 0, 0, 255]);
        for fade in [0.0, 0.35, 1.0] {
            let drawn = picture(&handle, fade);
            assert_eq!(drawn.opacity, fade);
            assert_eq!(drawn.filter_method, FilterMethod::Nearest);
            assert!(drawn.snap);
        }
    }

    #[test]
    fn a_line_states_its_alpha_or_takes_the_opaque_one() {
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
