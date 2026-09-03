// The state the volume row is in, as a pure function of the clock: the
// level the bus last delivered, the second of the last press, and how far
// up the row stood when that press landed.

use media_screen::volume::Volume;

use crate::views::volume::Row;

// A fade takes this long to reach full, and this long to reach clear, so
// the row leaves more slowly than it arrives.
const FADE_IN: f64 = 0.35;
const FADE_OUT: f64 = 0.6;

// The row leaves this many seconds after the last press. Each press
// restarts the wait, so a run of presses holds the row on screen.
const HOLD: f64 = 4.0;

/// The listening level as the browser draws it.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Level {
    // The level and the muted flag the volume topic last carried.
    volume: Volume,
    // The second of the last press, or nothing while no press has arrived.
    pressed: Option<f64>,
    // How far up the row stood when that press landed, so a press on a
    // leaving row lifts it from where it stands.
    from: f32,
}

impl Level {
    /// Fold one level in at this second. The operator marks a press, and
    /// the broker's retained catch-up carries none, so a browser that
    /// connects to a running unit draws no row.
    pub fn fold(&mut self, volume: Volume, pressed: bool, at: f64) {
        if pressed {
            self.from = self.fade(at);
            self.pressed = Some(at);
        }
        self.volume = volume;
    }

    /// The row's own fade, from 0 off screen to 1 full.
    pub fn fade(&self, at: f64) -> f32 {
        let Some(pressed) = self.pressed else {
            return 0.0;
        };
        let since = at - pressed;
        let arriving = f64::from(self.from) + since / FADE_IN;
        let leaving = 1.0 - (since - HOLD) / FADE_OUT;
        arriving.min(leaving).clamp(0.0, 1.0) as f32
    }

    // Whether the row has left. This is the one rule that says the row is
    // off screen: the fade at the last instant of a fade out is a float
    // away from zero, and a row is gone or it is not.
    fn gone(&self, at: f64) -> bool {
        self.pressed
            .is_none_or(|pressed| at >= pressed + HOLD + FADE_OUT)
    }

    /// The second the row next changes, for the browser's frame schedule.
    /// The two fades change the row on every frame they cover and answer
    /// now. Between them the row is up and steady, so the answer is the
    /// second it starts to leave, and the loop sleeps through the hold. A
    /// row that has left states nothing.
    pub fn next_frame(&self, at: f64) -> Option<f64> {
        if self.gone(at) {
            return None;
        }
        let pressed = self.pressed?;
        // A row lifted from part way up reaches full sooner, by exactly
        // the part of the fade it started above.
        let full = pressed + (1.0 - f64::from(self.from)) * FADE_IN;
        let leaving = pressed + HOLD;

        if at < full {
            Some(at)
        } else if at < leaving {
            Some(leaving)
        } else {
            Some(at)
        }
    }

    /// What one frame draws at this second, and nothing while the row is
    /// off screen.
    pub fn row(&self, at: f64) -> Option<Row> {
        if self.gone(at) {
            return None;
        }
        let fade = self.fade(at);
        (fade > 0.0).then_some(Row {
            volume: self.volume,
            fade,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The second the first press lands on, which every case measures
    // from.
    const PRESS: f64 = 1.0;

    // The level the fake unit stands at, and the state that read it and
    // then a press of it.
    fn level() -> Volume {
        Volume {
            level: 40,
            muted: false,
        }
    }

    fn pressed(at: f64) -> Level {
        let mut state = Level::default();
        state.fold(level(), false, 0.0);
        state.fold(level(), true, at);
        state
    }

    #[test]
    fn the_catch_up_shows_no_row() {
        let mut state = Level::default();
        state.fold(level(), false, 1.0);

        assert_eq!(state.fade(1.0), 0.0);
        assert_eq!(state.fade(2.0), 0.0);
        assert_eq!(state.row(2.0), None);
    }

    #[test]
    fn a_press_brings_the_row_in_over_350_ms() {
        let state = pressed(PRESS);

        assert_eq!(state.fade(PRESS), 0.0);
        assert!((state.fade(PRESS + 0.175) - 0.5).abs() < 1e-6);
        assert_eq!(state.fade(PRESS + 0.35), 1.0);
        assert_eq!(state.fade(PRESS + 3.0), 1.0);
    }

    #[test]
    fn the_row_leaves_four_seconds_after_the_last_press() {
        let state = pressed(PRESS);

        assert_eq!(state.fade(PRESS + 4.0), 1.0);
        assert!((state.fade(PRESS + 4.3) - 0.5).abs() < 1e-6);
        assert!(state.fade(PRESS + 4.6) < 1e-9);
        assert_eq!(state.fade(PRESS + 60.0), 0.0);
        assert_eq!(state.row(PRESS + 4.6), None);
    }

    #[test]
    fn a_second_press_restarts_the_hold() {
        let mut state = pressed(PRESS);
        state.fold(level(), true, PRESS + 3.0);

        assert_eq!(state.fade(PRESS + 4.6), 1.0);
        assert_eq!(state.fade(PRESS + 7.0), 1.0);
        assert!(state.fade(PRESS + 7.6) < 1e-9);
    }

    #[test]
    fn a_press_that_lifts_a_leaving_row_shortens_its_fade_in() {
        let mut state = pressed(PRESS);
        // The row is halfway out at 5.3 seconds, and the press lifts it
        // from there, so it reaches full in half of the 350 ms fade.
        state.fold(level(), true, PRESS + 4.3);

        assert!((state.fade(PRESS + 4.3) - 0.5).abs() < 1e-6);
        assert_eq!(state.fade(PRESS + 4.475), 1.0);
    }

    #[test]
    fn the_row_carries_the_level_and_the_muted_flag() {
        let mut state = pressed(PRESS);
        let row = state.row(PRESS + 1.0).expect("the row is up");
        assert_eq!(row.volume, level());
        assert_eq!(row.fade, 1.0);

        state.fold(
            Volume {
                level: 40,
                muted: true,
            },
            true,
            PRESS + 1.0,
        );
        let row = state.row(PRESS + 2.0).expect("the row is up");
        assert!(row.volume.muted);
    }

    #[test]
    fn the_two_fades_ask_for_a_frame_now() {
        let state = pressed(10.0);

        assert_eq!(state.next_frame(10.0), Some(10.0));
        assert_eq!(state.next_frame(10.34), Some(10.34));
        assert_eq!(state.next_frame(14.3), Some(14.3));
    }

    #[test]
    fn the_row_sleeps_through_its_hold_and_wakes_to_leave() {
        let state = pressed(10.0);

        assert_eq!(state.next_frame(10.35), Some(14.0));
        assert_eq!(state.next_frame(13.9), Some(14.0));
    }

    #[test]
    fn a_row_that_has_left_asks_for_no_frame() {
        assert_eq!(pressed(10.0).next_frame(14.61), None);
        assert_eq!(Level::default().next_frame(600.0), None);
    }
}
