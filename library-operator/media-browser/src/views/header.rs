// The ground a page draws over: the item's backdrop full bleed, and one
// gradient over it that darkens toward the lower left, where the text
// sits. An item with no backdrop draws over the ground color alone.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{extent, label, text};
use crate::look;
use crate::posters::Posters;

// The point on the gradient where the dim gives the art back.
const CLEARS_AT: f32 = 0.62;

/// Draw the backdrop and its dim over the whole frame. The store is asked
/// at the frame's own size, which is the size the prefetch asked for
/// while the wall held focus, so the page finds the decode in the cache.
pub fn backdrop<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    library: &str,
    art: &str,
    bounds: Rectangle,
) {
    if !art.is_empty()
        && let Some(image) = posters.poster(library, art, bounds.width as u32, bounds.height as u32)
    {
        frame.draw_image(bounds, canvas::Image::new(image));
    } else {
        frame.fill_rectangle(bounds.position(), extent(bounds), look::BACKGROUND);
    }

    frame.fill_rectangle(
        bounds.position(),
        extent(bounds),
        canvas::gradient::Linear::new(
            Point::new(bounds.x, bounds.y + bounds.height),
            Point::new(bounds.x + bounds.width, bounds.y),
        )
        .add_stop(0.0, look::shade())
        .add_stop(CLEARS_AT, look::scrim())
        .add_stop(1.0, look::CLEAR),
    );
}

/// What a page draws at its head: the item's logo where the volume holds
/// one, and the item's title in large text where it does not.
pub struct Title<'a> {
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The path of the logo file, empty where the item has none.
    pub logo: &'a str,
    /// The name a person reads.
    pub name: &'a str,
    /// The top left corner the head draws from.
    pub at: Point,
    /// The box a logo draws in.
    pub logo_box: (f32, f32),
    /// The width the title text wraps in.
    pub width: f32,
}

/// Draw the head. The answer is the height it took, so the caller stacks
/// the facts under it.
pub fn title<P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    head: &Title<'_>,
) -> f32 {
    let (logo_width, logo_height) = head.logo_box;
    if !head.logo.is_empty()
        && let Some(image) = posters.poster(
            head.library,
            head.logo,
            logo_width as u32,
            logo_height as u32,
        )
    {
        frame.draw_image(
            Rectangle {
                x: head.at.x,
                y: head.at.y,
                width: logo_width,
                height: logo_height,
            },
            canvas::Image::new(image),
        );
        return logo_height;
    }

    frame.fill_text(label(
        head.name,
        head.at,
        look::TITLE,
        look::text(),
        Alignment::Left,
        Vertical::Top,
        head.width,
    ));
    text::height(text::lines(head.name, look::TITLE, head.width), look::TITLE)
}
