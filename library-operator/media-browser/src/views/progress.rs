// The bar that says how far a play of one slot's work reached: a faint
// track the width of the art, with the watched share of it bright. The bar
// lies along the foot of the art and not inside it, because every mesh of
// a layer draws under every image of that layer, so a fill inside the art
// would never show.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::{Card, area, extent};
use crate::look;

/// How tall the bar is, in logical pixels: thin enough to read as a mark on
/// the art and not as a row of its own.
pub const HEIGHT: f32 = 4.0;

/// The two rectangles the bar draws, the track and the watched share of it,
/// for a slot of art this size. A share past the whole work draws the whole
/// track, because a play that ran past the catalog's duration is still one
/// work watched.
pub fn bar(slot: Rectangle, fraction: f32) -> (Rectangle, Rectangle) {
    let track = area(slot.x, slot.y + slot.height, slot.width, HEIGHT);
    let watched = area(
        track.x,
        track.y,
        track.width * fraction.clamp(0.0, 1.0),
        track.height,
    );
    (track, watched)
}

/// Draw the bar under one slot's art, and nothing for a slot no play names.
pub fn draw<T: Card>(frame: &mut canvas::Frame<Renderer>, card: &T, slot: Rectangle) {
    let Some(fraction) = card.watched() else {
        return;
    };
    fill(frame, slot, fraction);
}

/// Draw the bar along the foot of this box at this share. A page that draws
/// the bar under something other than art, such as a button row, names the
/// box itself.
pub fn fill(frame: &mut canvas::Frame<Renderer>, slot: Rectangle, fraction: f32) {
    let (track, watched) = bar(slot, fraction);
    frame.fill_rectangle(track.position(), extent(track), look::faint());
    if watched.width > 0.0 {
        frame.fill_rectangle(watched.position(), extent(watched), look::text());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn slot() -> Rectangle {
        area(100.0, 200.0, 300.0, 450.0)
    }

    #[test]
    fn the_track_runs_the_width_of_the_art_along_its_foot() {
        let (track, _) = bar(slot(), 0.5);
        assert_eq!(track.x, 100.0);
        assert_eq!(track.width, 300.0);
        assert_eq!(track.y, 650.0);
        assert_eq!(track.height, HEIGHT);
    }

    #[test]
    fn the_watched_share_is_that_share_of_the_track() {
        let (track, watched) = bar(slot(), 0.25);
        assert_eq!(watched.x, track.x);
        assert_eq!(watched.y, track.y);
        assert_eq!(watched.height, track.height);
        assert_eq!(watched.width, 75.0);
    }

    #[test]
    fn a_play_that_reached_nothing_draws_no_watched_share() {
        let (_, watched) = bar(slot(), 0.0);
        assert_eq!(watched.width, 0.0);
    }

    #[test]
    fn a_play_past_the_whole_work_draws_the_whole_track() {
        let (track, watched) = bar(slot(), 1.4);
        assert_eq!(watched.width, track.width);
        assert_eq!(bar(slot(), -0.2).1.width, 0.0);
    }
}
