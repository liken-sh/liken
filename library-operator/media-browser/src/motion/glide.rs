// One value that eases from where it was drawn to where a press put it,
// such as the scroll of a row or one edge of the focus outline.

use crate::look;

/// One value in motion: the two ends, the second the motion started, and
/// its length. A value at rest is a motion of no length.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Glide {
    from: f32,
    to: f32,
    start: f64,
    length: f64,
}

impl Glide {
    /// A value at rest at this place.
    pub fn still(at: f32) -> Self {
        Self {
            from: at,
            to: at,
            start: 0.0,
            length: 0.0,
        }
    }

    /// The place the value moves to, which is where it rests once the
    /// motion ends.
    pub fn to(&self) -> f32 {
        self.to
    }

    /// The value at this second, eased on the look's curve.
    pub fn at(&self, clock: f64) -> f32 {
        if self.length <= 0.0 {
            return self.to;
        }
        let share = look::eased(look::share(clock - self.start, self.length));
        self.from + (self.to - self.from) * share
    }

    /// The value at this second where the motion still goes to `settled`,
    /// and `settled` itself where something other than a press moved the
    /// place, such as a re-read or a resize. A value the screen reports
    /// is never drawn at a place the screen no longer holds.
    pub fn toward(&self, settled: f32, clock: f64) -> f32 {
        match self.to == settled {
            true => self.at(clock),
            false => settled,
        }
    }

    /// The second the motion ends.
    pub fn ends(&self) -> f64 {
        self.start + self.length
    }

    /// Put the value at this place at once.
    pub fn snap(&mut self, to: f32) {
        *self = Self::still(to);
    }

    /// Start a motion to this place, from the value drawn at this second,
    /// over this length. A place the value already moves to keeps the
    /// motion it has, so a press that leaves the place alone restarts
    /// nothing.
    pub fn go(&mut self, to: f32, clock: f64, length: f64) {
        if to == self.to {
            return;
        }
        *self = Self {
            from: self.at(clock),
            to,
            start: clock,
            length,
        };
    }

    /// Follow the place a screen reports at this second: travel there over
    /// `length`, scaled by the press's pace, where a press moved it, and
    /// snap there where nothing pressed, such as a re-read or a resize.
    /// The answer is the second the value comes to rest.
    pub fn follow(&mut self, settled: f32, clock: f64, pace: Option<f64>, length: f64) -> f64 {
        match pace {
            Some(pace) => self.go(settled, clock, length * pace),
            None if settled != self.to => self.snap(settled),
            None => {}
        }
        self.ends()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_value_at_rest_stays_where_it_is() {
        let still = Glide::still(40.0);
        assert_eq!(still.at(0.0), 40.0);
        assert_eq!(still.at(1_000.0), 40.0);
        assert_eq!(still.to(), 40.0);
    }

    #[test]
    fn a_motion_runs_from_its_start_to_its_end_on_the_curve() {
        let mut glide = Glide::still(0.0);
        glide.go(100.0, 2.0, 0.2);
        assert_eq!(glide.at(2.0), 0.0);
        assert_eq!(glide.at(2.1), 50.0);
        assert_eq!(glide.at(2.2), 100.0);
        assert_eq!(glide.at(9.0), 100.0);
        assert_eq!(glide.ends(), 2.2);
    }

    #[test]
    fn a_new_place_mid_motion_starts_from_the_value_drawn_then() {
        let mut glide = Glide::still(0.0);
        glide.go(100.0, 0.0, 0.2);
        let drawn = glide.at(0.1);
        glide.go(300.0, 0.1, 0.2);
        assert_eq!(glide.at(0.1), drawn);
        assert_eq!(glide.at(0.3), 300.0);
    }

    #[test]
    fn the_same_place_again_keeps_the_motion_running() {
        let mut glide = Glide::still(0.0);
        glide.go(100.0, 0.0, 0.2);
        glide.go(100.0, 0.1, 0.2);
        assert_eq!(glide.ends(), 0.2);
    }

    #[test]
    fn a_place_moved_by_something_other_than_a_press_draws_at_once() {
        let mut glide = Glide::still(0.0);
        glide.go(100.0, 0.0, 0.2);
        assert_eq!(glide.toward(100.0, 0.1), 50.0);
        assert_eq!(glide.toward(250.0, 0.1), 250.0);
    }

    #[test]
    fn a_place_a_press_moved_is_travelled_and_any_other_is_snapped() {
        let cases = [
            (Some(1.0), 0.1, 50.0, 0.2),
            (Some(0.5), 0.05, 50.0, 0.1),
            (None, 0.1, 100.0, 0.0),
        ];
        for (pace, at, drawn, ends) in cases {
            let mut glide = Glide::still(0.0);
            assert_eq!(glide.follow(100.0, 0.0, pace, 0.2), ends, "{pace:?}");
            assert_eq!(glide.at(at), drawn, "{pace:?}");
        }
    }

    #[test]
    fn a_snap_ends_the_motion_at_the_new_place() {
        let mut glide = Glide::still(0.0);
        glide.go(100.0, 0.0, 0.2);
        glide.snap(30.0);
        assert_eq!(glide.at(0.05), 30.0);
    }
}
