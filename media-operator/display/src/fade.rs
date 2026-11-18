//! A fade is a factor from 0 (clear) to 1 (full). An element multiplies
//! the alpha of every color it draws by its factor, so the whole element
//! fades together.
//!
//! Each element that fades on its own, such as the OSD, the up-next
//! card, the volume row, and the skip control, has a [`Clock`]: its
//! [`Fade`] and a request for its hide timer. The element only records
//! the request, because the frame loop owns the timers.

use std::time::Duration;

use crate::theme;

/// What the frame loop does with an element's hide timer: `Arm` starts
/// it, `Cancel` stops it, and `Keep` leaves it as it is. The OSD cancels
/// its timer while the film is paused, so the OSD stays up.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Hide {
    #[default]
    Keep,
    Arm,
    Cancel,
}

/// The fade of one element, and the hide timer request that the frame
/// loop has not read yet.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Clock {
    fade: Fade,
    hide: Hide,
}

impl Clock {
    pub fn fade(&self) -> &Fade {
        &self.fade
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        &mut self.fade
    }

    /// Return the pending hide request and reset it to `Keep`.
    pub fn take_hide(&mut self) -> Hide {
        std::mem::take(&mut self.hide)
    }

    /// Request a hide timer action and leave the fade as it is.
    pub fn request_hide(&mut self, hide: Hide) {
        self.hide = hide;
    }

    /// Fade in, and request a hide timer action.
    pub fn show(&mut self, hide: Hide) {
        self.fade.to(1.0);
        self.hide = hide;
    }

    /// Fade out. The frame loop calls this when the hide timer expires.
    pub fn hide(&mut self) {
        self.fade.to(0.0);
    }
}

/// The current factor and the target it moves toward. It starts at 0, so
/// nothing draws until a `show`.
#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct Fade {
    value: f32,
    target: f32,
    exit: Option<Exit>,
}

/// A fade out along the exit curve: the factor it started from, the ticks
/// that have run, and the total ticks.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Exit {
    from: f32,
    tick: u32,
    ticks: u32,
}

impl Fade {
    /// The factor, from 0 (clear) to 1 (full).
    pub fn value(self) -> f32 {
        self.value
    }

    /// Whether the factor has not reached its target. The display runs
    /// its tick only while a fade is running, so a fade at rest redraws
    /// nothing.
    pub fn running(self) -> bool {
        self.value != self.target
    }

    /// Whether the element draws: its factor or its target is above 0.
    pub fn shown(self) -> bool {
        self.value > 0.0 || self.target > 0.0
    }

    /// Set the target and cancel any exit. The factor moves from its
    /// current value, so a dismiss during a fade in reverses the fade
    /// from where it is.
    pub fn to(&mut self, target: f32) {
        self.target = target;
        self.exit = None;
    }

    /// Fall from the current value to 0 over `over`, along the ease-in
    /// curve t³. When the viewer takes the up-next offer, the card fades
    /// out this way over `theme::HAND_OFF`, which is the length of the
    /// hand-off that the arranger asks the compositor to run on the film
    /// with the `ease_in` curve. So the card and the film fade out
    /// together. A fade already at 0 runs no exit.
    pub fn exit(&mut self, over: Duration) {
        self.target = 0.0;
        let ticks = (over.as_secs_f32() / theme::FADE_TICK.as_secs_f32()).round() as u32;
        self.exit = (self.value > 0.0).then_some(Exit {
            from: self.value,
            tick: 0,
            ticks: ticks.max(1),
        });
    }

    /// Move the factor to its target at once. An element that is not
    /// drawn calls this, so no tick redraws the frame for it.
    pub fn finish(&mut self) {
        self.value = self.target;
        self.exit = None;
    }

