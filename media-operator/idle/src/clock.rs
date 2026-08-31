// The wall clock the screen draws, in the unit's own zone.
//
// Rust's standard library has no time zones, so the reading goes through
// `jiff`. `jiff` reads `TZ` and falls back to `/etc/localtime`, and it resolves
// the name against the image's own `/usr/share/zoneinfo`, which the `tzdata`
// package installs. The operator sets `TZ` on the pod when the household
// states a zone, so the clock shows the household's own hour. A pod with no
// zone reads UTC.

/// A wall-clock reading, to the minute. The screen redraws once a second so the
/// minute turns.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Time {
    pub hour: u8,
    pub minute: u8,
}

/// The time now, in the zone `TZ` names.
pub fn now() -> Time {
    let now = jiff::Zoned::now();
    Time {
        hour: now.hour() as u8,
        minute: now.minute() as u8,
    }
}

impl Time {
    /// A twelve-hour clock with no leading zero and a lowercase suffix, as in
    /// "3:01 pm". It is what `display/clock.lua` draws, so the two screens read
    /// the same at the same minute.
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
}
