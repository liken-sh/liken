//! The shade: how far the whole screen is dimmed toward black.
//!
//! `media-screen` decides when to send the sleep and wake moments, and this
//! module eases the screen between clear and dark. The fade to black is slow
//! because nobody is watching a screen that dims on its own. The fade back is
//! fast because a person who pressed a button is waiting.

use super::energy::ease;
use crate::unit::Unit;

/// Seconds the fade to black takes, and the fade back.
const SLEEP: f64 = 4.0;
const WAKE: f64 = 0.4;

/// The cover at second `at`, from 0 (clear) to 1 (black). It is 0 until the
/// unit has folded a sleep or wake moment.
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

/// The brightness factor that every element draws at, 1 minus the cover.
pub fn fade(unit: &Unit, at: f64) -> f32 {
    1.0 - cover(unit, at) as f32
}

/// For [`super::Idle::next_frame`]: `at` while a fade runs, because every
/// frame differs, and `None` when the cover does not change.
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

    fn unit(moment: Moment, at: f64) -> Unit {
        let mut unit = Unit::default();
        unit.fold(moment, at);
        unit
    }

    #[test]
    fn a_unit_with_no_sleep_or_wake_moment_has_no_cover() {
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
        assert!(
            (halfway - 0.5).abs() < 1e-9,
            "the cover halfway through the fade is {halfway}, want 0.5"
        );
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
    fn next_frame_returns_now_for_the_whole_four_second_fade_to_black() {
        let unit = unit(Moment::Sleep, 10.0);
        assert_eq!(next_frame(&unit, 10.0), Some(10.0));
        assert_eq!(next_frame(&unit, 13.9), Some(13.9));
        assert_eq!(next_frame(&unit, 14.0), None);
    }

    #[test]
    fn next_frame_returns_now_for_the_400_ms_fade_back() {
        let mut unit = unit(Moment::Sleep, 10.0);
        unit.fold(Moment::Wake, 20.0);

        assert_eq!(next_frame(&unit, 20.39), Some(20.39));
        assert_eq!(next_frame(&unit, 20.41), None);
    }

    #[test]
    fn next_frame_is_none_for_a_unit_with_no_sleep_or_wake_moment() {
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
