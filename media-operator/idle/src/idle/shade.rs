//! The shade: how far the whole screen is darkened.
//!
//! The idle command pod owns the quiet timer and sends the two moments the cover
//! reads. This module only eases between clear and opaque. Going dark is slow
//! and coming back is fast, because a screen that fades on its own has no one
//! watching it, and a person who pressed a button is waiting.
//!
//! The shade draws no shape of its own. It is one factor, and every element
//! scales its own colours by it. `look::under` states why, and
//! `display/theme.lua` holds the same mechanism in `theme.fade`.

use super::energy::ease;
use crate::unit::Unit;

/// Four seconds down, and under half a second back up.
const SLEEP: f64 = 4.0;
const WAKE: f64 = 0.4;

/// The cover at one moment, from 0 clear to 1 opaque.
///
/// A screen that has read no moment has no cover at all, which is the state an
/// idle pod starts in.
pub fn cover(unit: &Unit, at: f64) -> f64 {
    let Some(shade) = unit.shade else {
        return 0.0;
    };

    let (to, seconds) = match shade.down {
        true => (1.0, SLEEP),
        false => (0.0, WAKE),
    };

    shade.from + (to - shade.from) * ease((at - shade.since) / seconds)
}

/// The fraction of full brightness every element draws at, from 1 on a clear
/// screen to 0 on a dark one. It is the complement of the cover, and it is the
/// factor `display/theme.lua` calls `theme.fade`.
pub fn fade(unit: &Unit, at: f64) -> f32 {
    1.0 - cover(unit, at) as f32
}

/// The second the shade next changes, for [`super::Idle::next_frame`].
///
/// An ease changes the colour of every element on every frame it covers, so it
/// answers with `at` and the harness draws now. A cover that has reached its
/// end holds one factor, and a screen that has read no moment carries no cover
/// at all, so both state nothing.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    let shade = unit.shade?;
    let seconds = match shade.down {
        true => SLEEP,
        false => WAKE,
    };

    (at < shade.since + seconds).then_some(at)
}

#[cfg(test)]
mod tests {
    use super::*;
    use media_screen::Moment;

    /// A unit that read one moment on the screen topic.
    fn unit(moment: Moment, at: f64) -> Unit {
        let mut unit = Unit::default();
        unit.fold(moment, at);
        unit
    }

    #[test]
    fn a_screen_that_has_read_no_moment_carries_no_cover() {
        assert_eq!(cover(&Unit::default(), 0.0), 0.0);
        assert_eq!(cover(&Unit::default(), 600.0), 0.0);
    }

    #[test]
    fn the_screen_goes_dark_over_four_seconds() {
        let unit = unit(Moment::Sleep, 10.0);
        assert_eq!(cover(&unit, 10.0), 0.0);
        assert_eq!(cover(&unit, 12.0), 0.5);
        assert_eq!(cover(&unit, 14.0), 1.0);
        assert_eq!(cover(&unit, 30.0), 1.0);
    }

    #[test]
    fn a_press_brings_the_screen_back_in_under_half_a_second() {
        let mut unit = unit(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 20.0);

        let halfway = cover(&unit, 20.2);
        assert_eq!(cover(&unit, 20.0), 1.0);
        assert!((halfway - 0.5).abs() < 1e-9, "{halfway}");
        assert_eq!(cover(&unit, 20.4), 0.0);
    }

    #[test]
    fn the_factor_every_element_draws_at_is_the_complement_of_the_cover() {
        let unit = unit(Moment::Sleep, 10.0);

        assert_eq!(fade(&Unit::default(), 0.0), 1.0);
        assert_eq!(fade(&unit, 10.0), 1.0);
        assert_eq!(fade(&unit, 12.0), 0.5);
        assert_eq!(fade(&unit, 14.0), 0.0);
    }

    #[test]
    fn the_fade_to_black_asks_for_a_frame_for_the_whole_four_seconds() {
        let unit = unit(Moment::Sleep, 10.0);
        assert_eq!(next_frame(&unit, 10.0), Some(10.0));
        assert_eq!(next_frame(&unit, 13.9), Some(13.9));
        assert_eq!(next_frame(&unit, 14.0), None);
    }

    #[test]
    fn the_return_asks_for_a_frame_for_400_ms() {
        let mut unit = unit(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 20.0);

        assert_eq!(next_frame(&unit, 20.39), Some(20.39));
        assert_eq!(next_frame(&unit, 20.41), None);
    }

    #[test]
    fn a_screen_that_has_read_no_moment_asks_for_no_frame() {
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
    }

    #[test]
    fn a_press_during_the_fade_never_darkens_the_screen_first() {
        let mut unit = unit(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 12.0);

        assert_eq!(cover(&unit, 12.0), 0.5);
        assert!(cover(&unit, 12.1) < 0.5);
        assert_eq!(cover(&unit, 12.4), 0.0);
    }
}
