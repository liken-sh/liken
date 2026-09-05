// The drawing layer: the primitives a screen composes, and the culling
// math they share. The primitives are a wall of art slots, a band across
// the top of a wall, a block of text, a row of buttons, a strip of
// posters and stills at one height with a "see all" slot where a screen
// asks for one, a divider between two runs of a wall, and the scrolled
// stack a page is. A screen chooses which of them it draws and where. No
// primitive reads a kind.
//
// A jump rail of rotated bars at the left of a long wall is one more.
//
// A page draws in the layers the `layers` module stacks, because inside
// one layer the renderer draws every mesh, then every image, then every
// text, whatever order the canvas drew them in. That one fact decides
// three rules here: a focus mark strokes outside the slot it marks, art
// the person did not choose dims by the image's own opacity, and a
// backdrop is a layer of its own under everything a screen draws.

pub mod band;
pub mod banner;
pub mod buttons;
pub mod clock;
pub mod curtain;
pub mod divider;
pub mod header;
pub mod layers;
pub mod people;
pub mod rail;
pub mod ratings;
pub mod scroll;
pub mod stack;
pub mod strip;
pub mod text;
pub mod volume;
pub mod wall;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight, Shaping};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle, Size};

use crate::look;
use crate::posters::{Art, Posters};

/// What a primitive reads off one of a screen's items. A screen
/// implements it for the rows it holds, so a wall, a list, and a strip
/// draw the screen's own type and copy nothing.
pub trait Card {
    /// The art path the poster store resolves, empty where the item has
    /// none.
    fn art(&self) -> &str {
        ""
    }

    /// The library the art path resolves against, empty where every item
    /// of the primitive is in the library the caller names. A person's
    /// works span libraries, so each of those slots names its own.
    fn library(&self) -> &str {
        ""
    }

    /// The name a person reads. A slot whose art has not arrived shows
    /// it.
    fn name(&self) -> &str;

    /// The second line under a headshot in a stripe: the part the person
    /// played.
    fn detail(&self) -> &str {
        ""
    }

    /// The one line under every slot of a wall, drawn muted.
    fn caption(&self) -> &str {
        self.name()
    }

    /// The second line under a slot of a wall that draws two, faint under
    /// the caption. A person's works put the parts the person played
    /// here; every other wall leaves it empty.
    fn under(&self) -> &str {
        ""
    }

    /// The line under the focused slot of a wall, drawn bright: the whole
    /// facts of the row that fit in this many characters.
    fn line_fitting(&self, _chars: usize) -> &str {
        self.caption()
    }

    /// The height of the item's art as a share of its width, which a strip
    /// draws each slot at. A poster, unless the item says otherwise, and an
    /// episode's still says otherwise.
    fn ratio(&self) -> f32 {
        wall::POSTER
    }
}

/// How bright a slot's art draws.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Tone {
    /// The art as it is.
    Full,
    /// The art of a slot the screen drew but the person did not choose,
    /// such as a sibling in a set strip.
    Dimmed,
    /// The art at an opacity a motion states, from 0 clear to 1 as it is.
    At(f32),
}

impl Tone {
    // The opacity the renderer draws the image at. The veil is the image's
    // own opacity and not a fill over it, because every fill of a layer
    // draws under every image of that layer.
    fn opacity(self) -> f32 {
        match self {
            Self::Full => 1.0,
            Self::Dimmed => look::DIM,
            Self::At(opacity) => opacity,
        }
    }
}

// Both programs draw art through this one function, so one place
// enforces the rule: the store is asked only for a slot that is drawn,
// at the slot's exact pixel size, and never for a row with no art
// path. Until a poster arrives, the slot shows the ground color and
// the row's name.
pub(crate) fn artwork<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    library: &str,
    art: &str,
    slot: Rectangle,
    name: &str,
    tone: Tone,
) {
    if !art.is_empty()
        && let Some(poster) = posters.poster(library, art, slot.width as u32, slot.height as u32)
    {
        // The ground under a dimmed slot is the black one, so a sibling
        // darkens by the same amount whatever art lies behind the slot.
        if tone == Tone::Dimmed {
            frame.fill_rectangle(slot.position(), slot.size(), look::BACKGROUND);
        }
        paint(frame, &poster, slot, tone);
        return;
    }

    // The name in an empty frame shrinks with the frame, so a headshot
    // slot fits a whole word per line where a poster slot fits several.
    frame.fill_rectangle(slot.position(), slot.size(), look::slot());
    if !name.is_empty() {
        frame.fill_text(label(
            name,
            Point::new(slot.center_x(), slot.center_y()),
            look::DETAIL.min(slot.width / 8.0),
            look::muted(),
            Alignment::Center,
            Vertical::Center,
            slot.width - 16.0,
        ));
    }
}

// Art draws band by band, each band into its share of the rectangle,
// because the renderer uploads an image of two megabytes or more on a
// later frame, and this client draws no later frame until an event.
fn paint(frame: &mut canvas::Frame<Renderer>, art: &Art, into: Rectangle, tone: Tone) {
    for (band, handle) in art.bands(into) {
        frame.draw_image(band, canvas::Image::new(handle).opacity(tone.opacity()));
    }
}

