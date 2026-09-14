// The clock at the top right of every screen: the reading, the room it
// reserves, and the halo of dark ink it draws over, the way a subtitle
// does, so it reads over art of any brightness and nothing shows around
// it.
//
// The clock is drawn by the strip, the browser's layer over every
// screen, through `reading` here. This module keeps the glyph, the
// halo, the room the reading reserves, and the box the reading draws
// in.
//
// Three programs draw a clock on one panel. They are this browser, the
// player's `mpv` display, and the idle screen between films. One
// replaces another while a person watches. The reading holds one box
// across all three, so the hour stays where it is when the screen
// changes. The media operator states the same numbers in
// `display/theme.lua` and in `idle/src/look.rs`.
//
// The vertical measures of that box are below. The side margin the
// reading ends on is the margin every screen keeps, and `views::screen`
// states it.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle};

use super::{label, screen, text};
use crate::clock::Time;
use crate::look;

/// The top margin the reading's line box hangs from, in logical pixels.
/// The player's canvas is 1080 rows tall, and so is this frame at the
/// scale the compositor states. The two spaces therefore share every
/// vertical measure.
pub const MARGIN_Y: f32 = 90.0;

/// The line box the reading draws in, in logical pixels. `libass`
/// scales the face until its bounding height fills the size an ASS
/// `\fs` states. So the player's `\fs34` is 34 rows of canvas, and it
/// is not a 34-pixel glyph.
pub const BOX: f32 = 34.0;

/// The type size that fills that box. The brand face measures 1326
/// units of bounding height over an em of 1000. Those numbers come from
/// the `OS/2` and `head` tables of `SourceSans3-Regular.otf`. So the
/// size is the box over that metric, rounded to a whole pixel. The idle
/// screen rounds it the same way.
pub const SIZE: f32 = 26.0;

// The strip module: the browser's layer over every screen, which draws
// the search icon, the field it expands into, and the reading.
pub mod strip;

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

/// Where the eight dark copies of a reading draw, around the point the
/// bright reading draws at. The pill over a still draws the same way,
/// because any plate under the words would draw under the art.
pub fn halo(at: Point) -> [Point; 8] {
    AROUND.map(|(x, y)| Point::new(at.x + x * HALO, at.y + y * HALO))
}

/// The room the clock takes at the right edge of a frame.
pub fn room() -> f32 {
    text::width(WIDEST, SIZE) * SLACK
}

/// The right edge of the clock in a frame this wide. The reading is set
/// flush to it, so the hour ends on the side margin whatever it reads.
pub fn right(width: f32) -> f32 {
    width - screen::MARGIN_X
}

/// The left edge of the clock in a frame this wide. The strip's icon,
/// and the field it expands into, end a margin to the left of it.
pub fn left(width: f32) -> f32 {
    right(width) - room()
}

/// The middle of the reading's line. The player hangs its own line box
/// from the top margin by the box's top edge. This toolkit centres a
/// line on the point it is given. So the same line is half a box lower
/// here. The strip's other parts take this line as well.
pub fn middle() -> f32 {
    MARGIN_Y + BOX / 2.0
}

// The reading in the room the clock reserves at the right of the frame.
// The dark copies draw first and the bright reading over them, so the
// reading reads over art of any brightness and no shape shows around it.
pub(crate) fn reading(frame: &mut canvas::Frame<Renderer>, bounds: Rectangle, time: Time) {
    let reading = time.twelve_hour();
    let at = Point::new(bounds.x + right(bounds.width), bounds.y + middle());
    let ink = |point: Point, color: Color| {
        label(
            &reading,
            point,
            SIZE,
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
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_clock_hangs_off_the_right_edge() {
        assert_eq!(left(1920.0) + room(), right(1920.0));
        assert_eq!(left(1280.0) + room(), right(1280.0));
    }

    #[test]
    fn the_clock_ends_on_the_margin_the_player_keeps() {
        assert_eq!(right(1920.0), 1824.0);
        assert_eq!(right(2560.0), 2464.0);
    }

    #[test]
    fn the_clock_sits_on_the_line_the_player_hangs_its_reading_from() {
        assert_eq!(middle(), 107.0);
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
                assert!(text::width(&reading, SIZE) <= room(), "{reading}");
            }
        }
    }
}
