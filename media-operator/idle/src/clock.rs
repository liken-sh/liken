// The wall clock that the screen draws.
//
// Rust's standard library has no time zones, so this reads the time through
// `jiff`. `jiff` reads `TZ`, falls back to `/etc/localtime`, and resolves the
// zone name in the image's `/usr/share/zoneinfo`, which the `tzdata` package
// installs. `media-operator` sets `TZ` on the pod from `spec.timeZone` of the
// `MediaPreferences` named `default`. With no zone, the clock shows UTC.

/// The wall-clock time to the minute.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Time {
    pub hour: u8,
    pub minute: u8,
}

pub fn now() -> Time {
    let now = jiff::ZonedDateTime::now();
    Time {
        hour: now.hour() as u8,
        minute: now.minute() as u8,
    }
}

/// The seconds, with the fraction, that the wall clock is into its current
/// minute. The screen's own clock counts from its first frame, so the screen
/// needs this to schedule the frame at the minute turn.
pub fn into_minute() -> f64 {
    let now = jiff::ZonedDateTime::now();
    f64::from(now.second()) + f64::from(now.subsec_nanosecond()) / 1e9
}

impl Time {
    /// The time on a twelve-hour clock, such as "3:01 pm".
    pub fn twelve_hour(self) -> String {
        let suffix = if self.hour < 12 { "am" } else { "pm" };
        let twelve = match self.hour % 12 {
            0 => 12,
            hour => hour,
        };
        format!("{twelve}:{:02} {suffix}", self.minute)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(hour: u8, minute: u8) -> String {
        Time { hour, minute }.twelve_hour()
    }

    #[test]
    fn an_afternoon_time_has_no_leading_zero() {
        assert_eq!(at(15, 1), "3:01 pm");
        assert_eq!(at(13, 45), "1:45 pm");
    }

    #[test]
    fn a_morning_time_has_the_same_format() {
        assert_eq!(at(9, 30), "9:30 am");
        assert_eq!(at(11, 59), "11:59 am");
    }

    #[test]
    fn the_seconds_into_the_minute_are_below_sixty() {
        let into = into_minute();
        assert!(
            (0.0..60.0).contains(&into),
            "the seconds into the minute are {into}, want 0 up to 60"
        );
    }

    #[test]
    fn midnight_and_noon_show_hour_twelve() {
        assert_eq!(at(0, 0), "12:00 am");
        assert_eq!(at(12, 0), "12:00 pm");
    }
}
