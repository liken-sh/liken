// The media browser itself. It holds one line until plan 07 draws a library.
//
// The line is there rather than a bare ground because this image already runs
// on a screen: a person who reaches the browser before it browses reads what
// the screen is, not a black rectangle that could be a failure to draw.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::{Text, center};
use iced_winit::core::{Color, Element, Pixels, Theme};

use crate::harness::Screen;
use crate::look;

#[derive(Debug, Default)]
pub struct Browser;

impl Browser {
    pub fn new() -> Self {
        Self
    }
}

impl Screen for Browser {
    // Nothing on the screen emits a message yet, and the type says so.
    type Message = Infallible;

    fn background(&self) -> Color {
        look::BACKGROUND
    }

    fn key(&mut self, _name: &str) {}

    fn tick(&mut self, _at: f64) {}

    fn view(&self) -> Element<'_, Self::Message, Theme, Renderer> {
        center(
            Text::new("Coming soon")
                .size(Pixels(look::TITLE))
                .font(iced_winit::core::Font::with_name(look::FONT))
                .color(look::text()),
        )
        .into()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_browser_draws_on_the_theme_ground() {
        assert_eq!(Browser::new().background(), look::BACKGROUND);
    }
}
