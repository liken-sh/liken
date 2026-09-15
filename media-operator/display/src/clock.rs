//! The clock, the top-right element. It reads the wall-clock time and the time
//! the film ends, now plus the time left, so a viewer reads the hour without
//! leaving the film. The current time reads bright, and the end time reads dim.
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
    let left = (duration - position + 0.5).floor();
    let ends = now.saturating_add(jiff::Span::new().seconds(left as i64));
    lines.push(
        Line::new(
            format!("ends {}", reading(&ends)),
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

    /// The reading hangs from the top margin at the right, and carries no end
    /// time without a duration.
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

    /// The end time draws one line pitch under the reading, and reads dimmer
    /// than it.
    #[test]
    fn the_end_time_draws_one_line_pitch_under_the_reading() {
        let lines = lines(
            &Canvas::default(),
            &film(Some(6000.0), Some(1199.0)),
            &wall(15, 1),
        );
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[0].at, Point::new(1824.0, 90.0));
        assert_eq!(lines[1].at, Point::new(1824.0, 136.0));
        assert_eq!(lines[1].color.a, theme::alpha::SUBDUED);
        // 4801 seconds are left, so the film ends one hour and twenty
        // minutes after the reading.
        assert_eq!(lines[1].content, "ends 4:21 pm");
    }

    #[test]
    fn a_film_with_no_length_draws_no_end_time() {
        for (duration, position) in [
            (Some(0.0), Some(10.0)),
            (None, Some(10.0)),
            (Some(600.0), None),
        ] {
            assert_eq!(
                lines(&Canvas::default(), &film(duration, position), &wall(15, 1)).len(),
                1
            );
        }
    }

    /// The reading hangs off the right edge of a wider canvas.
    #[test]
    fn the_reading_follows_the_side_margin_of_a_wider_canvas() {
        let wide = Canvas::for_output(iced::Size::new(2560.0, 1080.0));
        let lines = lines(&wide, &film(None, None), &wall(15, 1));
        assert_eq!(lines[0].at, Point::new(2464.0, 90.0));
    }

    #[test]
    fn the_wall_minute_counts_whole_minutes_from_the_epoch() {
        assert_eq!(minute(&at(0)), 0);
        assert_eq!(minute(&at(59)), 0);
        assert_eq!(minute(&at(60)), 1);
        assert_eq!(minute(&at(-1)), -1);
    }
}
