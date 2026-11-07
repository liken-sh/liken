// The bar that says how far a play of one slot's work reached: a faint
// track the width of the art, with the watched share of it bright. The bar
// lies along the foot of the art and not inside it, because every mesh of
// a layer draws under every image of that layer, so a fill inside the art
// would never show. While more than one person watches, a film's poster
// also draws a face for each of them who has a play of it, in a row along
// the inside of its foot. The faces are images, so they draw over the
// poster.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::Rectangle;

use super::{Card, area, extent, text};
use crate::audience::Viewer;
use crate::audience::faces::{Face, Faces};
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

/// Draw the bar along the foot of this box at this share. A page that holds
/// the share itself, and not a card that answers it, names the share here.
pub fn fill(frame: &mut canvas::Frame<Renderer>, slot: Rectangle, fraction: f32) {
    let (track, watched) = bar(slot, fraction);
    frame.fill_rectangle(track.position(), extent(track), look::faint());
    if watched.width > 0.0 {
        frame.fill_rectangle(watched.position(), extent(watched), look::text());
    }
}

/// The side of one face along the foot of a poster, in logical pixels:
/// small enough that a row of several leaves most of the poster clear.
pub const FACE: f32 = 28.0;

// The space between two faces, and between the row and the poster's edges.
const FACE_GAP: f32 = 4.0;
const FACE_INSET: f32 = 6.0;

/// The box each of this many faces draws in over a poster this size: a row
/// from the poster's left edge along the inside of its foot. A face that
/// would run past the poster's right edge draws nowhere.
pub fn face_boxes(slot: Rectangle, count: usize) -> Vec<Rectangle> {
    let y = slot.y + slot.height - FACE_INSET - FACE;
    (0..count)
        .map(|index| {
            let x = slot.x + FACE_INSET + index as f32 * (FACE + FACE_GAP);
            area(x, y, FACE, FACE)
        })
        .take_while(|face| face.x + face.width <= slot.x + slot.width - FACE_INSET)
        .collect()
}

/// Draw the faces of one slot along the foot of its art, and nothing for a
/// slot that names none.
pub fn faces<T: Card>(
    frame: &mut canvas::Frame<Renderer>,
    card: &T,
    slot: Rectangle,
    faces: &Faces,
) {
    people(frame, card.faces(), slot, faces);
}

/// Draw these people's faces along the foot of this box, for a page that
/// holds the people itself, and not a card that answers them.
pub fn people(
    frame: &mut canvas::Frame<Renderer>,
    viewers: &[Viewer],
    slot: Rectangle,
    faces: &Faces,
) {
    for (viewer, at) in viewers.iter().zip(face_boxes(slot, viewers.len())) {
        face(frame, at, viewer, faces);
    }
}

// One face over art: the person's picture, or their letter on a plain disc.
fn face(frame: &mut canvas::Frame<Renderer>, at: Rectangle, viewer: &Viewer, faces: &Faces) {
    let letter = match faces.face(viewer, FACE) {
        Face::Picture(image) => {
            for (band, handle) in image.bands(at) {
                frame.draw_image(band, canvas::Image::new(handle));
            }
            return;
        }
        Face::Letter(letter) => letter,
    };
    if let Some(disc) = faces.disc(FACE) {
        for (band, handle) in disc.bands(at) {
            frame.draw_image(band, canvas::Image::new(handle));
        }
    }
    let line = text::height(1, look::FACE);
    text::shown(
        frame,
        &letter,
        area(at.x, at.center_y() - line / 2.0, at.width, line),
        look::FACE,
        look::text(),
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_faces_run_along_the_inside_of_the_foot_from_the_left() {
        let boxes = face_boxes(slot(), 2);

        assert_eq!(boxes[0].x, 106.0);
        assert_eq!(boxes[0].y + boxes[0].height, 644.0);
        assert_eq!(boxes[1].x, 106.0 + FACE + 4.0);
    }

    #[test]
    fn a_face_that_would_leave_the_poster_draws_nowhere() {
        assert_eq!(face_boxes(area(0.0, 0.0, 80.0, 120.0), 5).len(), 2);
    }

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
