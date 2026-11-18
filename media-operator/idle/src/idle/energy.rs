//! The energy of the `liken` logo mark that the idle screen draws. Each
//! hexagon of the mark pulses, growing and shrinking about its center.
//! At energy 0 the mark is still. At energy 1, called full swing below,
//! each hexagon pulses by the full amount, `SWING` in `liken_iced::pulse`,
//! and the animation clock runs at full speed. `brand/motion.md` says
//! that the mark moves only while the system works on something a person
//! waits for, and that every change of energy eases.
//!
//! The activity in the `Player`'s status sets the target. `Starting`
//! ramps the energy up, because a person waits while the playback pod
//! pulls its image and starts. `Playing` sets it to 0 at once, because
//! the film covers this screen. `Idle` eases it back to 0 over
//! [`RAMP_DOWN`].

use crate::unit::{Ramp, Unit};
use media_screen::status::Activity;

/// Seconds to ramp the energy from 0 to 1. It is shorter than
/// [`RAMP_DOWN`], so the mark reaches full swing quickly and returns to
/// rest slowly.
const RAMP_UP: f64 = 1.2;

/// Seconds to ease the energy back to 0. `activity::opacity` fades the
/// activity line over the same time.
pub const RAMP_DOWN: f64 = 2.5;

/// The speed of the animation clock at energy 0, as a fraction of full
/// speed. It is above 0, so a ramp down slows the motion without stopping
/// the clock before the pulse reaches 0.
const SPEED_FLOOR: f64 = 0.3;

/// Smoothstep, `3t^2 - 2t^3`, with `t` clamped to `0..=1`. Its slope is
/// zero at both ends, so a ramp starts and ends gently. `shade.rs`, which
/// dims the whole screen toward black, uses the same curve.
pub fn ease(t: f64) -> f64 {
    let t = t.clamp(0.0, 1.0);
    t * t * (3.0 - 2.0 * t)
}

/// The integral of [`ease`] from 0 to `t`: `t^3 - t^4 / 2`.
///
/// The animation clock is the integral of a speed that follows the
/// energy. With a closed form, the clock depends only on the time, so a
/// dropped frame does not change the motion and a capture draws the
/// same frame on every run.
fn area(t: f64) -> f64 {
    let t = t.clamp(0.0, 1.0);
    t.powi(3) - t.powi(4) / 2.0
}

/// One ramp of the energy: from `from` to `to` over `seconds`, starting
/// at second `since`, with the animation clock at `phase` at that
/// second.
#[derive(Debug, Clone, Copy, PartialEq)]
struct ActiveRamp {
    from: f64,
    to: f64,
    seconds: f64,
    since: f64,
    phase: f64,
}

impl ActiveRamp {
    fn level_at(&self, at: f64) -> f64 {
        self.from + (self.to - self.from) * ease((at - self.since) / self.seconds)
    }

    /// False for a ramp from 0 to 0. Such a ramp keeps the clock at its
    /// starting phase, so every frame draws the same mark and a settled
    /// screen schedules no frames.
    fn changes_energy(&self) -> bool {
        self.from > 0.0 || self.to > 0.0
    }

    /// The animation clock at `at`.
    ///
    /// The clock runs at `SPEED_FLOOR` at energy 0 and at full speed at
    /// energy 1. During the ramp the integral uses [`area`]. After a
    /// ramp that ends above 0, the clock runs at the speed of the final
    /// energy.
    fn phase_at(&self, at: f64) -> f64 {
        if !self.changes_energy() {
            return self.phase;
        }

        let elapsed = (at - self.since).max(0.0);
        let ramping = elapsed.min(self.seconds);
        let level = self.from * ramping
            + (self.to - self.from) * self.seconds * area(ramping / self.seconds);
        let mut phase = self.phase + SPEED_FLOOR * ramping + (1.0 - SPEED_FLOOR) * level;

        if self.to > 0.0 && elapsed > self.seconds {
            phase += (SPEED_FLOOR + (1.0 - SPEED_FLOOR) * self.to) * (elapsed - self.seconds);
        }

        phase
    }
}

