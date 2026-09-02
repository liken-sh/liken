//! The energy: the one scalar that drives the mark's motion, and the alpha the
//! activity line leaves on.
//!
//! It runs from 0, the mark at rest, to 1, the mark at full swing. The
//! `brand` repository's `motion.md` states the rule: the mark moves only while
//! the system works on something a person waits for, and every change of
//! energy eases rather than steps. The numbers below state how.
//!
//! The activity in the `Player`'s retained status decides the target. Starting
//! ramps the energy up, because the seconds a playback pod pulls and starts are
//! the seconds a person waits. Playing stops the motion outright, because the
//! film's surface covers the idle surface and a frame drawn behind it is never
//! seen. Anything else eases the energy back to 0.

use crate::unit::{Ramp, Unit};
use media_screen::status::Activity;

/// The ramp up is shorter than the ramp down, so the mark reaches full swing
/// quickly when a `Play` starts, and returns to rest slowly when the film ends.
const RAMP_UP: f64 = 1.2;
const RAMP_DOWN: f64 = 2.5;

/// The animation clock advances at this fraction of its full rate at energy 0,
/// and at the full rate at energy 1. The floor is above 0 so a ramp down slows
/// the motion without freezing it before the swing itself reaches 0.
const SPEED_FLOOR: f64 = 0.3;

/// Smoothstep, the curve that starts and ends at zero slope. It is what makes a
/// ramp read as an ease and not as a straight climb. The shade eases on the
/// same curve.
pub fn ease(t: f64) -> f64 {
    let t = t.clamp(0.0, 1.0);
    t * t * (3.0 - 2.0 * t)
}

/// The area under [`ease`] from 0 to `t`, which is `t^3 - t^4 / 2`.
///
/// The animation clock is an integral of the level over time, and the level
/// over a ramp is the ramp's two ends with [`ease`] between them. Integrating
/// that curve in closed form is what keeps the clock a function of the wall
/// clock: a frame the client drops costs the motion nothing, and a captured
/// frame is the same frame on every run.
fn area(t: f64) -> f64 {
    let t = t.clamp(0.0, 1.0);
    t.powi(3) - t.powi(4) / 2.0
}

/// The ramp in flight at one moment: its two ends, how long it takes, the
/// second it started, and the animation clock it carried into that second.
///
/// A ramp with both ends at 0 moves nothing, the clock stands still through
/// it, and a settled idle screen animates nothing at all.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Flight {
    from: f64,
    to: f64,
    seconds: f64,
    since: f64,
    phase: f64,
}

impl Flight {
    /// The energy at `at`, which is the ramp's two ends with the ease between
    /// them.
    fn level_at(&self, at: f64) -> f64 {
        self.from + (self.to - self.from) * ease((at - self.since) / self.seconds)
    }

    /// Whether this ramp moves the mark at all. A ramp with both ends at 0
    /// holds the energy at 0 and the animation clock where it stands, so every
    /// frame it covers draws the same mark.
    fn moves(&self) -> bool {
        self.from > 0.0 || self.to > 0.0
    }

