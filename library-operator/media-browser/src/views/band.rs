// The band across the top of a wall draws the heading alone. It is a
// layer of its own over the screen, because a row that scrolls up under
// it must not show through, and inside one layer the renderer draws
// every fill, then every image, then every text, whatever the order they
// were drawn in.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Element, Length, Point, Rectangle, Theme, mouse};

use super::{area, extent, label};
use crate::look;

/// The height the band takes off the top of the frame.
pub const HEIGHT: f32 = 84.0;

/// The margin at both ends of the band.
pub const PAD: f32 = 32.0;

/// Draw the band. `heading` is what the screen is about.
pub fn draw(frame: &mut canvas::Frame<Renderer>, width: f32, heading: &str) {
    // The band paints its own ground, so nothing under its layer shows
    // through it.
    let ground = area(0.0, 0.0, width, HEIGHT);
    frame.fill_rectangle(ground.position(), extent(ground), look::BACKGROUND);
    frame.fill_text(label(
        heading,
        Point::new(PAD, HEIGHT / 2.0),
        look::NAME,
        look::text(),
        Alignment::Left,
        Vertical::Center,
        width / 2.0,
    ));

    let rule = area(0.0, HEIGHT - 2.0, width, 2.0);
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}

/// The band as a layer over a screen: what it says.
pub struct Layer<'a> {
    pub heading: &'a str,
}

impl canvas::Program<Infallible, Theme, Renderer> for Layer<'_> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        draw(&mut frame, bounds.width, self.heading);
        vec![frame.into_geometry()]
    }
}

/// The band's layer as an element a screen stacks over its own.
pub fn layer(heading: &str) -> Element<'_, Infallible, Theme, Renderer> {
    canvas(Layer { heading })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
}
