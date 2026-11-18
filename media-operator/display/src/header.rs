//! The header in the top-left corner of the screen. It shows what is playing
//! and takes no input focus. `lines` builds its text from the item's
//! `Presentation`:
//!
//! - A movie shows its title over a line with the year and the length.
//! - A series shows the series name over a line with the season, the
//!   episode, the episode title, and the date.
//! - A music item shows the track title over the artist, and under that the
//!   album and the year. An album plays as one mpv item whose chapters are
//!   its tracks, so the track title is the title of the chapter that mpv is
//!   playing. The artist, album, and year come from the item's block, or
//!   else from the file's tags.
//!
//! For a movie or a series with a logo, the logo is drawn in place of the
//! title and `lines` draws only the line under it. A music item never has a
//! logo, because `art` requests none for it.

use iced::Point;
use serde_json::Value;

use crate::canvas::{Anchor, Line};
use crate::clock;
use crate::film::Film;
use crate::presentation::Presentation;
use crate::theme;

const LEFT: f32 = theme::MARGIN_X;
const TOP_Y: f32 = theme::MARGIN_Y;
/// Y of the line under the title when there is no logo.
const SECOND_Y: f32 = TOP_Y + 82.0;
/// The gap between the bottom of a logo and the line under it.
const SECOND_GAP: f32 = 26.0;
/// The distance from one line under a music title to the next.
const MUSIC_LINE_GAP: f32 = 46.0;

/// Separates two fields on one line: two spaces, a middle dot, two spaces.
pub const BETWEEN: &str = "  \u{00B7}  ";

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

/// Formats a JSON value for a line of text. A number prints as a whole
/// number, so a season that arrives as `2.0` reads "Season 2".
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

/// Turns a `YYYY-MM-DD` date into a form like "March 5, 2017". Any other
/// string, including one with a month outside 1 to 12, is returned
/// unchanged, so a date that the library already formatted shows as it is.
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