    /// Advance one tick: along the exit curve during an exit, and
    /// otherwise toward the target, at the `FADE_IN` rate when rising and
    /// the `FADE_OUT` rate when falling.
    pub fn step(&mut self) {
        if let Some(exit) = self.exit.as_mut() {
            exit.tick += 1;
            let done = exit.tick as f32 / exit.ticks as f32;
            self.value = exit.from * (1.0 - done.powi(3));
            if exit.tick >= exit.ticks {
                self.finish();
            }
            return;
        }
        let rate = if self.target > self.value {
            theme::FADE_IN
        } else {
            theme::FADE_OUT
        };
        let step = theme::FADE_TICK.as_secs_f32() / rate.as_secs_f32();
        self.value = if self.target > self.value {
            self.target.min(self.value + step)
        } else {
            self.target.max(self.value - step)
        };
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Set the target and step until the fade stops. Returns the number
    /// of ticks: `FADE_IN / FADE_TICK` for a full rise and
    /// `FADE_OUT / FADE_TICK` for a full fall, rounded up.
    fn ticks_to(fade: &mut Fade, target: f32) -> usize {
        fade.to(target);
        let mut ticks = 0;
        while fade.running() {
            fade.step();
            ticks += 1;
            assert!(
                ticks < 1000,
                "the fade did not reach {target} in 1000 ticks"
            );
        }
        ticks
    }

    #[test]
    fn the_display_starts_clear_and_runs_no_tick() {
        let fade = Fade::default();
        assert_eq!(fade.value(), 0.0);
        assert!(!fade.running(), "a tick is scheduled, want none");
    }

    #[test]
    fn a_rise_takes_twenty_one_ticks_and_a_fall_takes_thirty_six() {
        let mut fade = Fade::default();
        assert_eq!(ticks_to(&mut fade, 1.0), 21);
        assert_eq!(fade.value(), 1.0);
        assert_eq!(ticks_to(&mut fade, 0.0), 36);
        assert_eq!(fade.value(), 0.0);
    }

    /// A repeat press on an element that is already up does not fade it
    /// out and in again.
    #[test]
    fn a_target_equal_to_the_value_runs_no_tick() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.to(1.0);
        assert!(!fade.running(), "a tick is scheduled, want none");
        assert_eq!(fade.value(), 1.0);
    }

    /// A new target during a fade out moves from the current value, so
    /// the rise takes fewer ticks than a full one.
    #[test]
    fn a_target_during_a_fall_continues_from_the_current_value() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.to(0.0);
        for _ in 0..18 {
            fade.step();
        }
        let midway = fade.value();
        assert!(
            midway > 0.0 && midway < 1.0,
            "the value after 18 ticks is {midway}, want between 0 and 1"
        );

        assert_eq!(ticks_to(&mut fade, 1.0), 11);
        assert_eq!(fade.value(), 1.0);
    }

    #[test]
    fn a_finish_moves_the_factor_to_its_target_and_runs_no_tick() {
        let mut fade = Fade::default();
        fade.to(1.0);
        fade.finish();
        assert_eq!(fade.value(), 1.0);
        assert!(!fade.running(), "a tick is scheduled, want none");
    }

    /// An exit over 300 ms takes 18 ticks, and each tick falls further
    /// than the one before it.
    #[test]
    fn an_exit_eases_in_and_takes_eighteen_ticks() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.exit(Duration::from_millis(300));

        let mut values = Vec::new();
        while fade.running() {
            fade.step();
            values.push(fade.value());
        }
        assert_eq!(values.len(), 18);
        assert_eq!(fade.value(), 0.0);
        let drops: Vec<f32> = std::iter::once(1.0)
            .chain(values.iter().copied())
            .collect::<Vec<_>>()
            .windows(2)
            .map(|pair| pair[0] - pair[1])
            .collect();
        assert!(
            drops.windows(2).all(|pair| pair[1] > pair[0]),
            "the drops per tick are {drops:?}, want each larger than the one before"
        );
        assert!((values[8] - (1.0 - 0.125)).abs() < 1e-6, "{values:?}");
    }

    #[test]
    fn an_exit_starts_from_the_current_value() {
        let mut fade = Fade::default();
        fade.to(1.0);
        for _ in 0..10 {
            fade.step();
        }
        let midway = fade.value();
        fade.exit(Duration::from_millis(300));
        fade.step();
        assert!(
            fade.value() < midway,
            "the value is {}, want below {midway}",
            fade.value()
        );
        assert!(
            fade.value() > midway * 0.99,
            "the value is {}, want above {}",
            fade.value(),
            midway * 0.99
        );
    }

    #[test]
    fn an_exit_from_clear_runs_no_tick() {
        let mut fade = Fade::default();
        fade.exit(Duration::from_millis(300));
        assert!(!fade.running(), "a tick is scheduled, want none");
    }

    /// A new target during an exit ends the exit, and the fade moves
    /// from the current value at the `FADE_IN` rate.
    #[test]
    fn a_target_during_an_exit_ends_the_exit() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.exit(Duration::from_millis(300));
        for _ in 0..9 {
            fade.step();
        }
        assert_eq!(ticks_to(&mut fade, 1.0), 3);
    }

    #[test]
    fn the_factor_stops_at_the_target_and_never_passes_it() {
        let mut fade = Fade::default();
        fade.to(1.0);
        for _ in 0..100 {
            fade.step();
            assert!(
                fade.value() <= 1.0,
                "the value is {}, want at most 1.0",
                fade.value()
            );
        }
        assert_eq!(fade.value(), 1.0);

        fade.to(0.0);
        for _ in 0..100 {
            fade.step();
            assert!(
                fade.value() >= 0.0,
                "the value is {}, want at least 0.0",
                fade.value()
            );
        }
        assert_eq!(fade.value(), 0.0);
    }
}
