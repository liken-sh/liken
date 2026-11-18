//! The focus stop of a still photo. A photo has no timeline, so the display
//! shows this stop in place of a film's scrubber. Left and right move to the previous and next item of the playlist.

use iced::Point;
use serde_json::json;

use crate::canvas::{Anchor, Canvas, Line};
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::theme;

/// Whether the `type` of the current item's presentation block is `image`.
pub fn available(presentation: &Presentation) -> bool {
    presentation.is_image()
}

/// The counter line, such as "3 of 7", centered on the row where a film's
/// scrubber bar draws. mpv's `playlist-pos` starts at zero, so the number
/// shown is one more.
pub fn lines(canvas: &Canvas, film: &Film) -> Vec<Line> {
    let at = film.playlist_pos.unwrap_or(0) + 1;
    let count = film.playlist_count.unwrap_or(1);
    vec![Line::new(
        format!("{at} of {count}"),
        Point::new(canvas.centre_x(), theme::BAR_Y),
        Anchor::Centre,
        theme::type_scale::SMALL,
        theme::color::muted(),
    )]
}

/// The mpv command for a left or right press. Any other action returns no
/// command.
pub fn press(action: Action) -> Vec<Command> {
    match action {
        Action::Left => vec![vec![json!("playlist-prev")]],
        Action::Right => vec![vec![json!("playlist-next")]],
        _ => Vec::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn film(at: i64, count: i64) -> Film {
        let mut film = Film::default();
        film.apply("playlist-pos", &json!(at));
        film.apply("playlist-count", &json!(count));
        film
    }

    #[test]
    fn the_counter_is_centred_on_the_bar_row() {
        let lines = lines(&Canvas::default(), &film(2, 7));
        assert_eq!(lines.len(), 1);
        assert_eq!(lines[0].content, "3 of 7");
        assert_eq!(lines[0].at, Point::new(960.0, 904.0));
        assert_eq!(lines[0].anchor, Anchor::Centre);
        assert_eq!(lines[0].size, 34.0);
    }

    #[test]
    fn a_playlist_that_reports_nothing_counts_one_of_one() {
        assert_eq!(
            lines(&Canvas::default(), &Film::default())[0].content,
            "1 of 1"
        );
    }

    #[test]
    fn left_and_right_step_the_playlist_and_nothing_else_does() {
        assert_eq!(press(Action::Left), vec![vec![json!("playlist-prev")]]);
        assert_eq!(press(Action::Right), vec![vec![json!("playlist-next")]]);
        for action in [Action::Up, Action::Down, Action::Select, Action::Back] {
            assert!(press(action).is_empty(), "{action:?} steps nothing");
        }
    }

    #[test]
    fn only_an_image_item_shows_the_counter() {
        let mut presentation = Presentation::default();
        presentation.receive(r#"{"type":"image"}"#);
        assert!(available(&presentation));
        presentation.receive("{}");
        assert!(!available(&presentation));
    }
}