/// The line under a series name: the season, the episode number, the episode
/// title, and the date, joined with `BETWEEN`. It leaves out each part that
/// the item does not declare. `Presentation` calls this once when the block
/// arrives, because none of these fields changes while the item plays.
pub fn second_line(presentation: &Presentation) -> Option<String> {
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

/// The line under a film's title: the year, then the length rounded to the
/// nearest minute, as "2008  ·  2h 6m". The length is on this line and not
/// beside the scrubber's time because it does not change during playback.
fn facts(presentation: &Presentation, film: &Film) -> Option<String> {
    let parts: Vec<String> = presentation
        .year()
        .map(num)
        .into_iter()
        .chain(film.duration.and_then(clock::length))
        .collect();
    (!parts.is_empty()).then(|| parts.join(BETWEEN))
}

const TRAILER: &str = "Trailer";

/// For an item whose `role` is `trailer`, appends the trailer mark to the
/// line under the title, or makes the mark the whole line when there is
/// none. Any other item keeps its line unchanged.
fn marked(line: Option<String>, presentation: &Presentation) -> Option<String> {
    if presentation.role() != Some("trailer") {
        return line;
    }
    Some(match line {
        Some(line) => format!("{line}{BETWEEN}{TRAILER}"),
        None => TRAILER.to_string(),
    })
}

/// The lines under a music title: the artist, then the album and the year.
/// A field that neither the block nor mpv's tags supply is left out.
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

/// Y of the line under the title. With a logo, the line goes `SECOND_GAP`
/// below the logo's drawn height, which varies because the logo is scaled
/// to fit its box.
fn second_y(logo: Option<f32>) -> f32 {
    match logo {
        Some(rows) => TOP_Y + rows + SECOND_GAP,
        None => SECOND_Y,
    }
}

/// Builds the header's text lines for the current item. `logo` is the height
/// in canvas units that the drawn logo covers, when there is one. A line
/// whose fields are all missing is left out.
pub fn lines(presentation: &Presentation, film: &Film, logo: Option<f32>) -> Vec<Line> {
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
            lines.push(under(line, second_y(logo) + index as f32 * MUSIC_LINE_GAP));
        }
    } else if presentation.hint() == Some("series") {
        if logo.is_none()
            && let Some(series) = presentation
                .series()
                .map(str::to_string)
                .or_else(|| presentation.title(film))
        {
            lines.push(title(series, TOP_Y));
        }
        if let Some(line) = marked(presentation.second().map(str::to_string), presentation) {
            lines.push(under(line, second_y(logo)));
        }
    } else {
        let name = presentation.title(film);
        if logo.is_none()
            && let Some(name) = name.clone()
        {
            lines.push(title(name, TOP_Y));
        }
        if (logo.is_some() || name.is_some())
            && let Some(line) = marked(facts(presentation, film), presentation)
        {
            lines.push(under(line, second_y(logo)));
        }
    }
    lines
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn shown(block: &str, film: &Film) -> Vec<(String, Point, f32)> {
        with_logo(block, film, None)
    }

    fn with_logo(block: &str, film: &Film, logo: Option<f32>) -> Vec<(String, Point, f32)> {
        let mut presentation = Presentation::default();
        presentation.receive(block);
        lines(&presentation, film, logo)
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

    /// The line under a film's title has the year and the length rounded to
    /// the nearest minute.
    #[test]
    fn a_film_reads_its_year_and_length_on_one_line() {
        let mut film = Film::default();
        film.apply("duration", &json!(7559.0));
        assert_eq!(
            shown(r#"{"title":"A Film","year":2008}"#, &film),
            vec![
                ("A Film".to_string(), Point::new(96.0, 90.0), 64.0),
                (
                    "2008  \u{00B7}  2h 6m".to_string(),
                    Point::new(96.0, 172.0),
                    34.0
                ),
            ]
        );
        assert_eq!(
            shown(r#"{"title":"A Film"}"#, &film)[1].0,
            "2h 6m".to_string()
        );
    }

    #[test]
    fn a_film_with_no_year_reads_its_title_alone() {
        assert_eq!(
            shown(r#"{"title":"A Film"}"#, &Film::default()),
            vec![("A Film".to_string(), Point::new(96.0, 90.0), 64.0)]
        );
    }

    /// With no title in the block, the title is mpv's `media-title`. With
    /// neither, nothing is drawn.
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

    /// A trailer's mark goes on the line under the title, in the movie
    /// layout and the series layout, after any other fields of that line.
    #[test]
    fn a_trailer_adds_its_mark_to_the_line_under_the_title() {
        let cases = [
            (
                r#"{"title":"A Film","year":2014,"role":"trailer"}"#,
                "2014  \u{00B7}  Trailer",
            ),
            (r#"{"title":"A Film","role":"trailer"}"#, "Trailer"),
            (
                r#"{"hint":"series","series":"A Show","role":"trailer"}"#,
                "Trailer",
            ),
            (
                r#"{"hint":"series","series":"A Show","episode":7,"role":"trailer"}"#,
                "Episode 7  \u{00B7}  Trailer",
            ),
        ];
        for (block, want) in cases {
            assert_eq!(
                shown(block, &Film::default()).get(1),
                Some(&(want.to_string(), Point::new(96.0, 172.0), 34.0)),
                "{block}"
            );
        }
    }

    #[test]
    fn a_trailer_with_a_logo_marks_the_line_under_the_logo() {
        assert_eq!(
            with_logo(
                r#"{"title":"A Film","role":"trailer"}"#,
                &Film::default(),
                Some(110.0)
            ),
            vec![("Trailer".to_string(), Point::new(96.0, 226.0), 34.0)]
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

    /// With no chapter title, the track title is the item's title.
    #[test]
    fn a_music_item_falls_back_to_the_items_title() {
        assert_eq!(
            shown(r#"{"type":"music","title":"A Record"}"#, &Film::default()),
            vec![("A Record".to_string(), Point::new(96.0, 90.0), 64.0)]
        );
    }

    #[test]
    fn a_logo_takes_the_place_of_the_title() {
        assert_eq!(
            with_logo(
                r#"{"title":"A Film","year":2014}"#,
                &Film::default(),
                Some(96.0)
            ),
            vec![("2014".to_string(), Point::new(96.0, 212.0), 34.0)]
        );
    }

    #[test]
    fn a_series_with_a_logo_shows_the_season_line_alone() {
        assert_eq!(
            with_logo(
                r#"{"hint":"series","series":"A Show","episode":7}"#,
                &Film::default(),
                Some(110.0)
            ),
            vec![("Episode 7".to_string(), Point::new(96.0, 226.0), 34.0)]
        );
    }

    /// An item with a logo and no title still draws its year. An item with
    /// neither a logo nor a title draws nothing.
    #[test]
    fn a_logo_with_no_title_still_shows_the_year() {
        assert_eq!(
            with_logo(r#"{"year":2014}"#, &Film::default(), Some(110.0)).len(),
            1
        );
        assert!(shown(r#"{"year":2014}"#, &Film::default()).is_empty());
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
