//! The header, the passive top-left region. It draws the current item's name
//! from the resolved presentation fields, and takes no focus. A movie shows
//! its title. A series shows the series name over a season and episode line.
//! A music item shows the track title over the artist, then the album with
//! its year.
//! An album plays as one mpv item whose chapters are its tracks, so the track
//! name is the chapter mpv plays now, and the artist, the album, and the year
//! come from the item's own block.

use iced::Point;
use serde_json::Value;

use crate::canvas::{Anchor, Line};
use crate::film::Film;
use crate::presentation::Presentation;
use crate::theme;

const LEFT: f32 = theme::MARGIN_X;
const TOP_Y: f32 = theme::MARGIN_Y;
/// The second line sits below the title, far enough to clear the title glyphs.
const SECOND_Y: f32 = TOP_Y + 82.0;
/// A music item runs to a third line, so the header keeps one drop from each
/// of those lines to the next.
const MUSIC_LINE_GAP: f32 = 46.0;

/// The separator between two fields of one line: two spaces, a middle dot, and
/// two spaces.
pub const BETWEEN: &str = "  \u{00B7}  ";

/// The month names, indexed one to twelve, so a date reads with its month
/// spelled out.
const MONTHS: [&str; 12] = [
    "January",
    "February",
    "March",
    "April",
    "May",
    "June",
    "July",
    "August",
    "September",
    "October",
    "November",
    "December",
];

/// A season or an episode arrives as a JSON number. Render it as a whole
/// number, so the line reads "Season 2", not "Season 2.0".
fn num(value: &Value) -> String {
    match value {
        Value::Number(number) => match number.as_f64() {
            Some(number) => format!("{}", number.trunc() as i64),
            None => number.to_string(),
        },
        Value::String(word) => word.clone(),
        other => other.to_string(),
    }
}

/// Turn an ISO date, YYYY-MM-DD, into a readable form like "March 5, 2017". A
/// string that is not an ISO date returns unchanged, so a library that hands a
/// ready-made date shows it as it is.
fn format_date(date: &str) -> String {
    let parts: Vec<&str> = date.split('-').collect();
    let iso = parts.len() == 3
        && parts[0].len() == 4
        && parts[1].len() == 2
        && parts[2].len() == 2
        && parts
            .iter()
            .all(|part| part.bytes().all(|byte| byte.is_ascii_digit()));
    if !iso {
        return date.to_string();
    }
    let month = parts[1].parse::<usize>().unwrap_or_default();
    let Some(name) = MONTHS.get(month.wrapping_sub(1)) else {
        return date.to_string();
    };
    format!(
        "{name} {}, {}",
        parts[2].parse::<u32>().unwrap_or_default(),
        parts[0]
    )
}

/// The second line joins the season, the episode number, and the episode
/// title, and shows only the parts the item declared.
fn second_line(presentation: &Presentation) -> Option<String> {
    let mut parts: Vec<String> = Vec::new();
    if let Some(season) = presentation.season() {
        parts.push(format!("Season {}", num(season)));
    }
    if let Some(episode) = presentation.episode() {
        parts.push(format!("Episode {}", num(episode)));
    }
    if let Some(title) = presentation.episode_title() {
        parts.push(title.to_string());
    }
    if let Some(date) = presentation.date() {
        parts.push(format_date(date));
    }
    (!parts.is_empty()).then(|| parts.join(BETWEEN))
}

/// The lines under a music title: the artist on one, then the album and the
/// year together on the next. A field the block and the tags both leave empty
/// draws nothing.
fn music_lines(presentation: &Presentation, film: &Film) -> Vec<String> {
    let mut lines = Vec::new();
    if let Some(artist) = presentation.artist(film) {
        lines.push(artist);
    }
    let mut parts: Vec<String> = Vec::new();
    if let Some(album) = presentation.album(film) {
        parts.push(album);
    }
    if let Some(year) = presentation.music_year(film) {
        parts.push(num(&year));
    }
    if !parts.is_empty() {
        lines.push(parts.join(BETWEEN));
    }
    lines
}

// PROSE: with a logo, the second line clears the logo's own height; the art stage brings the bitmap and this reads the fixed line under the title until it does.
fn second_y() -> f32 {
    SECOND_Y
}

// PROSE: whether a logo stands in the title's place, which the art stage answers with the decoded bitmap.
fn has_logo() -> bool {
    false
}

