//! The clock at the top right of the screen, and the time and length
//! formats that other parts of the display reuse. The clock shows the
//! current time in bright text, and under it, in dim text, the time the
//! film ends.
//!
//! The clock draws at the same place whether or not the film has a
//! duration: hung from the top margin, ending at the right margin. The idle
//! screen and the library browser draw their clock there too, and each
//! screen replaces the previous one on the same panel, so a clock that
//! moved would seem to jump. The end time goes on the line under the clock,
//! where the idle screen draws its activity line, because beside the clock
//! it would push the clock left.
//!
//! The standard library has no time zones, so the clock uses `jiff`.
//! `jiff` reads `TZ`, falls back to `/etc/localtime`, and looks up the
//! zone name in the image's `/usr/share/zoneinfo`, which the `tzdata`
//! package installs.

use std::time::Duration;

use iced::Point;
use jiff::Zoned;

use crate::canvas::{Anchor, Canvas, Line};
use crate::film::Film;
use crate::theme;

const TOP_Y: f32 = theme::MARGIN_Y;
const ENDS_Y: f32 = theme::MARGIN_Y + theme::LINE_PITCH;

pub fn now() -> Zoned {
    Zoned::now()
}

/// How long until the minute changes. The display sleeps this long before
/// each redraw, so the clock changes at the start of the minute.
pub fn until_next_minute(now: &Zoned) -> Duration {
    let into = now.timestamp().as_millisecond().rem_euclid(60_000);
    Duration::from_millis((60_000 - into) as u64)
}

/// Whole minutes since the Unix epoch.
pub fn minute(now: &Zoned) -> i64 {
    now.timestamp().as_second().div_euclid(60)
}

/// Format a time as "3:01 pm": twelve-hour, no leading zero, lowercase
/// suffix.
pub fn reading(at: &Zoned) -> String {
    let hour = at.hour();
    let suffix = if hour < 12 { "am" } else { "pm" };
    let twelve = match hour % 12 {
        0 => 12,
        hour => hour,
    };
    format!("{twelve}:{:02} {suffix}", at.minute())
}

/// The time `seconds` after `now`, as "ends 2:15 pm". It rounds to the
/// nearest minute, as the scrubber's "1h 36m left" does, so the clock plus
/// the time left on screen equals the end time on screen.
pub fn ends(now: &Zoned, seconds: f64) -> String {
    let minutes = (seconds.max(0.0) / 60.0 + 0.5).floor() as i64;
    format!(
        "ends {}",
        reading(&now.saturating_add(jiff::Span::new().minutes(minutes)))
    )
}

/// Format the whole minutes of `seconds` as "46m", "1h", or "1h 30m".
/// Returns `None` for less than a minute.
pub fn runtime(seconds: i64) -> Option<String> {
    let minutes = seconds / 60;
    match (minutes / 60, minutes % 60) {
        (0, 0) => None,
        (0, minutes) => Some(format!("{minutes}m")),
        (hours, 0) => Some(format!("{hours}h")),
        (hours, minutes) => Some(format!("{hours}h {minutes}m")),
    }
}

/// Format a length in seconds as a runtime, rounded to the nearest minute.
/// A film of 2:05:59 reads "2h 6m".
pub fn length(seconds: f64) -> Option<String> {
    runtime(((seconds.max(0.0) / 60.0 + 0.5).floor() as i64) * 60)
}

/// The clock, and under it the end time when the film has a positive
/// duration and a position. While the film is paused, the end time is when
/// it would end if it resumed now.
pub fn lines(canvas: &Canvas, film: &Film, now: &Zoned) -> Vec<Line> {
    let right = canvas.right();
    let mut lines = vec![Line::new(
        reading(now),
        Point::new(right, TOP_Y),
        Anchor::TopRight,
        theme::type_scale::SMALL,
        theme::color::text(),
    )];

    let (Some(duration), Some(position)) = (film.duration, film.position) else {
        return lines;
    };
    if duration <= 0.0 {
        return lines;
    }
    lines.push(
        Line::new(
            ends(now, duration - position),
            Point::new(right, ENDS_Y),
            Anchor::TopRight,
            theme::type_scale::SMALL,
            theme::color::text(),
        )
        .alpha(theme::alpha::SUBDUED),
    );
    lines
}

