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

    // The reading this many seconds after this one, to the nearest
    // minute, turning past midnight.
    fn after(self, seconds: i64) -> Time {
        const DAY: i64 = 24 * 60;
        let minutes = i64::from(self.hour) * 60 + i64::from(self.minute) + (seconds + 30) / 60;
        let minutes = minutes.rem_euclid(DAY);
        Time {
            hour: (minutes / 60) as u8,
            minute: (minutes % 60) as u8,
        }
    }
}

/// When a play of this many seconds that starts now ends, the way the
/// browser spells it beside the button that starts it: "ends 10:42 pm",
/// in the reading the clock draws. A length the catalog does not hold
/// names no end, so the words are empty.
pub fn ends(now: Time, seconds: i64) -> String {
    match seconds > 0 {
        true => format!("ends {}", now.after(seconds).twelve_hour()),
        false => String::new(),
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

/// The wall clock now, in whole seconds since the Unix epoch. The answer
/// the browser keeps on the bus is stamped with it, because that stamp
/// must mean the same thing to the browser that reads it back after a
/// restart, and a run clock starts at zero on every run. A clock set
/// before the epoch reads zero, which is a stamp no answer stands under.
pub fn seconds() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |since| since.as_secs() as i64)
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
    local_at(unsafe { libc::time(std::ptr::null_mut()) })
}

/// The year and the month, from one, that these Unix seconds fall in, in
/// the zone `TZ` names, the zone the clock reads.
pub fn month_of(seconds: i64) -> (i64, u8) {
    let local = local_at(seconds as libc::time_t);
    (i64::from(local.tm_year) + 1900, (local.tm_mon + 1) as u8)
}

// The broken-down local time at these Unix seconds.
fn local_at(at: libc::time_t) -> libc::tm {
    let mut local: libc::tm = unsafe { std::mem::zeroed() };
    unsafe { libc::localtime_r(&at, &mut local) };
    local
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(hour: u8, minute: u8) -> String {
        Time { hour, minute }.twelve_hour()
    }

    // The middle of a month, so the month is the same in every zone.
    #[test]
    fn seconds_fall_in_their_month_and_year() {
        assert_eq!(month_of(1_773_662_400), (2026, 3));
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
    fn a_play_ends_at_the_reading_its_length_reaches_from_now() {
        let now = Time {
            hour: 20,
            minute: 51,
        };
        let cases = [
            (6_660, "ends 10:42 pm"),
            (6_689, "ends 10:42 pm"),
            (6_690, "ends 10:43 pm"),
            (3 * 3_600 + 9 * 60, "ends 12:00 am"),
            (5 * 3_600, "ends 1:51 am"),
            (60, "ends 8:52 pm"),
        ];
        for (seconds, want) in cases {
            assert_eq!(ends(now, seconds), want, "{seconds}");
        }
    }

    #[test]
    fn a_play_of_no_known_length_names_no_end() {
        assert_eq!(ends(Time::default(), 0), "");
        assert_eq!(ends(Time::default(), -5), "");
    }

    #[test]
    fn the_reading_is_a_time_of_day() {
        let time = now();
        assert!(time.hour < 24);
        assert!(time.minute < 60);
    }

    // The stamp counts from the epoch on the wall clock, so every reading
    // is past the second this case names, which fell in 2023.
    #[test]
    fn the_stamp_is_a_second_of_this_century() {
        assert!(seconds() > 1_700_000_000);
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
