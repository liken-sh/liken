// The clock at the top right of every screen: the reading the browser
// draws over whatever screen is on the stack, on the band's own middle
// line and inside its margin. It is the browser's own layer, so no screen
// knows about it and every screen carries it in the same place. The
// reading draws over a halo of dark ink, the way a subtitle does, so it
// reads over art of any brightness and nothing shows around it.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle, Theme, mouse};

use super::{band, label, text};
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

// How far the dark copies of the reading draw from the bright one, in
// logical pixels: far enough to edge every glyph over white art, near
// enough that the copies stay behind the reading.
const HALO: f32 = 2.0;

// The eight directions the halo draws in, as unit vectors, so every
// copy lies the same distance from the reading and the ring around a
// glyph has no gap.
const AROUND: [(f32, f32); 8] = [
    (1.0, 0.0),
    (-1.0, 0.0),
    (0.0, 1.0),
    (0.0, -1.0),
    (DIAGONAL, DIAGONAL),
    (DIAGONAL, -DIAGONAL),
    (-DIAGONAL, DIAGONAL),
    (-DIAGONAL, -DIAGONAL),
];

// The length of each leg of a diagonal unit vector.
const DIAGONAL: f32 = std::f32::consts::FRAC_1_SQRT_2;

// Where the eight dark copies of the reading draw, around the point the
// bright reading draws at.
fn halo(at: Point) -> [Point; 8] {
    AROUND.map(|(x, y)| Point::new(at.x + x * HALO, at.y + y * HALO))
}

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

        // The dark copies draw first and the bright reading over them, so
        // the reading reads over art of any brightness and no shape shows
        // around it.
        let at = Point::new(right, middle);
        let ink = |point: Point, color: Color| {
            label(
                &reading,
                point,
                look::CONTROL,
                color,
                Alignment::Right,
                Vertical::Center,
                room(),
            )
        };
        for point in halo(at) {
            frame.fill_text(ink(point, look::BACKGROUND));
        }
        frame.fill_text(ink(at, look::text()));
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
    fn every_copy_of_the_halo_lies_the_same_distance_from_the_reading() {
        let at = Point::new(100.0, 50.0);
        let distances =
            halo(at).map(|point| ((point.x - at.x).hypot(point.y - at.y) * 100.0).round());
        assert_eq!(distances, [(HALO * 100.0).round(); 8]);
    }

    #[test]
    fn the_halo_draws_in_eight_directions() {
        let mut directions = halo(Point::new(0.0, 0.0))
            .map(|point| {
                (
                    (point.x * 100.0).round() as i32,
                    (point.y * 100.0).round() as i32,
                )
            })
            .to_vec();
        directions.sort_unstable();
        directions.dedup();
        assert_eq!(directions.len(), 8);
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
