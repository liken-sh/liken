use super::*;
use crate::views;

fn video() -> FileFacts {
    FileFacts {
        role: "primary".into(),
        kind: "video".into(),
        container: "mkv".into(),
        video_codec: "x265".into(),
        audio_codec: "AC3".into(),
        width: 1_920,
        height: 804,
        size_bytes: 4_200_000_000,
        ..FileFacts::default()
    }
}

fn subtitle(language: &str) -> FileFacts {
    FileFacts {
        role: "subtitle".into(),
        kind: "subtitle".into(),
        language: language.into(),
        ..FileFacts::default()
    }
}

fn studios(names: &[&str]) -> Vec<String> {
    names.iter().map(|name| name.to_string()).collect()
}

#[test]
fn a_title_names_its_studios_and_then_every_video_file_it_holds() {
    let foot = Foot::of(
        &studios(&["A Studio", "Another"]),
        &[video(), subtitle("en"), subtitle("fr")],
    );
    assert_eq!(foot.studios().collect::<Vec<_>>(), ["A Studio", "Another"]);
    assert_eq!(
        foot.files(),
        ["1920×804 · x265 · AC3 · 4.2 GB · Subtitles: English, French"]
    );
}

#[test]
fn a_language_the_table_does_not_name_draws_as_the_file_carries_it() {
    let foot = Foot::of(&[], &[video(), subtitle("qq"), subtitle("eng")]);
    assert_eq!(
        foot.files(),
        ["1920×804 · x265 · AC3 · 4.2 GB · Subtitles: qq, English"]
    );
}

#[test]
fn a_file_the_scanner_read_no_frame_or_codec_of_shows_what_it_has() {
    let bare = FileFacts {
        width: 0,
        height: 0,
        video_codec: String::new(),
        size_bytes: 700_000_000,
        ..video()
    };
    let foot = Foot::of(&[], &[bare]);
    assert_eq!(foot.files(), ["AC3 · 700 MB"]);
}

#[test]
fn the_foot_names_the_title_s_own_video_files_and_no_other_file() {
    let trailer = FileFacts {
        role: "trailer".into(),
        ..video()
    };
    let art = FileFacts {
        role: "backdrop".into(),
        kind: "image".into(),
        ..FileFacts::default()
    };
    let foot = Foot::of(&[], &[video(), trailer, art]);
    assert_eq!(foot.files().len(), 1);
}

#[test]
fn a_title_with_no_studio_and_no_file_takes_no_height() {
    let foot = Foot::of(&[], &[]);
    assert!(foot.placed(Point::ORIGIN, 900.0).is_empty());
    assert_eq!(foot.height(900.0), 0.0);
}

#[test]
fn an_empty_studio_name_is_no_stop() {
    let foot = Foot::of(&studios(&["", "A Studio"]), &[]);
    assert_eq!(foot.studios().collect::<Vec<_>>(), ["A Studio"]);
    assert_eq!(foot.studio(0), Some("A Studio"));
    assert_eq!(foot.studio(1), None);
}

#[test]
fn a_foot_is_as_tall_as_its_lines_and_the_gap_between_the_two_kinds() {
    let foot = Foot::of(&studios(&["A Studio"]), &[video()]);
    assert_eq!(
        foot.height(1_680.0),
        text::height(1, PRODUCTION) + GAP + text::height(1, DETAIL)
    );
}

#[test]
fn a_file_line_with_no_production_line_over_it_takes_no_gap() {
    let foot = Foot::of(&[], &[video()]);
    assert_eq!(foot.height(1_680.0), text::height(1, DETAIL));
}

// The outline on the first name reaches no further left than the gap
// after "Produced by", so it never touches those words.
#[test]
fn the_outline_on_the_first_studio_stays_clear_of_the_leading_words() {
    let foot = Foot::of(&studios(&["Gullwing Pictures"]), &[]);
    let at = Point::new(120.0, 900.0);
    let first = foot.placed(at, 1_680.0)[0];
    let words_end = at.x + text::measured(PRODUCED_BY, PRODUCTION);
    assert!(views::clip_marked(first).x > words_end);
    assert_eq!(first.y, at.y);
}

// Two names on one line stand far enough apart that the outline on
// either one stays clear of the other.
#[test]
fn the_outline_on_one_studio_stays_clear_of_the_next() {
    let foot = Foot::of(&studios(&["Gullwing Pictures", "Lantern Works"]), &[]);
    let boxes = foot.placed(Point::ORIGIN, 1_680.0);
    let reach = views::clip_marked(boxes[0]);
    let next = views::clip_marked(boxes[1]);
    assert_eq!(boxes[0].y, boxes[1].y);
    assert!(reach.x + reach.width < boxes[1].x);
    assert!(next.x > boxes[0].x + boxes[0].width);
}

// A name that would run past the width starts a line of its own under
// the first name, and the block grows by that line.
#[test]
fn a_studio_past_the_width_starts_the_next_line_under_the_first() {
    let foot = Foot::of(&studios(&["Gullwing Pictures", "Lantern Works"]), &[]);
    let one_line = foot.placed(Point::ORIGIN, 1_680.0);
    let narrow = one_line[0].x + one_line[0].width + 40.0;
    let boxes = foot.placed(Point::ORIGIN, narrow);
    assert_eq!(boxes[1].x, boxes[0].x);
    assert_eq!(boxes[1].y, boxes[0].height + LINE_GAP);
    assert_eq!(foot.height(narrow), 2.0 * boxes[0].height + LINE_GAP);
    assert!(views::clip_marked(boxes[1]).y > boxes[0].y + boxes[0].height);
}