/// How far the focus mark reaches past the edge of the slot it marks: the
/// stroke's outer edge.
pub const REACH: f32 = look::MARK_GAP + look::MARK;

// How far the center line of the focus stroke sits outside the slot: the
// gap, then half of the stroke, so the stroke's inner edge is the gap
// away from the art.
const OUTSET: f32 = look::MARK_GAP + look::MARK / 2.0;

/// The rectangle the focus stroke follows, outside the slot's own edge. A
/// stroke is a mesh, and every mesh of a layer draws under every image of
/// that layer, so a stroke on the edge would lose its inner half.
pub fn marked(slot: Rectangle) -> Rectangle {
    area(
        slot.x - OUTSET,
        slot.y - OUTSET,
        slot.width + 2.0 * OUTSET,
        slot.height + 2.0 * OUTSET,
    )
}

/// The bar that marks the current member of a strip: the bottom edge of
/// the focus rectangle alone, so it reads as a place and not as focus.
pub fn underlined(slot: Rectangle) -> Rectangle {
    let around = marked(slot);
    area(
        around.x - look::MARK / 2.0,
        around.y + around.height - look::MARK / 2.0,
        around.width + look::MARK,
        look::MARK,
    )
}

// The one mark focus takes everywhere on this screen: a stroke of the
// accent outside the chosen slot.
pub(crate) fn mark(frame: &mut canvas::Frame<Renderer>, slot: Rectangle) {
    let around = marked(slot);
    frame.stroke_rectangle(
        around.position(),
        extent(around),
        canvas::Stroke::default()
            .with_color(look::mark())
            .with_width(look::MARK),
    );
}

// The underline that marks the current member of a strip, in the same
// color as the focus stroke, so one word of the look says "here".
fn underline(frame: &mut canvas::Frame<Renderer>, slot: Rectangle) {
    let bar = underlined(slot);
    frame.fill_rectangle(bar.position(), extent(bar), look::mark());
}

// One canvas text with the display's font and shaping, so every line
// on the screen is set the same way.
fn label(
    content: &str,
    position: Point,
    size: f32,
    color: Color,
    align_x: Alignment,
    align_y: Vertical,
    max_width: f32,
) -> canvas::Text {
    canvas::Text {
        content: content.to_string(),
        position,
        color,
        size: Pixels(size),
        // A block of text is measured in lines before it is drawn, so
        // the line height is stated in pixels here and not left to the
        // toolkit's ratio.
        line_height: LineHeight::Absolute(Pixels(size * text::LEADING)),
        font: Font::with_name(look::FONT),
        align_x,
        align_y,
        max_width,
        shaping: Shaping::Advanced,
    }
}

// One rounded rectangle. A radius wider than half the shape has no meaning,
// so a bar with a few pixels of fill rounds by what it has.
pub(crate) fn rounded(shape: Rectangle, radius: f32) -> canvas::Path {
    let radius = radius.min(shape.width / 2.0).min(shape.height / 2.0);
    canvas::Path::rounded_rectangle(
        shape.position(),
        Size::new(shape.width, shape.height),
        radius.into(),
    )
}

// A rectangle from its corner and its size, the shape every primitive
// builds its geometry from.
pub(crate) fn area(x: f32, y: f32, width: f32, height: f32) -> Rectangle {
    Rectangle {
        x,
        y,
        width,
        height,
    }
}

// The size of a rectangle, for the fills that take one.
pub(crate) fn extent(area: Rectangle) -> Size {
    Size::new(area.width, area.height)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn slot() -> Rectangle {
        area(100.0, 200.0, 300.0, 450.0)
    }

    #[test]
    fn the_mark_lies_outside_the_slot_it_marks() {
        let slot = slot();
        let around = marked(slot);
        let inner = look::MARK / 2.0;
        assert!(around.x + inner < slot.x);
        assert!(around.y + inner < slot.y);
        assert!(around.x + around.width - inner > slot.x + slot.width);
        assert!(around.y + around.height - inner > slot.y + slot.height);
    }

    #[test]
    fn the_mark_reaches_no_further_than_its_reach() {
        let slot = slot();
        let around = marked(slot);
        assert_eq!(slot.y - (around.y - look::MARK / 2.0), REACH);
        assert_eq!(around.center_x(), slot.center_x());
    }

    #[test]
    fn art_the_person_chose_draws_at_full_opacity() {
        assert_eq!(Tone::Full.opacity(), 1.0);
        assert_eq!(Tone::Dimmed.opacity(), look::DIM);
        assert_eq!(Tone::At(0.25).opacity(), 0.25);
    }

    struct Named;

    impl Card for Named {
        fn name(&self) -> &str {
            "A Title"
        }
    }

    #[test]
    fn a_card_states_nothing_but_its_name_unless_it_says_otherwise() {
        assert_eq!(Named.art(), "");
        assert_eq!(Named.library(), "");
        assert_eq!(Named.detail(), "");
        assert_eq!(Named.under(), "");
        assert_eq!(Named.caption(), "A Title");
        assert_eq!(Named.line_fitting(3), "A Title");
        assert_eq!(Named.ratio(), wall::POSTER);
    }
}