/// The ramp in effect for this unit. The target and the duration follow
/// `unit.activity`, and the start comes from `unit.ramp`, except after a
/// move from `Playing` to `Idle`.
fn active_ramp(unit: &Unit) -> ActiveRamp {
    let (to, seconds) = match unit.activity {
        Activity::Starting => (1.0, RAMP_UP),
        _ => (0.0, RAMP_DOWN),
    };
    let ramp = ActiveRamp {
        from: unit.ramp.from,
        to,
        seconds,
        since: unit.ramp.since,
        phase: unit.ramp.phase,
    };

    // After a move from `Playing` to `Idle`, the mark starts at full
    // swing and eases to rest, so the screen comes back in motion. The
    // energy was 0 while the film covered the screen.
    //
    // After a move from `Starting` to `Idle`, the ramp that the move
    // started stays, so the motion turns around from its current level
    // and does not jump to full swing.
    //
    // The arrival does not apply while the activity is `Starting` or
    // `Playing`, or when a later change of activity started `unit.ramp`.
    match unit.arrived {
        Some(arrival)
            if arrival.from == Activity::Playing
                && arrival.at >= unit.ramp.since
                && !matches!(unit.activity, Activity::Starting | Activity::Playing) =>
        {
            ActiveRamp {
                from: 1.0,
                to: 0.0,
                seconds: RAMP_DOWN,
                since: arrival.at,
                phase: ramp.phase_at(arrival.at),
            }
        }
        _ => ramp,
    }
}

/// The energy at second `at`.
pub fn level(unit: &Unit, at: f64) -> f64 {
    active_ramp(unit).level_at(at)
}

/// The animation clock in seconds, which `liken_iced::mark::draw` reads.
/// The clock runs faster at higher energy, so the energy scales the speed
/// and the size of the pulse together. It never resets, so a change of energy
/// changes the speed of the motion and does not make the mark jump.
pub fn phase(unit: &Unit, at: f64) -> f64 {
    active_ramp(unit).phase_at(at)
}

/// The second at which the energy next changes, for
/// [`crate::idle::Idle::next_frame`]. It returns `Some(at)` while a ramp
/// runs, so the frame loop in `crate::harness` draws the next frame at
/// once, and `None` after the ramp ends or for a ramp from 0 to 0. When
/// the energy stays above 0 after a ramp, `mark::next_frame` schedules
/// the frames.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    let ramp = active_ramp(unit);

    (ramp.changes_energy() && at < ramp.since + ramp.seconds).then_some(at)
}

