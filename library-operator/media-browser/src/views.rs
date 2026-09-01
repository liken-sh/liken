// The drawing layer: a wall, a list, and the culling math they share.
// The two programs draw the rows they are given and hold no code per
// kind; the level model decides which program a level gets.

pub mod list;
pub mod scroll;
pub mod wall;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, Shaping};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle};

use crate::look;
use crate::posters::Posters;

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
        font: Font::with_name(look::FONT),
        align_x,
        align_y,
        max_width,
        shaping: Shaping::Advanced,
        ..canvas::Text::default()
    }
}
