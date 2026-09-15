//! The image stop. A still photo has no timeline, so it shows no scrubber, and
//! left and right step across the photos in the playlist instead of seeking.

use iced::Point;
use serde_json::json;

use crate::canvas::{Anchor, Canvas, Line};
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::theme;

/// An image item is one whose presentation block declares the image type.
pub fn available(presentation: &Presentation) -> bool {
    presentation.is_image()
}

/// The counter sits low and centered, on the row the scrubber bar holds for a
/// film. playlist-pos counts from zero, so the item is one more.
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

/// left and right walk the playlist across the photos. This is the first use
/// of the multi-item list for navigation, not for seeking within one item.
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
    fn the_counter_sits_centred_on_the_bar_row() {
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
