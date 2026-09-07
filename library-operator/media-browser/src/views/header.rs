// What a page draws at its head: the item's logo where the volume holds
// one, and the item's title in large text where it does not.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{Tone, curtain, label, paint, text};
use crate::art::Art;
use crate::look;

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
    /// The size the logo is decoded at. A page passes the loading state's
    /// size, [`crate::views::curtain::logo_decode`], so the head draws the
    /// decode the state will slide to the centre, scaled down into its box.
    pub decode: (u32, u32),
    /// The width the title text wraps in.
    pub width: f32,
    /// The size the title draws at where the item has no logo.
    pub size: f32,
    /// Whether the loading state has lifted the logo off the page. The
    /// state draws the logo itself on its way to the centre, so the head
    /// leaves the logo's box empty rather than draw a second one under
    /// it. A head with no logo draws its title as it always does, and
    /// the state fades that title under its own art.
    pub lifted: bool,
}

/// Draw the head. The answer is the height it took, so the caller stacks
/// the facts under it.
pub fn title<A: Art>(frame: &mut canvas::Frame<Renderer>, store: &mut A, head: &Title<'_>) -> f32 {
    let (logo_width, logo_height) = head.logo_box;
    if !head.logo.is_empty()
        && let Some(image) = store.fitted(head.library, head.logo, head.decode.0, head.decode.1)
    {
        // The logo keeps its own ratio inside the box, so it takes the
        // height the fit lands at and not the height of the box.
        let (decoded_width, decoded_height) = image.size();
        let ratio = decoded_height as f32 / decoded_width as f32;
        let (width, height) = curtain::fitted(logo_width, logo_height, ratio);
        if !head.lifted {
            paint(
                frame,
                &image,
                Rectangle {
                    x: head.at.x,
                    y: head.at.y,
                    width,
                    height,
                },
                Tone::Full,
            );
        }
        return height;
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
