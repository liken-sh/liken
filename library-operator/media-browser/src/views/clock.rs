// The clock at the top right of every screen: the reading the browser
// draws over whatever screen is on the stack, on the band's own middle
// line and inside its margin. It is a layer of its own for the reason
// the volume row is one: inside one layer the renderer draws every mesh,
// then every image, then every text, so a layer under a page's images
// would lose its ground.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle, Theme, mouse};

use super::{area, band, extent, label, text};
use crate::clock::Time;
use crate::look;

// The widest reading of the day. The clock reserves the room this one
// takes, so the controls beside it hold their place as the minute turns.
const WIDEST: &str = "12:00 pm";

// What the estimate in `text::width` is multiplied by. That estimate is
// the font's average advance, the digits of a reading are wider than the
// average, and a reading wider than the room it was given wraps to a
// second line.
const SLACK: f32 = 1.5;

// The corner's scrim: a square on the corner, shaded along its diagonal
// from the corner in, held across the reading, and clear at half the
// diagonal. Every edge of a square lies at or past the half of its
// diagonal from the corner, so the fade ends inside the square and no
// edge of it shows on the art. The clock draws over a page's backdrop as
// well as over the band, so the shade keeps the reading legible over art
// of any brightness. On the band it lies on the band's own black and
// shows nothing.
const SIDE: f32 = 240.0;
const HOLDS_TO: f32 = 0.32;
const CLEARS_AT: f32 = 0.5;

// The shade at the corner itself. The surface blends in linear space, so
// this reads as a mid grey over white art and not as black, and the
// reading draws in the bright ink so it stays legible on that grey.
const SHADE: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.7);

/// The room the clock takes at the right edge of a frame.
pub fn room() -> f32 {
    text::width(WIDEST, look::CONTROL) * SLACK
}

/// The left edge of the clock in a frame this wide. The band's controls
/// end a margin to the left of it.
pub fn left(width: f32) -> f32 {
    width - band::PAD - room()
}

/// The clock as one frame draws it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Face {
    /// The reading the frame draws.
    pub time: Time,
}

impl canvas::Program<Infallible, Theme, Renderer> for Face {
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
        let reading = self.time.twelve_hour();
        let right = bounds.x + bounds.width - band::PAD;
        let middle = bounds.y + band::HEIGHT / 2.0;

        let corner = area(bounds.x + bounds.width - SIDE, bounds.y, SIDE, SIDE);
        frame.fill_rectangle(
            corner.position(),
            extent(corner),
            canvas::gradient::Linear::new(
                Point::new(corner.x + corner.width, corner.y),
                Point::new(corner.x, corner.y + corner.height),
            )
            .add_stop(0.0, SHADE)
            .add_stop(HOLDS_TO, SHADE)
            .add_stop(CLEARS_AT, look::CLEAR),
        );
        frame.fill_text(label(
            &reading,
            Point::new(right, middle),
            look::CONTROL,
            look::text(),
            Alignment::Right,
            Vertical::Center,
            room(),
        ));
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_clock_hangs_off_the_right_edge() {
        assert_eq!(left(1920.0) + room() + band::PAD, 1920.0);
        assert_eq!(left(1280.0) + room() + band::PAD, 1280.0);
    }

    #[test]
    fn the_room_holds_every_reading_of_the_day() {
        for hour in 0..24 {
            for minute in [0, 59] {
                let reading = Time { hour, minute }.twelve_hour();
                assert!(text::width(&reading, look::CONTROL) <= room(), "{reading}");
            }
        }
    }
}
