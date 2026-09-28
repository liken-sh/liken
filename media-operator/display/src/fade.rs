//! The fade the whole display shares. One factor runs from 0 clear to 1
//! full, and every colour the display draws scales its alpha by it, so the
//! display fades as one.
//!
//! Three things on the display come and go on clocks of their own: the OSD,
//! the up-next card, and the volume row. Each one carries a [`Clock`], and
//! the frame loop steps the fades and arms the windows, because the loop owns
//! the timer.

use crate::theme;

/// What the hide window does after a change. A summon arms one, a dismiss
/// cancels it, and a press while paused leaves the display standing.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Hide {
    #[default]
    Keep,
    Arm,
    Cancel,
}

/// One element's own clock: how far its fade has moved, and what its hide
/// window is asked to do next.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Clock {
    fade: Fade,
    hide: Hide,
}

impl Clock {
    /// The fade, which the frame loop steps while it is moving.
    pub fn fade(&self) -> &Fade {
        &self.fade
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        &mut self.fade
    }

    /// The hide window this state asks for, which the frame loop arms and
    /// cancels because it owns the timer.
    pub fn take_hide(&mut self) -> Hide {
        std::mem::take(&mut self.hide)
    }

    /// Ask for a window without moving the fade, which a change that leaves
    /// the element standing does.
    pub fn ask(&mut self, hide: Hide) {
        self.hide = hide;
    }

    /// Rise, and ask for the window that takes this element down again.
    pub fn show(&mut self, hide: Hide) {
        self.fade.to(1.0);
        self.hide = hide;
    }

    /// The window ran out, so the element leaves on its own fade.
    pub fn hide(&mut self) {
        self.fade.to(0.0);
    }
}

/// The factor and where it is going. It starts clear, so the display draws
/// nothing until something summons it.
#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct Fade {
    value: f32,
    target: f32,
}

impl Fade {
    /// The factor, from 0 clear to 1 full.
    pub fn value(self) -> f32 {
        self.value
    }

    /// Whether the factor is still moving. The display runs its tick only
    /// while this holds, so a factor standing at either end costs nothing.
    pub fn running(self) -> bool {
        self.value != self.target
    }

    /// Set the target. One factor serves both directions, so a dismiss
    /// during a fade in reverses it from where it stands rather than
    /// dropping the display and raising it again.
    pub fn to(&mut self, target: f32) {
        self.target = target;
    }

    /// Step the factor toward its target on one tick, at the in rate on the
    /// way up and the out rate on the way down.
    pub fn step(&mut self) {
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

    /// A fade in takes ceil(350 / 16.667) ticks, and a fade out takes
    /// ceil(600 / 16.667).
    fn ticks_to(fade: &mut Fade, target: f32) -> usize {
        fade.to(target);
        let mut ticks = 0;
        while fade.running() {
            fade.step();
            ticks += 1;
            assert!(ticks < 1000, "the fade never reached {target}");
        }
        ticks
    }

    #[test]
    fn the_display_starts_clear_and_runs_no_tick() {
        let fade = Fade::default();
        assert_eq!(fade.value(), 0.0);
        assert!(!fade.running());
    }

    #[test]
    fn a_rise_takes_twenty_one_ticks_and_a_fall_takes_thirty_six() {
        let mut fade = Fade::default();
        assert_eq!(ticks_to(&mut fade, 1.0), 21);
        assert_eq!(fade.value(), 1.0);
        assert_eq!(ticks_to(&mut fade, 0.0), 36);
        assert_eq!(fade.value(), 0.0);
    }

    /// A summon while the display is already up moves nothing, so a repeat
    /// press never drops it back to nothing and raises it again.
    #[test]
    fn a_target_the_factor_already_holds_runs_no_tick() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.to(1.0);
        assert!(!fade.running());
        assert_eq!(fade.value(), 1.0);
    }

    /// A summon during a fade out resumes from where the factor stands, so
    /// the rest of the rise is shorter than a whole one.
    #[test]
    fn a_summon_during_a_fall_resumes_from_where_the_factor_stands() {
        let mut fade = Fade::default();
        ticks_to(&mut fade, 1.0);
        fade.to(0.0);
        for _ in 0..18 {
            fade.step();
        }
        let standing = fade.value();
        assert!(standing > 0.0 && standing < 1.0);

        assert_eq!(ticks_to(&mut fade, 1.0), 11);
        assert_eq!(fade.value(), 1.0);
    }

    #[test]
    fn the_factor_stops_at_the_target_and_never_passes_it() {
        let mut fade = Fade::default();
        fade.to(1.0);
        for _ in 0..100 {
            fade.step();
            assert!(fade.value() <= 1.0);
        }
        assert_eq!(fade.value(), 1.0);

        fade.to(0.0);
        for _ in 0..100 {
            fade.step();
            assert!(fade.value() >= 0.0);
        }
        assert_eq!(fade.value(), 0.0);
    }
}
