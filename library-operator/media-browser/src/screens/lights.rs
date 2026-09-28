// How bright the room is while a film is on its way and while it leaves.
// The browser dims its whole frame from the second the `Player` moves off
// `Idle`, so the film fades in over a dimmed page and the page under a
// half-transparent film reads as a room with the lights down. The state
// is a pure function of the clock, the way the loading state beside it
// is: the second the status entered the state, and the second the lift
// began.

use crate::look;

/// The brightness of the room, as a function of the clock.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Lights {
    // The second the status entered the state.
    entered: f64,
    // The lift, once it has begun.
    lifted: Option<Lift>,
}

// The lift: the second it ends, and how bright the room was when it
// began, so the motion runs up from there.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Lift {
    until: f64,
    from: f32,
}

impl Lights {
    /// The state the `Player`'s move off `Idle` enters at this second.
    pub fn entered(at: f64) -> Self {
        Self {
            entered: at,
            lifted: None,
        }
    }

    /// Start the lift at this second. A room that is already coming up
    /// keeps the lift it runs, so a second status does not restart it.
    pub fn lift(&mut self, at: f64) {
        if self.lifted.is_some() {
            return;
        }
        self.lifted = Some(Lift {
            until: at + look::LIGHTS_UP,
            from: self.level(at),
        });
    }

    /// How bright the room is, from `look::LIGHTS_FLOOR` at the darkest
    /// to 1 at full.
    pub fn level(&self, at: f64) -> f32 {
        match self.lifted {
            None => {
                let share = look::share(at - self.entered, look::LIGHTS_DOWN);
                1.0 - (1.0 - look::LIGHTS_FLOOR) * look::eased(share)
            }
            // The lift runs at an even rate, the way the curtain's exit
            // does, so the two read as one motion. The share left is
            // measured from the second the lift ends and not from the
            // second it began, so the last frame of the lift lands on
            // exactly full.
            Some(lift) => 1.0 - (1.0 - lift.from) * look::share(lift.until - at, look::LIGHTS_UP),
        }
    }

    /// Whether the lift has run its length. The browser then drops the
    /// state and the frame carries no dim at all.
    pub fn done(&self, at: f64) -> bool {
        matches!(self.lifted, Some(lift) if at >= lift.until)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The second the status lands on, which every case measures from.
    const MOVED: f64 = 3.0;

    // The level halfway between full and the floor, which both moves pass
    // through at their own midpoint.
    fn half() -> f32 {
        1.0 - (1.0 - look::LIGHTS_FLOOR) / 2.0
    }

    #[test]
    fn the_room_is_at_full_when_the_status_lands() {
        let lights = Lights::entered(MOVED);
        assert_eq!(lights.level(MOVED), 1.0);
        assert!(!lights.done(MOVED));
    }

    #[test]
    fn the_room_goes_to_the_floor_over_the_way_down() {
        let lights = Lights::entered(MOVED);
        for (at, level) in [
            (MOVED, 1.0),
            (MOVED + look::LIGHTS_DOWN / 2.0, half()),
            (MOVED + look::LIGHTS_DOWN, look::LIGHTS_FLOOR),
        ] {
            assert_eq!(lights.level(at), level, "at {at}");
        }
    }

    #[test]
    fn the_room_holds_the_floor_with_no_ceiling() {
        let lights = Lights::entered(MOVED);
        for held in [1.0, 60.0, 3_600.0] {
            let at = MOVED + look::LIGHTS_DOWN + held;
            assert_eq!(lights.level(at), look::LIGHTS_FLOOR, "at {at}");
            assert!(!lights.done(at));
        }
    }

    #[test]
    fn the_lift_runs_the_room_from_the_floor_back_to_full_and_ends() {
        let held = MOVED + 10.0;
        let mut lights = Lights::entered(MOVED);
        lights.lift(held);

        for (at, level) in [
            (held, look::LIGHTS_FLOOR),
            (held + look::LIGHTS_UP / 2.0, half()),
            (held + look::LIGHTS_UP, 1.0),
        ] {
            assert_eq!(lights.level(at), level, "at {at}");
        }
        assert!(!lights.done(held));
        assert!(lights.done(held + look::LIGHTS_UP));
    }

    #[test]
    fn a_lift_part_way_down_runs_the_room_up_from_where_it_stood() {
        let part = MOVED + look::LIGHTS_DOWN / 2.0;
        let mut lights = Lights::entered(MOVED);
        let stood = lights.level(part);
        lights.lift(part);

        assert_eq!(lights.level(part), stood);
        assert_eq!(lights.level(part + look::LIGHTS_UP), 1.0);
    }

    #[test]
    fn a_second_call_to_lift_does_not_restart_it() {
        let held = MOVED + 10.0;
        let mut lights = Lights::entered(MOVED);
        lights.lift(held);
        lights.lift(held + look::LIGHTS_UP / 2.0);

        assert!(lights.done(held + look::LIGHTS_UP));
    }
}
