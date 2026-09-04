// The wall-clock reading, in the zone the pod's `TZ` names, and the one
// libc read this crate makes of the local time. The day's draw reads its
// date through the same call.

/// A wall-clock reading, to the minute. The browser redraws once a
/// minute, so the reading turns.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Time {
    pub hour: u8,
    pub minute: u8,
}

impl Time {
    /// A twelve-hour reading with no leading zero and a lowercase suffix,
    /// as in "3:01 pm". The room's idle screen draws the same reading, so
    /// the two screens read the same at the same minute.
    pub fn twelve_hour(self) -> String {
        let suffix = if self.hour < 12 { "am" } else { "pm" };
        let twelve = match self.hour % 12 {
            0 => 12,
            hour => hour,
        };
        format!("{twelve}:{:02} {suffix}", self.minute)
    }
}

/// The time now, in the zone `TZ` names.
pub fn now() -> Time {
    let local = local();
    Time {
        hour: local.tm_hour as u8,
        minute: local.tm_min as u8,
    }
}

/// The seconds from now until the minute turns, so a caller schedules
/// one redraw on the turn and none between.
pub fn seconds_to_next_minute() -> f64 {
    to_next_minute(local().tm_sec)
}

// The seconds from one second of a minute to the next minute. A leap
// second reads 60 and takes the wait of the second before it, so no
// reading schedules a frame at the second it is already on.
fn to_next_minute(second: i32) -> f64 {
    f64::from(60 - second.clamp(0, 59))
}

/// The broken-down local time. The standard library has no zones, and
/// glibc's `localtime_r` reads `TZ` and `/etc/localtime`, which the operator
/// sets on the pod, so this is one libc call and not a date crate.
pub(crate) fn local() -> libc::tm {
    let now = unsafe { libc::time(std::ptr::null_mut()) };
    let mut local: libc::tm = unsafe { std::mem::zeroed() };
    unsafe { libc::localtime_r(&now, &mut local) };
    local
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(hour: u8, minute: u8) -> String {
        Time { hour, minute }.twelve_hour()
    }

    #[test]
    fn the_afternoon_reads_with_no_leading_zero() {
        assert_eq!(at(15, 1), "3:01 pm");
        assert_eq!(at(13, 45), "1:45 pm");
    }

    #[test]
    fn the_morning_reads_the_same_way() {
        assert_eq!(at(9, 30), "9:30 am");
        assert_eq!(at(11, 59), "11:59 am");
    }

    #[test]
    fn both_ends_of_the_day_read_twelve() {
        assert_eq!(at(0, 0), "12:00 am");
        assert_eq!(at(12, 0), "12:00 pm");
    }

    #[test]
    fn the_reading_is_a_time_of_day() {
        let time = now();
        assert!(time.hour < 24);
        assert!(time.minute < 60);
    }

    #[test]
    fn the_wait_runs_to_the_turn_of_the_minute() {
        for (second, wait) in [(0, 60.0), (1, 59.0), (30, 30.0), (59, 1.0), (60, 1.0)] {
            assert_eq!(to_next_minute(second), wait, "{second}");
        }
        let wait = seconds_to_next_minute();
        assert!(wait > 0.0 && wait <= 60.0, "{wait}");
    }
}
