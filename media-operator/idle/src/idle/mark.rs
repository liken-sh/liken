//! The mark: the fourteen-hexagon mosaic at the middle of the screen.
//!
//! The geometry, the colours, and the pulse are the `brand` repository's, and
//! `liken-iced` reads them out of `liken.svg`. This module places the mark on
//! the canvas and hands it the energy. Where the mark draws and how large it
//! draws are this display's layout and not the brand's.

use super::energy;
use super::{Frame, Layout};
use crate::unit::Unit;

/// Draw the element into the frame, in canvas units.
///
/// The mark draws at the shade's own factor and at no opacity of its own, so
/// the whole screen darkens as one when the quiet window runs out.
///
/// The mark is fourteen filled paths whose corners are geometry, so it needs
/// nothing from the canvas scale. `liken_iced::mark::outline` says why.
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

/// The second the mark next changes, for [`super::Idle::next_frame`].
///
/// The animation clock runs at the speed floor or above it while the energy is
/// above 0, and a ramp that lands above 0 goes on turning the mark after it,
/// so the mark answers with `at` and the harness draws now. At energy 0 the
/// swing is 0 and the animation clock stands still, so every frame draws the
/// fourteen hexagons in the same places and the mark states nothing.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    (energy::level(unit, at) > 0.0).then_some(at)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::bus::Message;
    use crate::bus::status::{Activity, Status};
    use iced_winit::core::Size;

    fn layout() -> Layout {
        Layout::for_surface(Size::new(1920.0, 1080.0))
    }

    #[test]
    fn the_mosaic_is_fourteen_hexagons() {
        assert_eq!(liken_iced::mark::hexagons().len(), 14);
    }

    #[test]
    fn a_starting_play_swings_the_mosaic_and_rest_holds_it_still() {
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
            Message::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.0,
        );
        assert_ne!(placed(&unit, 2.0).points, still.points);
    }

    #[test]
    fn a_swinging_mark_asks_for_a_frame_now() {
        let mut unit = Unit::default();
        unit.fold(
            Message::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.0,
        );

        // The ramp has landed at full swing by 1.2 seconds, and the mark goes
        // on turning at that level with no ramp under it.
        assert_eq!(next_frame(&unit, 0.6), Some(0.6));
        assert_eq!(next_frame(&unit, 30.0), Some(30.0));
    }

    #[test]
    fn a_mark_at_rest_asks_for_no_frame() {
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }
}
