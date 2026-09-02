// What a page draws at its head: the item's logo where the volume holds
// one, and the item's title in large text where it does not.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{Tone, label, paint, text};
use crate::look;
use crate::posters::Posters;

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
    /// The size the title draws at where the item has no logo.
    pub size: f32,
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
        && let Some(image) = posters.fitted(
            head.library,
            head.logo,
            logo_width as u32,
            logo_height as u32,
        )
    {
        // The logo keeps its own ratio, so it takes the height the fit
        // landed at and not the height of the box.
        let (width, height) = image.size();
        paint(
            frame,
            &image,
            Rectangle {
                x: head.at.x,
                y: head.at.y,
                width: width as f32,
                height: height as f32,
            },
            Tone::Full,
        );
        return height as f32;
    }

    frame.fill_text(label(
        head.name,
        head.at,
        head.size,
        look::text(),
        Alignment::Left,
        Vertical::Top,
        head.width,
    ));
    text::height(text::lines(head.name, head.size, head.width), head.size)
}
