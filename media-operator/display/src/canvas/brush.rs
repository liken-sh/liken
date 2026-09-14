// What the display draws with. A brush carries the frame, the canvas it
// maps to, and the fade every alpha passes through, so a module states a
// shape or a line and nothing else.

use std::sync::Once;

use iced::advanced::graphics::text::Paragraph;
use iced::advanced::image::{FilterMethod, Handle, Image};
use iced::advanced::text::{self, LineHeight, Paragraph as _, Shaping, Wrapping};
use iced::alignment::Vertical;
use iced::widget::canvas::{Fill, Frame, Path, Stroke, Style, Text};
use iced::{Color, Pixels, Point, Rectangle, Size};

use super::{Canvas, Scrim, shape};
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
/// type scale, a colour, and an ASS alpha.
#[derive(Debug, Clone, PartialEq)]
pub struct Line {
    pub content: String,
    pub at: Point,
    pub anchor: Anchor,
    /// The line box, which is the number the display states as an ASS `\fs`.
    pub size: f32,
    pub color: Color,
    pub alpha: u8,
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
            color,
            alpha: theme::alpha::OPAQUE,
        }
    }

    /// The same line at one ASS alpha.
    pub fn alpha(self, alpha: u8) -> Self {
        Self { alpha, ..self }
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
pub fn measure(content: &str, size: f32) -> f32 {
    faces();
    Paragraph::with_text(shaped(content, size)).min_width()
}

// The frame, the canvas, and the fade, as the modules draw through them.
// Every module draws with a brush and none touches the frame, so the
// mapping to output pixels and the fade apply once, here.
pub struct Brush<'a> {
    frame: &'a mut Frame,
    canvas: Canvas,
    fade: f32,
}

impl<'a> Brush<'a> {
    pub fn new(frame: &'a mut Frame, canvas: Canvas, fade: f32) -> Self {
        Self {
            frame,
            canvas,
            fade,
        }
    }

    // The canvas the frame maps to. A module reads it to snap a value that
    // moves with the position to a whole output pixel.
    pub fn canvas(&self) -> Canvas {
        self.canvas
    }

    // One scrim. It draws a gradient of its own rather than a fill of one
    // color, because a blurred shape is a gradient once it is drawn.
    pub fn scrim(&mut self, scrim: &Scrim) {
        let (canvas, fade) = (self.canvas, self.fade);
        scrim.draw(self.frame, &canvas, fade);
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
        };
        draw(&mut inner);
    }

    pub fn rect(&mut self, shape: Rectangle, color: Color, alpha: u8) {
        self.frame.fill_rectangle(
            Point::new(shape.x, shape.y),
            Size::new(shape.width, shape.height),
            theme::faded(color, alpha, self.fade),
        );
    }

    pub fn rounded(&mut self, shape: Rectangle, radius: f32, color: Color, alpha: u8) {
        self.frame.fill(
            &shape::rounded(shape, radius),
            theme::faded(color, alpha, self.fade),
        );
    }

    // One hexagon inside its own border. The border draws first and the fill
    // over it, so the fill's alpha shows the border through it.
    pub fn hexagon(
        &mut self,
        at: Point,
        radius: f32,
        color: Color,
        alpha: u8,
        border: f32,
        border_color: Color,
    ) {
        if border > 0.0 {
            let (outline, grown) = shape::outside_hexagon(at, radius, border);
            self.frame.stroke(
                &shape::hexagon(outline, grown),
                Stroke {
                    width: border,
                    style: Style::Solid(theme::faded(border_color, alpha, self.fade)),
                    ..Stroke::default()
                },
            );
        }
        self.frame.fill(
            &shape::hexagon(at, radius),
            theme::faded(color, alpha, self.fade),
        );
    }

    /// A menu panel: a faint dark fill inside a solid green border, so a
    /// chooser or an adjuster reads as one surface over the video. The border
    /// is liken's green, and the fill dims the video behind the text without
    /// hiding it.
    // The panel fades with everything else.
    pub fn panel(&mut self, shape: Rectangle) {
        let (outline, radius) = shape::outside_rounded(shape, PANEL_RADIUS, PANEL_BORDER);
        self.frame.stroke(
            &shape::rounded(outline, radius),
            Stroke {
                width: PANEL_BORDER,
                style: Style::Solid(theme::faded(
                    theme::color::fill(),
                    theme::alpha::OPAQUE,
                    self.fade,
                )),
                ..Stroke::default()
            },
        );
        self.frame.fill(
            &shape::rounded(shape, PANEL_RADIUS),
            Fill::from(theme::faded(
                theme::color::SHADOW,
                theme::alpha::PANEL,
                self.fade,
            )),
        );
    }

    /// One path the caller states, for a mark the named shapes above do not
    /// cover, such as the volume glyph. The path is already where it stands,
    /// and the mark takes the same fade every other shape takes.
    pub fn shape(&mut self, path: &Path, color: Color, alpha: u8) {
        self.frame.fill(path, theme::faded(color, alpha, self.fade));
    }

    /// The same mark inside a border, so a mark over another mark in the same
    /// color reads apart from it. libass draws a border outside the shape and
    /// the toolkit centers a stroke on its path, so the stroke runs at twice
    /// the width and the fill covers the half that falls inside.
    pub fn bordered(
        &mut self,
        path: &Path,
        color: Color,
        alpha: u8,
        border: f32,
        border_color: Color,
    ) {
        self.frame.stroke(
            path,
            Stroke {
                width: border * 2.0,
                style: Style::Solid(theme::faded(border_color, alpha, self.fade)),
                ..Stroke::default()
            },
        );
        self.shape(path, color, alpha);
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
    // text, whatever order the calls come in, because the renderer draws one
    // layer in four passes: quads, then meshes, then images, then text.
    pub fn image(&mut self, bounds: Rectangle, handle: &Handle) {
        self.frame.draw_image(bounds, picture(handle, self.fade));
    }

    /// One line of text. The scrim holds the contrast, so a line draws flat,
    /// with no outline and no shadow.
    pub fn text(&mut self, line: Line) {
        self.frame.fill_text(Text {
            content: line.content,
            position: line.at,
            color: theme::faded(line.color, line.alpha, self.fade),
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

    /// The face's own advances, through the toolkit. One unit of ASS size is
    /// 0.7541 em, and the middle dot and the ellipsis measure 0.2490 and
    /// 0.9480 of an em.
    #[test]
    fn a_run_measures_by_the_faces_own_advances() {
        let em = 28.0 * 0.754_1;
        assert!((measure("\u{00B7}", 28.0) - em * 0.249).abs() < 0.5);
        assert!((measure("\u{2026}", 28.0) - em * 0.948).abs() < 0.5);
    }

    /// A longer run measures wider, and an empty one measures nothing.
    #[test]
    fn a_measurement_grows_with_the_run() {
        assert_eq!(measure("", 40.0), 0.0);
        assert!(measure("Off", 40.0) > 0.0);
        assert!(measure("English (EN)", 40.0) > measure("Off", 40.0));
        assert!(measure("Off", 64.0) > measure("Off", 40.0));
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
        assert_eq!(line.alpha, theme::alpha::OPAQUE);
        assert_eq!(
            line.clone().alpha(theme::alpha::SUBDUED).alpha,
            theme::alpha::SUBDUED
        );
        assert_eq!(line.at, Point::new(1824.0, 90.0));
    }
}