    /// The animation clock at `at`.
    ///
    /// The clock runs at the floor rate at energy 0 and at the full rate at
    /// energy 1, so its reading is the integral of that rate over the seconds
    /// the mark moved. The ramp itself contributes the area under its own
    /// curve, and a ramp that has landed above 0 goes on turning the mark at a
    /// steady rate until the next moment.
    fn phase_at(&self, at: f64) -> f64 {
        if !self.moves() {
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

/// The ramp in flight, from the moment the activity last changed and the
/// moment a new surface last came up.
///
/// The target and the duration come from the activity the unit holds now, and
/// the unit's [`Ramp`] holds where that ramp started. An arrival replaces the
/// whole ramp, because a `present` puts the energy at 1 whatever the ramp under
/// it was doing.
fn flight(unit: &Unit) -> Flight {
    let (to, seconds) = match unit.activity {
        Activity::Starting => (1.0, RAMP_UP),
        _ => (0.0, RAMP_DOWN),
    };
    let ramp = Flight {
        from: unit.ramp.from,
        to,
        seconds,
        since: unit.ramp.since,
        phase: unit.ramp.phase,
    };

    // The arrival. A new surface after a film ends brings the mark back at full
    // swing and eases it to rest, so the screen returns in motion rather than
    // appearing frozen. An arrival that lands while a `Play` starts or runs
    // changes nothing, because that activity owns the energy, and a later
    // change of activity replaces the arrival for the same reason.
    match unit.presented {
        Some(presented)
            if presented >= unit.ramp.since
                && !matches!(unit.activity, Activity::Starting | Activity::Playing) =>
        {
            Flight {
                from: 1.0,
                to: 0.0,
                seconds: RAMP_DOWN,
                since: presented,
                phase: ramp.phase_at(presented),
            }
        }
        _ => ramp,
    }
}

/// The energy at one moment.
pub fn level(unit: &Unit, at: f64) -> f64 {
    flight(unit).level_at(at)
}

/// The animation clock the mark's sines read, in seconds.
///
/// The clock advances faster at a high energy than at a low one, which is how
/// the energy scales the speed and the swing together. It never resets, so a
/// change of energy changes the rate of the motion and never its position.
pub fn phase(unit: &Unit, at: f64) -> f64 {
    flight(unit).phase_at(at)
}

/// The second the energy next changes, for [`crate::idle::Idle::next_frame`].
///
/// A ramp changes the energy on every frame it covers, and the harness reads
/// `at` itself as a draw now, so a ramp in flight answers with `at`. A ramp
/// that has landed states nothing, and so does one that moves nothing: the
/// energy then holds one level, and the mark states whether that level still
/// turns the animation clock.
pub fn next_frame(unit: &Unit, at: f64) -> Option<f64> {
    let flight = flight(unit);

    (flight.moves() && at < flight.since + flight.seconds).then_some(at)
}

/// The ramp the energy takes when the activity becomes `next` at `at`.
///
/// A ramp starts from the level the energy stands at, so a reversal turns the
/// motion around from where it is and the mark never jumps. `Playing` is the
/// one activity that steps rather than eases: a film covers this surface, and a
/// frame drawn behind it is never seen. An idle pod that starts in the middle
/// of a film reads its first retained status as `Playing` and lands here, so it
/// starts still instead of animating.
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
    use media_screen::Moment;
    use media_screen::status::Status;

    /// Two readings of one curve. The expected numbers below are read off the
    /// curve itself, so a difference this small is the arithmetic and not the
    /// motion.
    #[track_caller]
    fn assert_close(measured: f64, expected: f64) {
        assert!(
            (measured - expected).abs() < 1e-9,
            "{measured} is not {expected}"
        );
    }

    /// A unit whose activity became `activity` at `at`, which is the one moment
    /// the energy reads.
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
    fn a_screen_that_has_read_nothing_rests() {
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
    fn a_film_stops_the_mark_with_no_ease() {
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
    fn a_reversal_turns_around_from_where_the_mark_stands() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 0.6);

        assert_close(level(&unit, 0.6), 0.5);
        assert_close(level(&unit, 1.85), 0.25);
        assert_close(level(&unit, 3.1), 0.0);
    }

    #[test]
    fn a_repeated_status_does_not_stretch_the_ramp() {
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
    fn an_arrival_brings_the_mark_back_at_full_swing() {
        let mut unit = unit(Activity::Playing, 1.0);
        unit.fold(Moment::Status(Status::default()), 4.0);
        unit.presented = Some(4.5);

        assert_eq!(level(&unit, 4.5), 1.0);
        assert_close(level(&unit, 5.75), 0.5);
        assert_close(level(&unit, 7.0), 0.0);
    }

    #[test]
    fn an_arrival_while_a_play_starts_changes_nothing() {
        let mut unit = unit(Activity::Starting, 1.0);
        unit.presented = Some(1.6);
        assert_close(level(&unit, 1.6), 0.5);
    }

    #[test]
    fn a_status_after_an_arrival_takes_the_energy_the_arrival_reached() {
        let mut unit = Unit {
            presented: Some(0.0),
            ..Unit::default()
        };
        unit.fold(
            Moment::Status(Status {
                activity: Activity::Starting,
                ..Status::default()
            }),
            1.25,
        );

        // The arrival had eased halfway to rest, and the ramp up leaves from
        // there rather than from 0.
        assert_close(level(&unit, 1.25), 0.5);
        assert!(level(&unit, 1.55) > 0.5);
    }

    #[test]
    fn the_clock_stands_still_while_the_mark_rests() {
        let unit = Unit::default();
        assert_eq!(phase(&unit, 0.0), 0.0);
        assert_eq!(phase(&unit, 600.0), 0.0);
    }

    #[test]
    fn the_clock_stands_still_under_a_film() {
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
        assert!((a_second - 1.0).abs() < 1e-12, "{a_second}");
    }

    #[test]
    fn the_clock_never_jumps_when_the_activity_changes() {
        let mut unit = unit(Activity::Starting, 0.0);
        let before = phase(&unit, 0.6);
        unit.fold(Moment::Status(Status::default()), 0.6);
        assert_eq!(phase(&unit, 0.6), before);
    }

    #[test]
    fn the_clock_never_jumps_when_a_surface_arrives() {
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

        unit.fold(Moment::Present, 2.0);
        unit.presented = Some(2.0);
        assert_eq!(phase(&unit, 2.0), before);
    }

    /// The animation clock over one ramp, read from [`Flight::phase_at`].
    fn closed(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
        Flight {
            from,
            to,
            seconds,
            since: 0.0,
            phase: 0.0,
        }
        .phase_at(until)
    }

    /// The same clock by numeric integration.
    ///
    /// The clock is the integral of the rate over time, and the rate at one
    /// moment is the floor plus the rest of the range scaled by the level.
    /// [`ease`] clamps its own input, so a moment after the ramp lands reads the
    /// steady rate the ramp left the mark at. This is the midpoint rule over
    /// enough steps that its own error is under a nanosecond, which makes it a
    /// reading of the mathematics rather than a second copy of the code.
    fn integrated(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
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

    /// A reading of the integral and a reading of the closed form differ only
    /// by the midpoint rule's own error.
    const INTEGRAL_TOLERANCE: f64 = 1e-9;

    #[test]
    fn the_clock_is_the_integral_of_the_rate_over_a_ramp_down() {
        let drift = closed(1.0, 0.0, RAMP_DOWN, 2.5) - integrated(1.0, 0.0, RAMP_DOWN, 2.5);
        assert!(drift.abs() < INTEGRAL_TOLERANCE, "{drift}");
    }

    #[test]
    fn the_clock_is_the_integral_halfway_through_a_ramp() {
        let drift = closed(0.0, 1.0, RAMP_UP, 0.6) - integrated(0.0, 1.0, RAMP_UP, 0.6);
        assert!(drift.abs() < INTEGRAL_TOLERANCE, "{drift}");
    }

    #[test]
    fn the_clock_holds_the_integral_for_seconds_after_a_ramp() {
        let drift = closed(0.0, 1.0, RAMP_UP, 6.0) - integrated(0.0, 1.0, RAMP_UP, 6.0);
        assert!(drift.abs() < INTEGRAL_TOLERANCE, "{drift}");
    }

    #[test]
    fn the_clock_is_the_integral_over_a_reversal() {
        let drift = closed(0.4, 1.0, RAMP_UP, 4.0) - integrated(0.4, 1.0, RAMP_UP, 4.0);
        assert!(drift.abs() < INTEGRAL_TOLERANCE, "{drift}");
    }

    // The phase is defined as this stepped loop: `phase = phase + TICK *
    // (SPEED_FLOOR + (1 - SPEED_FLOOR) * level)`, thirty steps a second, with
    // the level read after the ramp steps. The closed form above must land
    // where the loop lands, so the test below runs the loop to `until`
    // seconds and holds the two together.
    const TICK: f64 = 1.0 / 30.0;

    fn accumulated(from: f64, to: f64, seconds: f64, until: f64) -> f64 {
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

    // The Lua adds one tick of the rate it reads at the end of each frame,
    // which is a right-hand Riemann sum of the integral the closed form takes.
    // Such a sum leads the integral by `(TICK / 2) * (rate at the end - rate at
    // the start)`, which is 0.012 s over a ramp between rest and full swing,
    // and the terms after that one are under a microsecond. The tolerance
    // leaves room for them.
    //
    // Twelve milliseconds of animation clock turns the fastest hexagon's sines
    // by 0.008 of a cycle, which moves a vertex by a third of a pixel on a
    // 1080-row canvas. The two screens draw the same frame.
    const LUA_TOLERANCE: f64 = 0.02;

    #[test]
    fn the_closed_form_agrees_with_the_display_energy_lua_loop() {
        let drift = closed(0.0, 1.0, RAMP_UP, 1.2) - accumulated(0.0, 1.0, RAMP_UP, 1.2);
        assert!(drift.abs() < LUA_TOLERANCE, "{drift}");
    }

    #[test]
    fn a_ramp_up_asks_for_a_frame_until_it_lands() {
        let unit = unit(Activity::Starting, 1.0);
        assert_eq!(next_frame(&unit, 1.0), Some(1.0));
        assert_eq!(next_frame(&unit, 2.19), Some(2.19));
        assert_eq!(next_frame(&unit, 2.21), None);
    }

    #[test]
    fn a_ramp_down_asks_for_a_frame_for_the_whole_2500_ms() {
        let mut unit = unit(Activity::Starting, 0.0);
        unit.fold(Moment::Status(Status::default()), 2.0);

        assert_eq!(next_frame(&unit, 4.4), Some(4.4));
        assert_eq!(next_frame(&unit, 4.5), None);
    }

    #[test]
    fn an_arrival_asks_for_a_frame_from_the_second_the_surface_went_up() {
        let unit = Unit {
            presented: Some(10.0),
            ..Unit::default()
        };

        assert_eq!(next_frame(&unit, 12.4), Some(12.4));
        assert_eq!(next_frame(&unit, 12.5), None);
    }

    #[test]
    fn a_settled_screen_and_a_film_ask_for_no_frame() {
        assert_eq!(next_frame(&Unit::default(), 600.0), None);
        // A film steps the energy to 0 and covers this surface, so the ramp
        // under it moves nothing for the whole 2500 ms it is in flight.
        assert_eq!(next_frame(&unit(Activity::Playing, 0.0), 0.5), None);
    }
}
