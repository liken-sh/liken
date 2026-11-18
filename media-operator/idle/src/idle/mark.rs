//! The `liken` mark, a mosaic of hexagons, at the center of the screen.
//!
//! `liken-iced` reads the hexagons and their colors from
//! `brand/liken.svg` and draws them. This module places the mark at the
//! center of the layout, sets its size, and passes it the energy level
//! and phase.

use super::energy;
use super::{Frame, Layout};
use crate::unit::Unit;

/// Draws the mark into the frame, in canvas units. `light` scales its
/// colors by the same factor that every element uses, so the whole
/// screen dims together when it sleeps.
///
/// The mark is filled paths with no stroke, so it scales with the
/// frame's transform. `liken_iced::mark::outline` explains why.
pub fn draw(frame: &mut Frame, layout: &Layout, unit: &Unit, at: f64, light: f32) {
    liken_iced::mark::draw(
        frame,
        layout.center(),
        layout.mark_span(),
        energy::level(unit, at),
        energy::phase(unit, at),
        light,
    );
}

/// The second at which the mark next changes, for
/// [`super::Idle::next_frame`].
///
/// While the energy is above 0 the mark moves, also after a ramp has
/// finished, so the next frame is due at `at`. At energy 0 every frame
/// is identical, so this returns `None`.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    (energy::level(unit, at) > 0.0).then_some(at)
}

#[cfg(test)]
mod tests {
    use super::*;
    use iced_winit::core::Size;
    use media_screen::Moment;
    use media_screen::status::{Activity, Status};

    fn layout() -> Layout {
        Layout::for_surface(Size::new(1920.0, 1080.0))
    }

    #[test]
    fn the_mosaic_is_fourteen_hexagons() {
        assert_eq!(liken_iced::mark::hexagons().len(), 14);
    }

    #[test]
    fn a_starting_play_moves_the_mosaic_and_rest_leaves_it_still() {
        let mut unit = Unit::default();
        let layout = layout();
        let hexagon = &liken_iced::mark::hexagons()[0];
        let placed = |unit: &Unit, at: f64| {
            hexagon.place(
                layout.center(),
                layout.mark_span(),
                energy::level(unit, at),
                energy::phase(unit, at),
            )
        };

        let still = placed(&unit, 0.0);
        assert_eq!(placed(&unit, 9.0).points, still.points);

        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.0,
        );
        assert_ne!(placed(&unit, 2.0).points, still.points);
    }

    #[test]
    fn a_moving_mark_needs_a_frame_now() {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.0,
        );

        // By 1.2 seconds the ramp has finished, and the mark keeps
        // moving at full level.
        assert_eq!(next_frame(&unit, 0.6), Some(0.6));
        assert_eq!(next_frame(&unit, 30.0), Some(30.0));
    }

    #[test]
    fn a_mark_at_rest_needs_no_frame() {
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }
}
