// The pace of a held key. A remote repeats a held arrow several times a
// second, faster than the outline's longest motion, and a motion of full
// length on every repeat would land further behind the remote with each
// press. So the lengths shrink to the time between two presses.

use crate::look;

/// The second of the last press, which the next press measures its
/// interval from.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Pace {
    last: Option<f64>,
}

impl Pace {
    /// Take a press at this second, and answer the share of its full
    /// length every motion it starts runs at: the time since the last
    /// press as a share of the longest motion, where that is shorter,
    /// and never under `look::HELD_FLOOR`.
    pub fn press(&mut self, at: f64) -> f64 {
        let interval = self.last.map_or(f64::INFINITY, |last| at - last);
        self.last = Some(at);
        (interval / look::FOLLOW).clamp(look::HELD_FLOOR, 1.0)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_press_after_a_pause_runs_at_full_length() {
        let mut pace = Pace::default();
        assert_eq!(pace.press(5.0), 1.0);
        assert_eq!(pace.press(6.0), 1.0);
    }

    #[test]
    fn a_held_key_runs_each_motion_in_the_time_between_two_presses() {
        let half = look::FOLLOW / 2.0;
        let cases = [
            (half, 0.5),
            (look::FOLLOW, 1.0),
            (look::FOLLOW / 10.0, look::HELD_FLOOR),
        ];
        for (interval, share) in cases {
            let mut pace = Pace::default();
            pace.press(0.0);
            assert_eq!(pace.press(interval), share, "{interval}");
        }
    }
}
