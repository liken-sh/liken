// The state a page is in between the select that asks for a film and the
// film that covers the page. It is a pure function of the clock: the second
// it was entered, and the second the exit began.

use crate::look;
use crate::views::curtain::Curtain;

/// The loading state one page is in, as a function of the clock.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Loading {
    // The second the press entered the state.
    entered: f64,
    // The exit, once it has begun.
    left: Option<Exit>,
}

// The exit: the second it ends, and how far away the page stood when it
// began, so the motion runs back from there.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Exit {
    until: f64,
    from: f32,
}

impl Loading {
    /// The state a press enters at this second.
    pub fn entered(at: f64) -> Self {
        Self {
            entered: at,
            left: None,
        }
    }

    /// Start the exit at this second. A state that is already leaving keeps
    /// the exit it runs, so a second ask does not restart it.
    pub fn leave(&mut self, at: f64) {
        if self.left.is_some() {
            return;
        }
        self.left = Some(Exit {
            until: at + look::RETURN,
            from: self.away(at),
        });
    }

    /// Whether the exit has begun.
    pub fn leaving(&self) -> bool {
        self.left.is_some()
    }

    /// How far the page has gone, from 0 whole to 1 fully away.
    pub fn away(&self, at: f64) -> f32 {
        match self.left {
            None => eased(share(at - self.entered, look::DEPARTURE)),
            // The share left is measured from the second the exit ends
            // and not from the second it began, so the last frame of the
            // exit lands on exactly zero.
            Some(exit) => exit.from * share(exit.until - at, look::RETURN),
        }
    }

    /// Whether the exit has run its length. The browser then drops the
    /// state and the page is whole again.
    pub fn done(&self, at: f64) -> bool {
        matches!(self.left, Some(exit) if at >= exit.until)
    }

    /// What one frame draws at this second.
    pub fn curtain(&self, at: f64) -> Curtain {
        Curtain {
            away: self.away(at),
            phase: at,
        }
    }
}

// How far into a length of time this many seconds is, from 0 to 1.
fn share(since: f64, length: f64) -> f32 {
    (since / length).clamp(0.0, 1.0) as f32
}

// The smoothstep the departure runs on, so the page leaves and settles
// rather than starting and stopping on a hard edge.
fn eased(share: f32) -> f32 {
    share * share * (3.0 - 2.0 * share)
}

#[cfg(test)]
mod tests {
    use super::*;

    // The second the press lands on, which every case measures from.
    const PRESS: f64 = 3.0;

    #[test]
    fn the_press_enters_the_state_with_the_page_whole() {
        let state = Loading::entered(PRESS);
        assert_eq!(state.away(PRESS), 0.0);
        assert!(!state.leaving());
        assert!(!state.done(PRESS));
    }

    #[test]
    fn the_page_is_fully_away_after_the_departure() {
        let state = Loading::entered(PRESS);
        assert_eq!(state.away(PRESS + look::DEPARTURE), 1.0);
    }

    #[test]
    fn the_page_leaves_without_going_back() {
        let state = Loading::entered(PRESS);
        let mut last = 0.0;
        for step in 1..=10 {
            let away = state.away(PRESS + look::DEPARTURE * f64::from(step) / 10.0);
            assert!(away > last, "{away} at step {step}");
            last = away;
        }
    }

    #[test]
    fn the_state_holds_with_no_ceiling() {
        let state = Loading::entered(PRESS);
        for held in [1.0, 60.0, 3_600.0] {
            assert_eq!(state.away(PRESS + look::DEPARTURE + held), 1.0);
            assert!(!state.done(PRESS + look::DEPARTURE + held));
        }
    }

    #[test]
    fn the_exit_runs_the_page_back_and_ends() {
        let held = PRESS + 10.0;
        let mut state = Loading::entered(PRESS);
        state.leave(held);

        assert!(state.leaving());
        assert_eq!(state.away(held), 1.0);
        assert_eq!(state.away(held + look::RETURN / 2.0), 0.5);
        assert_eq!(state.away(held + look::RETURN), 0.0);
        assert!(state.done(held + look::RETURN));
    }

    #[test]
    fn an_exit_before_the_page_is_away_runs_back_from_where_it_stood() {
        let part = PRESS + look::DEPARTURE / 2.0;
        let mut state = Loading::entered(PRESS);
        let stood = state.away(part);
        state.leave(part);

        assert_eq!(state.away(part), stood);
        assert_eq!(state.away(part + look::RETURN), 0.0);
    }

    #[test]
    fn a_second_ask_to_leave_does_not_restart_the_exit() {
        let held = PRESS + 10.0;
        let mut state = Loading::entered(PRESS);
        state.leave(held);
        state.leave(held + look::RETURN / 2.0);

        assert!(state.done(held + look::RETURN));
    }

    #[test]
    fn the_curtain_carries_the_clock_the_mark_pulses_on() {
        let state = Loading::entered(PRESS);
        let curtain = state.curtain(PRESS + look::DEPARTURE);
        assert_eq!(curtain.away, 1.0);
        assert_eq!(curtain.phase, PRESS + look::DEPARTURE);
    }
}
