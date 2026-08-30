// The media browser itself. In this plan it is the ground and nothing on it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::Space;
use iced_winit::core::{Color, Element, Length, Theme};

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
        Space::new().width(Length::Fill).height(Length::Fill).into()
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
