//! The clock, the top-right element, and the spellings of a time that the
//! other regions share. It reads the wall-clock time and the time the film
//! ends, now plus the time left, so a viewer reads the hour without leaving
//! the film. The current time reads bright, and the end time reads dim.
//!
//! The reading holds one box. It hangs from the top margin and ends on the side
//! margin, whether or not the film has a duration. The idle screen and the
//! library browser draw their own clock in the same box. The three screens
//! follow each other on one panel, and a reading that moved would make the hour
//! jump when one screen replaces another. The end time therefore draws on the
//! line under the reading, where the idle screen draws its activity line. An end
//! time beside the reading would push the reading left by its own width.
//!
//! Rust's standard library has no time zones, so the reading goes through
//! `jiff`. `jiff` reads `TZ` and falls back to `/etc/localtime`, and it resolves
//! the name against the image's own `/usr/share/zoneinfo`, which the `tzdata`
//! package installs.

use std::time::Duration;

use iced::Point;
use jiff::Zoned;

use crate::canvas::{Anchor, Canvas, Line};
use crate::film::Film;
use crate::theme;

/// The row the reading hangs from, and the row the end time draws on.
const TOP_Y: f32 = theme::MARGIN_Y;
const ENDS_Y: f32 = theme::MARGIN_Y + theme::LINE_PITCH;

/// The wall clock now, in the zone `TZ` names.
pub fn now() -> Zoned {
    Zoned::now()
}

/// How long until the wall minute turns. The minute tick sleeps this long
/// before each redraw, so the reading and every end time change when the
/// minute does, and not up to a minute later.
pub fn until_next_minute(now: &Zoned) -> Duration {
    let into = now.timestamp().as_millisecond().rem_euclid(60_000);
    Duration::from_millis((60_000 - into) as u64)
}

/// The wall minute, which is the third part of the position signature.
pub fn minute(now: &Zoned) -> i64 {
    now.timestamp().as_second().div_euclid(60)
}

/// Format a wall-clock time as "3:01 pm", a twelve-hour clock with no leading
/// zero and a lowercase suffix.
pub fn reading(at: &Zoned) -> String {
    let hour = at.hour();
    let suffix = if hour < 12 { "am" } else { "pm" };
    let twelve = match hour % 12 {
        0 => 12,
        hour => hour,
    };
    format!("{twelve}:{:02} {suffix}", at.minute())
}

/// The wall clock `seconds` from `now`, as "ends 2:15 pm". The time left
/// rounds to the nearest minute, the same rounding as the "1h 36m left" on
/// the scrubber's time line, so the clock reading plus that time left gives
/// the end time on the screen.
pub fn ends(now: &Zoned, seconds: f64) -> String {
    let minutes = (seconds.max(0.0) / 60.0 + 0.5).floor() as i64;
    format!(
        "ends {}",
        reading(&now.saturating_add(jiff::Span::new().minutes(minutes)))
    )
}

/// Whole minutes as the browser spells a runtime: "46m", "1h", "1h 30m".
/// Nothing under a minute.
pub fn runtime(seconds: i64) -> Option<String> {
    let minutes = seconds / 60;
    match (minutes / 60, minutes % 60) {
        (0, 0) => None,
        (0, minutes) => Some(format!("{minutes}m")),
        (hours, 0) => Some(format!("{hours}h")),
        (hours, minutes) => Some(format!("{hours}h {minutes}m")),
    }
}

/// A length in seconds as a runtime, to the nearest minute, so a film of
/// 2:05:59 reads "2h 6m".
pub fn length(seconds: f64) -> Option<String> {
    runtime(((seconds.max(0.0) / 60.0 + 0.5).floor() as i64) * 60)
}

/// The lines of the top-right column. With a duration it adds the end time,
/// now plus the time left, on the line under the reading. A paused film reads
/// the hour it would end from where it sits, which is close enough to read at
/// a glance. The current time draws bright and the end time draws dim, so the
/// two tell apart.
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

/// One wall clock a test states, read in UTC so the numbers hold in any zone.
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

    /// 1970-01-01 15:01:00 UTC, and the hours around it.
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

    /// The reading hangs from the top margin at the right, and a film with
    /// no duration adds no line under it.
    #[test]
    fn the_reading_hangs_from_the_top_margin_at_the_right() {
        let lines = lines(&Canvas::default(), &film(None, None), &wall(15, 1));
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].at, Point::new(1824.0, 90.0));
        assert_eq!(lines[0].anchor, Anchor::TopRight);
        assert_eq!(lines[0].size, 34.0);
        assert_eq!(lines[0].color.a, theme::alpha::OPAQUE);
        assert_eq!(lines[0].content, "3:01 pm");
    }

    /// The end time draws dim on the line under the reading, in the same
    /// box, so the reading stays where the other screens draw theirs.
    #[test]
    fn the_end_time_draws_dim_on_the_line_under_the_reading() {
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

    /// A film whose duration is not above zero has no end to name.
    #[test]
    fn a_film_with_no_length_names_no_end() {
        let lines = lines(
            &Canvas::default(),
            &film(Some(0.0), Some(0.0)),
            &wall(15, 1),
        );
        assert_eq!(lines.len(), 1);
    }

    /// 4801 seconds after 3:01 pm is one hour and twenty minutes later, and a
    /// time already past ends now. 5752 seconds read as "1h 36m left" on the
    /// time line, so the end time is 96 minutes after the reading, not 95.
    #[test]
    fn an_end_time_is_the_reading_plus_the_time_left() {
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

    /// The reading hangs off the right edge of a wider canvas.
    #[test]
    fn the_reading_follows_the_side_margin_of_a_wider_canvas() {
        let wide = Canvas::for_output(iced::Size::new(2560.0, 1080.0));
        let lines = lines(&wide, &film(None, None), &wall(15, 1));
        assert_eq!(lines[0].at, Point::new(2464.0, 90.0));
    }

    /// The tick lands when the wall minute turns, so a reading never lags
    /// the minute by up to a minute.
    #[test]
    fn the_wait_ends_when_the_wall_minute_turns() {
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
    fn the_wall_minute_counts_whole_minutes_from_the_epoch() {
        assert_eq!(minute(&at(0)), 0);
        assert_eq!(minute(&at(59)), 0);
        assert_eq!(minute(&at(60)), 1);
        assert_eq!(minute(&at(-1)), -1);
    }
}