/// A series shows the series name over the season line. Everything else with a
/// title shows the title. A field that resolved to nothing draws nothing. A
/// logo takes the place of that name, and the second line stays as text.
pub fn lines(presentation: &Presentation, film: &Film) -> Vec<Line> {
    let title = |content: String, y: f32| {
        Line::new(
            content,
            Point::new(LEFT, y),
            Anchor::TopLeft,
            theme::type_scale::TITLE,
            theme::color::text(),
        )
    };
    let under = |content: String, y: f32| {
        Line::new(
            content,
            Point::new(LEFT, y),
            Anchor::TopLeft,
            theme::type_scale::SMALL,
            theme::color::muted(),
        )
    };

    let mut lines = Vec::new();
    if presentation.is_music() {
        if let Some(name) = film
            .chapter_title
            .clone()
            .or_else(|| presentation.title(film))
        {
            lines.push(title(name, TOP_Y));
        }
        for (index, line) in music_lines(presentation, film).into_iter().enumerate() {
            lines.push(under(line, second_y() + index as f32 * MUSIC_LINE_GAP));
        }
    } else if presentation.hint() == Some("series") {
        if !has_logo()
            && let Some(series) = presentation
                .series()
                .map(str::to_string)
                .or_else(|| presentation.title(film))
        {
            lines.push(title(series, TOP_Y));
        }
        if let Some(line) = second_line(presentation) {
            lines.push(under(line, second_y()));
        }
    } else {
        let name = presentation.title(film);
        if !has_logo()
            && let Some(name) = name.clone()
        {
            lines.push(title(name, TOP_Y));
        }
        if (has_logo() || name.is_some())
            && let Some(year) = presentation.year()
        {
            lines.push(under(num(year), second_y()));
        }
    }
    lines
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn shown(block: &str, film: &Film) -> Vec<(String, Point, f32)> {
        let mut presentation = Presentation::default();
        presentation.receive(block);
        lines(&presentation, film)
            .into_iter()
            .map(|line| (line.content, line.at, line.size))
            .collect()
    }

    #[test]
    fn a_film_reads_its_title_over_its_year() {
        assert_eq!(
            shown(r#"{"title":"A Film","year":2014}"#, &Film::default()),
            vec![
                ("A Film".to_string(), Point::new(96.0, 90.0), 64.0),
                ("2014".to_string(), Point::new(96.0, 172.0), 34.0),
            ]
        );
    }

    #[test]
    fn a_film_with_no_year_reads_its_title_alone() {
        assert_eq!(
            shown(r#"{"title":"A Film"}"#, &Film::default()),
            vec![("A Film".to_string(), Point::new(96.0, 90.0), 64.0)]
        );
    }

    /// The title falls back to mpv's own `media-title`, and an item that
    /// declares nothing at all draws nothing.
    #[test]
    fn a_film_with_no_block_reads_the_files_own_name() {
        let mut film = Film::default();
        film.apply("media-title", &json!("the-file.mkv"));
        assert_eq!(
            shown("{}", &film),
            vec![("the-file.mkv".to_string(), Point::new(96.0, 90.0), 64.0)]
        );
        assert!(shown("{}", &Film::default()).is_empty());
    }

    #[test]
    fn a_series_reads_its_name_over_the_season_line() {
        assert_eq!(
            shown(
                r#"{"hint":"series","series":"A Show","season":2,"episode":7,
                    "episodeTitle":"The One","date":"2017-03-05"}"#,
                &Film::default()
            ),
            vec![
                ("A Show".to_string(), Point::new(96.0, 90.0), 64.0),
                (
                    "Season 2  \u{00B7}  Episode 7  \u{00B7}  The One  \u{00B7}  March 5, 2017"
                        .to_string(),
                    Point::new(96.0, 172.0),
                    34.0
                ),
            ]
        );
    }

    /// A series with no series name of its own falls back to the title, and a
    /// series that declares no season line draws only the name.
    #[test]
    fn a_series_line_shows_the_parts_the_item_declared() {
        assert_eq!(
            shown(
                r#"{"hint":"series","title":"A Show","episode":7}"#,
                &Film::default()
            ),
            vec![
                ("A Show".to_string(), Point::new(96.0, 90.0), 64.0),
                ("Episode 7".to_string(), Point::new(96.0, 172.0), 34.0),
            ]
        );
        assert_eq!(
            shown(r#"{"hint":"series","series":"A Show"}"#, &Film::default()).len(),
            1
        );
    }

    #[test]
    fn a_music_item_reads_the_playing_chapter_over_the_artist_and_the_album() {
        let mut film = Film::default();
        film.apply("chapter-metadata/by-key/title", &json!("A Song"));
        assert_eq!(
            shown(
                r#"{"type":"music","title":"A Record","artist":"The Band","album":"A Record","year":1979}"#,
                &film
            ),
            vec![
                ("A Song".to_string(), Point::new(96.0, 90.0), 64.0),
                ("The Band".to_string(), Point::new(96.0, 172.0), 34.0),
                (
                    "A Record  \u{00B7}  1979".to_string(),
                    Point::new(96.0, 218.0),
                    34.0
                ),
            ]
        );
    }

    /// With no chapter title the track name falls back to the item's own
    /// title, and a field neither the block nor the tags carry draws nothing.
    #[test]
    fn a_music_item_falls_back_to_the_items_title() {
        assert_eq!(
            shown(r#"{"type":"music","title":"A Record"}"#, &Film::default()),
            vec![("A Record".to_string(), Point::new(96.0, 90.0), 64.0)]
        );
    }

    #[test]
    fn a_number_reads_whole_and_a_date_reads_with_its_month_spelled_out() {
        assert_eq!(num(&json!(2)), "2");
        assert_eq!(num(&json!(2.0)), "2");
        assert_eq!(num(&json!("2")), "2");
        assert_eq!(num(&json!(true)), "true");

        assert_eq!(format_date("2017-03-05"), "March 5, 2017");
        assert_eq!(format_date("2017-12-31"), "December 31, 2017");
        assert_eq!(format_date("March 5, 2017"), "March 5, 2017");
        assert_eq!(format_date("2017-13-05"), "2017-13-05");
        assert_eq!(format_date("2017-3-5"), "2017-3-5");
    }
}
