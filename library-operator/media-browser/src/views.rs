// The drawing layer: the primitives a screen composes, and the culling
// math they share. The primitives are a wall of art slots, a list of
// rows, a band across the top of a wall, a backdrop under a page, a
// block of text, a row of buttons, and a strip of posters with one
// marked. A screen chooses which of them it draws and where. No
// primitive reads a kind.

pub mod band;
pub mod buttons;
pub mod header;
pub mod list;
pub mod scroll;
pub mod strip;
pub mod text;
pub mod wall;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight, Shaping};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle, Size};

use crate::look;
use crate::posters::Posters;

/// What a primitive reads off one of a screen's items. A screen
/// implements it for the rows it holds, so a wall, a list, and a strip
/// draw the screen's own type and copy nothing.
pub trait Card {
    /// The art path the poster store resolves, empty where the item has
    /// none.
    fn art(&self) -> &str {
        ""
    }

    /// The name a person reads. A slot whose art has not arrived shows
    /// it.
    fn name(&self) -> &str;

    /// The secondary line a list row draws at its right.
    fn detail(&self) -> &str {
        ""
    }

    /// The line under the focused slot of a wall.
    fn line(&self) -> &str {
        self.name()
    }
}

// Both programs draw art through this one function, so one place
// enforces the rule: the store is asked only for a slot that is drawn,
// at the slot's exact pixel size, and never for a row with no art
// path. Until a poster arrives, the slot shows the ground color and
// the row's name.
fn artwork<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    library: &str,
    art: &str,
    slot: Rectangle,
    name: &str,
) {
    if !art.is_empty()
        && let Some(poster) = posters.poster(library, art, slot.width as u32, slot.height as u32)
    {
        frame.draw_image(slot, canvas::Image::new(poster));
        return;
    }

    frame.fill_rectangle(slot.position(), slot.size(), look::slot());
    if !name.is_empty() {
        frame.fill_text(label(
            name,
            Point::new(slot.center_x(), slot.center_y()),
            look::DETAIL,
            look::muted(),
            Alignment::Center,
            Vertical::Center,
            slot.width - 16.0,
        ));
    }
}

// The one mark focus takes everywhere on this screen: a stroke of the
// accent around the chosen slot.
fn mark(frame: &mut canvas::Frame<Renderer>, slot: Rectangle) {
    frame.stroke_rectangle(
        slot.position(),
        slot.size(),
        canvas::Stroke::default()
            .with_color(look::accent())
            .with_width(4.0),
    );
}

// The veil over art a screen draws but has not chosen.
fn dim(frame: &mut canvas::Frame<Renderer>, slot: Rectangle) {
    frame.fill_rectangle(slot.position(), slot.size(), look::scrim());
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
