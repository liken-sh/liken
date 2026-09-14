// The band across the top of a wall draws the heading, and under it the
// one line a screen says about its rows, such as the calendar a
// franchise's times count on. It is a layer of its own over the screen, because a row that scrolls up under
// it must not show through, and inside one layer the renderer draws
// every fill, then every image, then every text, whatever the order they
// were drawn in.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Element, Length, Point, Rectangle, Theme, mouse};

use super::{area, clock, extent, label, screen, text};
use crate::look;

/// The height the band takes off the top of the frame. A screen's rows
/// start at its bottom edge. The strip draws the clock over the band. A
/// row that reached the clock's line would scroll under the reading. So
/// the band holds that line and the air under it.
pub const HEIGHT: f32 = clock::MARGIN_Y + clock::BOX + AIR;

// The air between the clock's line and the first row of a screen.
const AIR: f32 = 30.0;

/// How far the heading rises over the band's line to make room for a
/// note under it, and how far under that line the note sits.
const RISE: f32 = 11.0;
const DROP: f32 = 13.0;

/// Draw the band. `heading` is what the screen is about, and `note` is
/// the one line under it, or nothing.
pub fn draw(frame: &mut canvas::Frame<Renderer>, width: f32, heading: &str, note: &str) {
    // The band paints its own ground, so nothing under its layer shows
    // through it.
    let ground = area(0.0, 0.0, width, HEIGHT);
    frame.fill_rectangle(ground.position(), extent(ground), look::BACKGROUND);
    // The heading takes the clock's own line, so the two ends of the band
    // read as one row.
    let middle = match note.is_empty() {
        true => clock::middle(),
        false => clock::middle() - RISE,
    };
    // The heading starts on the screen's side margin, the same margin the
    // clock's reading ends on at the other edge.
    frame.fill_text(label(
        heading,
        Point::new(screen::MARGIN_X, middle),
        look::NAME,
        look::text(),
        Alignment::Left,
        Vertical::Center,
        width / 2.0,
    ));
    if !note.is_empty() {
        frame.fill_text(label(
            &text::cut(note, look::CAPTION, width / 2.0),
            Point::new(screen::MARGIN_X, clock::middle() + DROP),
            look::CAPTION,
            look::faint(),
            Alignment::Left,
            Vertical::Center,
            f32::INFINITY,
        ));
    }

    let rule = area(0.0, HEIGHT - 2.0, width, 2.0);
    frame.fill_rectangle(rule.position(), extent(rule), look::slot());
}

/// The band as a layer over a screen: what it says.
pub struct Layer<'a> {
    pub heading: &'a str,
    pub note: &'a str,
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
        draw(&mut frame, bounds.width, self.heading, self.note);
        vec![frame.into_geometry()]
    }
}

/// The band's layer as an element a screen stacks over its own.
pub fn layer<'a>(heading: &'a str, note: &'a str) -> Element<'a, Infallible, Theme, Renderer> {
    canvas(Layer { heading, note })
        .width(Length::Fill)
        .height(Length::Fill)
        .into()
}

#[cfg(test)]
mod tests {
    use super::*;

    // A row that reached the clock's line would scroll under the
    // reading, so the band holds the whole line box and the air under it.
    #[test]
    fn the_band_clears_the_clock_s_line() {
        const { assert!(HEIGHT > clock::MARGIN_Y + clock::BOX) };
    }
}