/// The ramp to record when the activity changes to `next` at second
/// `at`. It starts from the current energy, so a reversal turns the
/// motion around without a jump. A change to `Playing` starts from 0,
/// because the film covers this screen. So a pod that starts during a
/// film, and reads `Playing` as its first status, draws a still mark.
pub fn ramp(unit: &Unit, next: Activity, at: f64) -> Ramp {
    Ramp {
        since: at,
        from: match next {
            Activity::Playing => 0.0,
            _ => level(unit, at),
        },
        phase: phase(unit, at),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::unit::Arrival;
    use media_screen::Moment;
    use media_screen::status::Status;

    /// The expected values are exact points of the curve, so a difference
    /// above 1e-9 is a wrong result and not float rounding.
    #[track_caller]
    fn assert_close(measured: f64, expected: f64) {
        assert!(
            (measured - expected).abs() < 1e-9,
            "the level is {measured}, want {expected}"
        );
    }

    /// An arrival at `Idle` from `Playing`, the arrival that replaces the
    /// ramp.
    fn arrival_from_film(at: f64) -> Option<Arrival> {
        Some(Arrival {
            at,
            from: Activity::Playing,
        })
    }

    /// A unit whose activity became `activity` at second `at`.
    fn unit(activity: Activity, at: f64) -> Unit {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Status(Status {
                activity,
                ..Status::default()
            }),
            at,
        );
        unit
    }

    #[test]
    fn a_unit_that_has_read_no_status_has_energy_0() {
        let unit = Unit::default();
        assert_eq!(level(&unit, 0.0), 0.0);
        assert_eq!(level(&unit, 600.0), 0.0);
    }

    #[test]
    fn a_starting_play_ramps_the_energy_to_full_swing() {
        let unit = unit(Activity::Starting, 1.0);
        assert_eq!(level(&unit, 1.0), 0.0);
        assert_close(level(&unit, 1.6), 0.5);
        assert_eq!(level(&unit, 2.2), 1.0);
        assert_eq!(level(&unit, 9.0), 1.0);
    }

    #[test]
    fn playing_sets_the_energy_to_0_without_easing() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            0.6,
        );
        assert_eq!(level(&unit, 0.6), 0.0);
        assert_eq!(level(&unit, 0.7), 0.0);
    }

    #[test]
    fn a_reversal_eases_from_the_current_energy() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 0.6);

        assert_close(level(&unit, 0.6), 0.5);
        assert_close(level(&unit, 1.85), 0.25);
        assert_close(level(&unit, 3.1), 0.0);
    }

    #[test]
    fn a_repeated_status_does_not_restart_the_ramp() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.6,
        );
        assert_eq!(level(&unit, 1.2), 1.0);
    }

    #[test]
    fn an_arrival_from_a_film_starts_at_full_swing() {
        let mut unit = unit(Activity::Playing, 1.0);
        unit.fold(Moment::Status(Status::default()), 4.0);
        unit.arrived = arrival_from_film(4.5);

        assert_eq!(level(&unit, 4.5), 1.0);
        assert_close(level(&unit, 5.75), 0.5);
        assert_close(level(&unit, 7.0), 0.0);
    }

    /// The energy is the same as in `a_reversal_eases_from_the_current_energy`,
    /// so no frame jumps to full swing.
    #[test]
    fn an_arrival_from_starting_keeps_the_ramp_and_reverses_it() {
        let mut unit = unit(Activity::Starting, 0.0);
        assert_close(level(&unit, 0.6), 0.5);

        unit.fold(Moment::Status(Status::default()), 0.6);
        unit.arrived = Some(Arrival {
            at: 0.6,
            from: Activity::Starting,
        });

        assert_close(level(&unit, 0.6), 0.5);
        assert_close(level(&unit, 1.85), 0.25);
        assert_close(level(&unit, 3.1), 0.0);
    }

    #[test]
    fn an_arrival_while_starting_does_not_change_the_energy() {
        let mut unit = unit(Activity::Starting, 1.0);
        unit.arrived = arrival_from_film(1.6);
        assert_close(level(&unit, 1.6), 0.5);
    }

    #[test]
    fn a_status_after_an_arrival_starts_from_the_energy_the_arrival_reached() {
        let mut unit = Unit {
            arrived: arrival_from_film(0.0),
            ..Unit::default()
        };
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            1.25,
        );

        // The arrival had eased halfway to rest, so the ramp up starts
        // from 0.5.
        assert_close(level(&unit, 1.25), 0.5);
        assert!(level(&unit, 1.55) > 0.5);
    }

    #[test]
    fn the_clock_does_not_advance_while_the_mark_rests() {
        let unit = Unit::default();
        assert_eq!(phase(&unit, 0.0), 0.0);
        assert_eq!(phase(&unit, 600.0), 0.0);
    }

    #[test]
    fn the_clock_does_not_advance_while_a_film_plays() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Playing,
                ..Status::default()
            }),
            1.2,
        );
        assert_eq!(phase(&unit, 1.2), phase(&unit, 60.0));
    }

    #[test]
    fn the_clock_runs_at_the_full_rate_at_full_swing() {
        let unit = unit(Activity::Starting, 0.0);
        let a_second = phase(&unit, 5.0) - phase(&unit, 4.0);
        assert!(
            (a_second - 1.0).abs() < 1e-12,
            "the clock advanced {a_second} s in one second at full swing, want 1.0"
        );
    }

    #[test]
    fn the_clock_does_not_jump_when_the_activity_changes() {
        let mut unit = unit(Activity::Starting, 0.0);
        let before = phase(&unit, 0.6);
        unit.fold(Moment::Status(Status::default()), 0.6);
        assert_eq!(phase(&unit, 0.6), before);
    }

    #[test]
    fn the_clock_does_not_jump_when_the_screen_comes_back() {
        let mut unit = Unit::default();
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            0.0,
        );
        unit.fold(Moment::Status(Status::default()), 1.2);
        let before = phase(&unit, 2.0);

        unit.arrived = arrival_from_film(2.0);
        assert_eq!(phase(&unit, 2.0), before);
    }

    /// The clock after `until` seconds of one ramp, by
    /// [`ActiveRamp::phase_at`].
    fn closed_form_phase(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
        ActiveRamp {
            from,
            to,
            seconds,
            since: 0.0,
            phase: 0.0,
        }
        .phase_at(until)
    }

    /// The same clock by the midpoint rule, with enough steps that its
    /// error is under a nanosecond. It integrates the speed directly, so
    /// it checks the closed form against the mathematics and not against
    /// a copy of the code. [`ease`] clamps its input, so after the ramp
    /// the speed stays at its final value.
    fn integrated_phase(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
        const STEPS: usize = 100_000;
        let step = until / STEPS as f64;

        (0..STEPS)
            .map(|index| {
                let at = (index as f64 + 0.5) * step;
                let level = from + (to - from) * ease(at / seconds);

                step * (SPEED_FLOOR + (1.0 - SPEED_FLOOR) * level)
            })
            .sum()
    }

    /// The error of the midpoint rule.
    const INTEGRAL_TOLERANCE: f64 = 1e-9;

    #[test]
    fn the_clock_is_the_integral_of_the_rate_over_a_ramp_down() {
        let drift = closed_form_phase(1.0, 0.0, RAMP_DOWN, 2.5)
            - integrated_phase(1.0, 0.0, RAMP_DOWN, 2.5);
        assert!(
            drift.abs() < INTEGRAL_TOLERANCE,
            "the closed form differs from the integral by {drift}, want under {INTEGRAL_TOLERANCE}"
        );
    }

    #[test]
    fn the_clock_is_the_integral_halfway_through_a_ramp() {
        let drift =
            closed_form_phase(0.0, 1.0, RAMP_UP, 0.6) - integrated_phase(0.0, 1.0, RAMP_UP, 0.6);
        assert!(
            drift.abs() < INTEGRAL_TOLERANCE,
            "the closed form differs from the integral by {drift}, want under {INTEGRAL_TOLERANCE}"
        );
    }

    #[test]
    fn the_clock_matches_the_integral_long_after_a_ramp_ends() {
        let drift =
            closed_form_phase(0.0, 1.0, RAMP_UP, 6.0) - integrated_phase(0.0, 1.0, RAMP_UP, 6.0);
        assert!(
            drift.abs() < INTEGRAL_TOLERANCE,
            "the closed form differs from the integral by {drift}, want under {INTEGRAL_TOLERANCE}"
        );
    }

    #[test]
    fn the_clock_is_the_integral_over_a_reversal() {
        let drift =
            closed_form_phase(0.4, 1.0, RAMP_UP, 4.0) - integrated_phase(0.4, 1.0, RAMP_UP, 4.0);
        assert!(
            drift.abs() < INTEGRAL_TOLERANCE,
            "the closed form differs from the integral by {drift}, want under {INTEGRAL_TOLERANCE}"
        );
    }

    // `stepped_phase` advances the clock the way a loop at 30 frames a
    // second would: once per frame, at the speed of the energy at the end
    // of the frame. The closed form must agree with it.
    const TICK: f64 = 1.0 / 30.0;

    fn stepped_phase(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
        let mut phase = 0.0;
        let mut elapsed = 0.0;
        let mut frame = 0.0;

        while frame + TICK <= until + 1e-9 {
            frame += TICK;
            elapsed += TICK;
            let level = from + (to - from) * ease(elapsed / seconds);
            phase += TICK * (SPEED_FLOOR + (1.0 - SPEED_FLOOR) * level);
        }

        phase
    }

    // The loop is a right-hand Riemann sum, which leads the integral by
    // about `(TICK / 2) * (final speed - starting speed)`: 0.012 s for a
    // ramp from rest to full swing. The other error terms are under a
    // microsecond.
    const LOOP_TOLERANCE: f64 = 0.02;

    #[test]
    fn the_closed_form_agrees_with_the_stepped_loop() {
        let drift =
            closed_form_phase(0.0, 1.0, RAMP_UP, 1.2) - stepped_phase(0.0, 1.0, RAMP_UP, 1.2);
        assert!(
            drift.abs() < LOOP_TOLERANCE,
            "the closed form differs from the stepped loop by {drift}, want under {LOOP_TOLERANCE}"
        );
    }

    #[test]
    fn a_ramp_up_draws_every_frame_until_it_ends() {
        let unit = unit(Activity::Starting, 1.0);
        assert_eq!(next_frame(&unit, 1.0), Some(1.0));
        assert_eq!(next_frame(&unit, 2.19), Some(2.19));
        assert_eq!(next_frame(&unit, 2.21), None);
    }

    #[test]
    fn a_ramp_down_draws_every_frame_until_it_ends() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 2.0);

        assert_eq!(next_frame(&unit, 4.4), Some(4.4));
        assert_eq!(next_frame(&unit, 4.5), None);
    }

    #[test]
    fn an_arrival_draws_every_frame_of_its_ramp_down() {
        let unit = Unit {
            arrived: arrival_from_film(10.0),
            ..Unit::default()
        };

        assert_eq!(next_frame(&unit, 12.4), Some(12.4));
        assert_eq!(next_frame(&unit, 12.5), None);
    }

    #[test]
    fn a_settled_screen_and_a_film_need_no_frame() {
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
        // `Playing` sets the energy to 0 and the film covers this surface,
        // so the ramp changes nothing while it lasts.
        assert_eq!(next_frame(&unit(Activity::Playing, 0.0), 0.5), None);
    }
}