/// A time for a test, in UTC so the results do not depend on the local zone.
#[cfg(test)]
pub fn at(unix: i64) -> Zoned {
    jiff::Timestamp::from_second(unix)
        .expect("a second inside the clock's range")
        .to_zoned(jiff::tz::TimeZone::UTC)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    /// A time of day on 1970-01-01, in UTC.
    fn wall(hour: i64, minute: i64) -> Zoned {
        at(hour * 3600 + minute * 60)
    }

    #[test]
    fn the_reading_is_a_twelve_hour_clock_with_no_leading_zero() {
        assert_eq!(reading(&wall(15, 1)), "3:01 pm");
        assert_eq!(reading(&wall(13, 45)), "1:45 pm");
        assert_eq!(reading(&wall(9, 30)), "9:30 am");
        assert_eq!(reading(&wall(11, 59)), "11:59 am");
        assert_eq!(reading(&wall(0, 0)), "12:00 am");
        assert_eq!(reading(&wall(12, 0)), "12:00 pm");
    }

    fn film(duration: Option<f64>, position: Option<f64>) -> Film {
        let mut film = Film::default();
        if let Some(duration) = duration {
            film.apply("duration", &json!(duration));
        }
        if let Some(position) = position {
            film.apply("time-pos", &json!(position));
        }
        film
    }

    /// The clock hangs from the top margin at the right, and a film with no
    /// duration adds no line under it.
    #[test]
    fn the_clock_hangs_from_the_top_margin_at_the_right() {
        let lines = lines(&Canvas::default(), &film(None, None), &wall(15, 1));
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].at, Point::new(1824.0, 90.0));
        assert_eq!(lines[0].anchor, Anchor::TopRight);
        assert_eq!(lines[0].size, 34.0);
        assert_eq!(lines[0].color.a, theme::alpha::OPAQUE);
        assert_eq!(lines[0].content, "3:01 pm");
    }

    /// The end time draws dim on the line under the clock, so the clock stays
    /// where the other screens draw theirs.
    #[test]
    fn the_end_time_draws_dim_on_the_line_under_the_clock() {
        let lines = lines(
            &Canvas::default(),
            &film(Some(7559.0), Some(1807.0)),
            &wall(15, 1),
        );
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[0].content, "3:01 pm");
        assert_eq!(lines[1].content, "ends 4:37 pm");
        assert_eq!(lines[1].at, Point::new(1824.0, 90.0 + theme::LINE_PITCH));
        assert_eq!(lines[1].anchor, Anchor::TopRight);
        assert_eq!(lines[1].size, 34.0);
        assert_eq!(lines[1].color.a, theme::alpha::SUBDUED);
    }

    #[test]
    fn a_film_with_zero_duration_has_no_end_time() {
        let lines = lines(
            &Canvas::default(),
            &film(Some(0.0), Some(0.0)),
            &wall(15, 1),
        );
        assert_eq!(lines.len(), 1);
    }

    /// 4801 seconds is 80 minutes after 3:01 pm, and a negative time left
    /// ends now. 5752 seconds shows as "1h 36m left", so the end time is 96
    /// minutes after the clock, not 95.
    #[test]
    fn the_end_time_is_the_clock_plus_the_time_left() {
        assert_eq!(ends(&wall(15, 1), 4801.0), "ends 4:21 pm");
        assert_eq!(ends(&wall(15, 1), 5752.0), "ends 4:37 pm");
        assert_eq!(ends(&wall(15, 1), -30.0), "ends 3:01 pm");
    }

    #[test]
    fn a_runtime_reads_in_hours_and_minutes() {
        assert_eq!(runtime(0), None);
        assert_eq!(runtime(59), None);
        assert_eq!(runtime(46 * 60), Some("46m".to_string()));
        assert_eq!(runtime(3600), Some("1h".to_string()));
        assert_eq!(runtime(5400), Some("1h 30m".to_string()));
        assert_eq!(length(7559.0), Some("2h 6m".to_string()));
        assert_eq!(length(5752.0), Some("1h 36m".to_string()));
        assert_eq!(length(29.0), None);
        assert_eq!(length(30.0), Some("1m".to_string()));
    }

    #[test]
    fn the_clock_follows_the_side_margin_of_a_wider_canvas() {
        let wide = Canvas::for_output(iced::Size::new(2560.0, 1080.0));
        let lines = lines(&wide, &film(None, None), &wall(15, 1));
        assert_eq!(lines[0].at, Point::new(2464.0, 90.0));
    }

    #[test]
    fn the_wait_ends_when_the_minute_changes() {
        let cases = [
            ("on the minute", at(15 * 60), Duration::from_secs(60)),
            (
                "twenty seconds in",
                at(15 * 60 + 20),
                Duration::from_secs(40),
            ),
            (
                "one second before",
                at(15 * 60 + 59),
                Duration::from_secs(1),
            ),
            (
                "a part second before",
                jiff::Timestamp::from_millisecond(15 * 60_000 + 59_750)
                    .expect("a millisecond inside the clock's range")
                    .to_zoned(jiff::tz::TimeZone::UTC),
                Duration::from_millis(250),
            ),
        ];
        for (name, now, want) in cases {
            assert_eq!(until_next_minute(&now), want, "{name}");
        }
    }

    #[test]
    fn the_minute_counts_whole_minutes_from_the_epoch() {
        assert_eq!(minute(&at(0)), 0);
        assert_eq!(minute(&at(59)), 0);
        assert_eq!(minute(&at(60)), 1);
        assert_eq!(minute(&at(-1)), -1);
    }
}
