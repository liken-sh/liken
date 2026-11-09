// The backing under the strip: a shade at the top of the frame, dark
// from the top edge through the strip's line and clear a little lower.
// The strip draws its glass and its clock as thin light strokes with no
// plate of their own, so over a bright backdrop the glass disappears. The
// player's overlay draws the same kind of shade behind its header and
// clock.
//
// A title's page draws its backdrop under the strip and asks for the
// backing through `level`. The backdrop stands still while the page
// scrolls, and a scrolled page shows its rows only under the strip, so
// the level is the same at rest and scrolled. The home page and a wall
// draw a black band under the strip and ask for none. The backing is a layer of its own between the screen and
// the strip, because inside one layer a ramp is an image, and the
// renderer draws every image after every mesh, so a ramp in the strip's
// layer would cover the glass.

use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Rectangle, Theme, mouse};

use crate::look;
use crate::views::area;
use crate::views::ramp::{self, Axis, Stop};

/// How dark the backing is at its peak, as the share of the ground it
/// covers.
pub const PEAK: f32 = 0.6;

/// How far down from the top edge the backing holds its peak, in logical
/// pixels: the foot of the clock's line box, so every part of the strip
/// draws over the full shade.
pub const HOLD: f32 = 124.0;

/// How far down from the top edge the backing is clear, in logical
/// pixels.
pub const CLEAR: f32 = 200.0;

/// The backing's level on a page that draws a backdrop under the strip,
/// as a share of the peak. It is under the peak, because the page draws
/// its title under the backing too.
pub const OVER_BACKDROP: f32 = 0.5;

// The points of the smoothstep from the hold to the clear end. The ramp
// joins them with straight runs.
const EASE_POINTS: usize = 8;

/// The backing's level, from 0 for none to 1 for the peak, on a page that
/// draws a backdrop under the strip or not.
pub fn level(over_backdrop: bool) -> f32 {
    match over_backdrop {
        true => OVER_BACKDROP,
        false => 0.0,
    }
}

/// The stops of the backing at this level, as shares of `CLEAR` from the
/// top edge: the peak through `HOLD`, then one smoothstep to clear.
pub fn stops(level: f32) -> Vec<Stop> {
    let peak = PEAK * level.clamp(0.0, 1.0);
    let shade = |share: f32| Color {
        a: peak * share,
        ..look::BACKGROUND
    };
    let hold = HOLD / CLEAR;
    let mut stops = vec![(0.0, shade(1.0)), (hold, shade(1.0))];
    for point in 1..=EASE_POINTS {
        let t = point as f32 / EASE_POINTS as f32;
        stops.push((hold + (1.0 - hold) * t, shade(1.0 - look::eased(t))));
    }
    stops
}

/// The backing at one level, as the layer the browser draws under the
/// strip.
pub struct Backing {
    pub level: f32,
}

impl canvas::Program<Infallible, Theme, Renderer> for Backing {
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
        ramp::fill(
            &mut frame,
            area(0.0, 0.0, bounds.width, CLEAR),
            Axis::Down,
            &stops(self.level),
        );
        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::clock;
    use crate::views::screen;

    #[test]
    fn a_page_draws_the_backing_under_its_peak_over_a_backdrop_and_none_without_one() {
        assert_eq!(level(true), OVER_BACKDROP);
        const { assert!(OVER_BACKDROP < 1.0) };
        assert_eq!(level(false), 0.0);
    }

    #[test]
    fn the_peak_holds_through_the_foot_of_the_clock_and_clears_by_the_end() {
        assert_eq!(HOLD, screen::MARGIN_Y + clock::BOX);
        let stops = stops(1.0);
        let at = |share: f32| {
            stops
                .iter()
                .find(|stop| stop.0 >= share)
                .map(|stop| stop.1.a)
                .unwrap()
        };
        assert_eq!(at(0.0), PEAK);
        assert_eq!(at(HOLD / CLEAR), PEAK);
        assert_eq!(
            stops.last().map(|stop| (stop.0, stop.1.a)),
            Some((1.0, 0.0))
        );
        assert!(stops.windows(2).all(|pair| pair[0].1.a >= pair[1].1.a));
    }

    #[test]
    fn a_lower_level_draws_the_same_shape_lighter() {
        let half = stops(0.5);
        for (full, half) in stops(1.0).iter().zip(&half) {
            assert_eq!(full.0, half.0);
            assert!((full.1.a / 2.0 - half.1.a).abs() < 1e-6);
        }
    }
}
