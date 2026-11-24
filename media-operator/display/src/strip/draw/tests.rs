use serde_json::json;

use super::*;
use crate::film::Film;
use crate::focus::Action;
use crate::presentation::Presentation;
use crate::strip::Strip;
use crate::track::Track;

const CENTRE: f32 = 960.0;
const RIGHT: f32 = 1824.0;

fn block() -> Presentation {
    Presentation::default()
}

/// English audio and four subtitle tracks: English full, which plays, an
/// English SDH track, an English SDH file, and Spanish with a title.
fn film(sub_delay: f64) -> Film {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "codec": "eac3",
              "demux-channel-count": 6, "selected": true },
            { "id": 1, "type": "sub", "lang": "eng", "codec": "subrip", "selected": true },
            { "id": 2, "type": "sub", "lang": "eng", "codec": "subrip", "hearing-impaired": true },
            { "id": 3, "type": "sub", "lang": "en", "codec": "subrip", "external": true,
              "external-filename": "/media/a-film.en.sdh.srt" },
            { "id": 4, "type": "sub", "lang": "spa", "codec": "subrip",
              "title": "Español (Latinoamérica)" },
        ]),
    );
    film.apply("sub-delay", &json!(sub_delay));
    film
}

/// A film with 30 subtitle tracks in twelve languages, all PGS, and the
/// first English track playing.
fn long() -> Film {
    let languages = [
        ("eng", 4),
        ("chi", 2),
        ("dut", 1),
        ("fre", 5),
        ("ger", 4),
        ("ita", 3),
        ("jpn", 1),
        ("kor", 1),
        ("pol", 1),
        ("por", 3),
        ("spa", 4),
        ("swe", 1),
    ];
    let mut tracks = vec![json!({ "id": 1, "type": "audio", "lang": "eng", "selected": true })];
    let mut id = 0;
    for (lang, count) in languages {
        for at in 0..count {
            id += 1;
            tracks.push(json!({ "id": id, "type": "sub", "lang": lang,
                "codec": "hdmv_pgs_subtitle", "title": format!("Cut {at}"),
                "selected": id == 1 }));
        }
    }
    let mut film = Film::default();
    film.apply("track-list", &json!(tracks));
    film
}

/// The subtitle panel of `film`, open, after `presses`.
fn open(film: &Film, presses: &[Action]) -> PanelView {
    let mut strip = Strip::default();
    strip.press(Action::Right, &block(), film);
    strip.press(Action::Select, &block(), film);
    for press in presses {
        strip.handle(*press, &block(), film, 0);
    }
    strip.panel(&block(), film).expect("an open panel")
}

fn ends(line: &Line) -> f32 {
    match line.anchor {
        Anchor::Right | Anchor::TopRight => line.at.x,
        Anchor::Centre | Anchor::BottomCentre => {
            line.at.x + measure(&line.content, line.size) / 2.0
        }
        _ => {
            line.at.x
                + if line.italic {
                    measure_italic(&line.content, line.size)
                } else {
                    measure(&line.content, line.size)
                }
        }
    }
}

fn starts(line: &Line) -> f32 {
    match line.anchor {
        Anchor::Right | Anchor::TopRight => line.at.x - measure(&line.content, line.size),
        Anchor::Centre | Anchor::BottomCentre => {
            line.at.x - measure(&line.content, line.size) / 2.0
        }
        _ => line.at.x,
    }
}

/// Each language ends a gutter left of the centre line, each variant
/// starts a gutter right of it, and the readout is centred on it.
#[test]
fn the_tracks_hang_on_the_centre_line() {
    let picture = picture(CENTRE, &open(&film(0.0), &[]), 0);
    let line = |content: &str| {
        picture
            .lines
            .iter()
            .find(|line| line.content == content)
            .unwrap_or_else(|| panic!("no line reads {content:?}"))
    };
    let cases = [
        ("English", ends(line("English")), CENTRE - GUTTER),
        ("Spanish", ends(line("Spanish")), CENTRE - GUTTER),
        ("Off", starts(line("Off")), CENTRE + GUTTER),
        ("SDH", starts(line("SDH")), CENTRE + GUTTER),
        (
            "the readout",
            line("Subtitles as timed in the file").at.x,
            CENTRE,
        ),
    ];
    for (name, x, want) in cases {
        assert!((x - want).abs() < 0.01, "{name} is at {x}, want {want}");
    }
}

/// English is written once for its group, and the rows under it show only
/// their role and format. The English tracks differ in format, so each
/// keeps its own. The Spanish group has one track, so its format is
/// written once, beside its language.
#[test]
fn a_language_is_written_once_and_a_shared_format_once() {
    let picture = picture(CENTRE, &open(&film(0.0), &[]), 0);
    let count = |content: &str| {
        picture
            .lines
            .iter()
            .filter(|line| line.content == content)
            .count()
    };
    assert_eq!(count("English"), 1);
    assert_eq!(count("SRT"), 3);
    assert_eq!(count("SRT file"), 1);
}

/// The panel ends above the time over the scrubber. A long list shows
/// five rows and an edge line at each end, so the top of the panel, and
/// the cue that rises above it, stays in the lower two thirds of the
/// frame, wherever the list is scrolled.
#[test]
fn a_long_list_keeps_the_panel_low_and_its_height_fixed() {
    let tops: Vec<f32> = [vec![], vec![Action::Down; 12], vec![Action::Down; 40]]
        .iter()
        .map(|presses| panel_top(&open(&long(), presses)))
        .collect();
    assert!(
        tops.iter().all(|top| *top == tops[0]),
        "the panel's top moves: {tops:?}"
    );
    let cue_ends = tops[0] - crate::subtitles::lift::GAP;
    assert!(
        cue_ends >= crate::theme::CANVAS_HEIGHT / 3.0,
        "the cue ends at {cue_ends}, in the upper third"
    );
}

/// The edge lines name the languages scrolled out of view, each once, and
/// the playing language keeps its dot.
#[test]
fn the_edge_lines_name_the_hidden_languages() {
    let picture = picture(CENTRE, &open(&long(), &[Action::Down; 12]), 0);
    let words: Vec<&str> = picture
        .lines
        .iter()
        // The formats written under the languages are small lines too.
        .filter(|line| line.size == SMALL && line.content != " \u{b7} " && line.content != "PGS")
        .map(|line| line.content.as_str())
        .collect();
    assert_eq!(
        words,
        vec![
            "sooner",
            "later",
            "Off",
            "English",
            "Chinese",
            "Dutch",
            "French",
            "German",
            "Italian",
            "Japanese",
            "Korean",
            "Polish",
            "Portuguese",
            "and 2 more"
        ]
    );
    let arrows = picture
        .marks
        .iter()
        .filter(|mark| matches!(mark, Mark::Arrow(..)))
        .count();
    let small_dots = picture
        .marks
        .iter()
        .filter(|mark| matches!(mark, Mark::Dot(_, radius, _) if *radius == 4.0))
        .count();
    assert_eq!((arrows, small_dots), (2, 1));
}

/// The compare's hint has room under the scale whether it shows or not,
/// so the rows and the cue do not move when the viewer compares.
#[test]
fn the_compare_hint_moves_nothing() {
    let film = film(-0.7);
    let resting = open(&film, &[Action::Up, Action::Up]);
    let mut strip = Strip::default();
    strip.press(Action::Right, &block(), &film);
    strip.press(Action::Select, &block(), &film);
    strip.handle(Action::Up, &block(), &film, 0);
    strip.handle(Action::Up, &block(), &film, 0);
    strip.handle(Action::Select, &block(), &film, 0);
    let compared = strip
        .panel(&block(), &self::film(0.0))
        .expect("an open panel");
    assert!(
        compared
            .timing
            .as_ref()
            .and_then(|timing| timing.hint.as_ref())
            .is_some()
    );
    assert_eq!(panel_top(&compared), panel_top(&resting));
}

/// At the end of the range, the end word on that side is faint.
#[test]
fn the_end_word_fades_at_the_end_of_the_range() {
    let colour = |delay: f64, word: &str| {
        picture(CENTRE, &open(&film(delay), &[]), 0)
            .lines
            .into_iter()
            .find(|line| line.content == word)
            .map(|line| line.color)
    };
    assert_eq!(colour(-10.0, "sooner"), Some(faint(0.45)));
    assert_eq!(colour(-10.0, "later"), Some(resting()));
    assert_eq!(colour(0.0, "sooner"), Some(resting()));
}

/// The underline glides from the row it left to the row it takes, and it
/// lands after `GLIDE` ticks.
#[test]
fn the_underline_glides_to_the_row_that_takes_the_focus() {
    let film = film(0.0);
    let mut strip = Strip::default();
    strip.press(Action::Right, &block(), &film);
    strip.press(Action::Select, &block(), &film);
    let before = underline(&strip.panel(&block(), &film).expect("a panel"), CENTRE, 100);
    strip.handle(Action::Down, &block(), &film, 100);
    let view = strip.panel(&block(), &film).expect("a panel");
    let landed = underline(&view, CENTRE, 100 + crate::motion::GLIDE);
    assert_eq!(underline(&view, CENTRE, 100), before);
    assert_ne!(landed, before);
    assert!(
        strip.moving(103),
        "the underline does not glide, want it to"
    );
    assert!(!strip.moving(100 + crate::motion::GLIDE));
}

/// The strip's stops are either side of a hairline on the centre line.
/// A file with only audio keeps the hairline, with its audio on the left.
#[test]
fn the_strips_stops_flank_the_centre_line() {
    let cases = [
        ("audio and subtitles", film(0.0)),
        ("only audio", {
            let mut film = Film::default();
            film.apply(
                "track-list",
                &json!([{ "id": 1, "type": "audio", "lang": "eng", "selected": true }]),
            );
            film
        }),
    ];
    for (name, film) in cases {
        let stops = Strip::default().view(&block(), &film, true);
        let (lines, marks) = strip_lines(CENTRE, RIGHT, &stops);
        let audio_ends = lines
            .iter()
            .filter(|line| starts(line) < CENTRE)
            .map(ends)
            .fold(0.0, f32::max);
        assert!(
            (audio_ends - (CENTRE - GUTTER)).abs() < 0.01,
            "{name}: the audio stop ends at {audio_ends}"
        );
        assert!(
            lines
                .iter()
                .filter(|line| starts(line) > CENTRE)
                .all(|line| starts(line) >= CENTRE + GUTTER - 0.01),
            "{name}: a word on the right starts inside the gutter"
        );
        assert_eq!(
            marks[0],
            centred(CENTRE, STRIP_Y, 2.0, 2.0 * STRIP_HAIR, faint(0.5)),
            "{name}"
        );
    }
    assert_eq!(
        Strip::present(&block(), &film(0.0)),
        vec![Track::Audio, Track::Subtitles]
    );
}

/// The focused stop's value is bright and underlined.
#[test]
fn the_focused_stop_is_bright_and_underlined() {
    let mut strip = Strip::default();
    strip.press(Action::Right, &block(), &film(0.0));
    let stops = strip.view(&block(), &film(0.0), true);
    let (lines, marks) = strip_lines(CENTRE, RIGHT, &stops);
    let value = lines
        .iter()
        .find(|line| line.content == "English" && starts(line) > CENTRE)
        .expect("the subtitle value");
    assert_eq!(value.color, focus_ink());
    assert!(
        marks
            .iter()
            .any(|mark| matches!(mark, Mark::Bar(shape, _) if shape.x == value.at.x))
    );
}

/// A panel with no rows, for a moment before it closes, draws no stem of
/// negative height.
#[test]
fn a_panel_with_no_rows_draws_no_negative_mark() {
    let mut strip = Strip::default();
    strip.press(Action::Select, &block(), &film(0.0));
    let mut view = strip.panel(&block(), &film(0.0)).expect("a panel");
    view.rows.clear();
    let picture = picture(CENTRE, &view, 0);
    assert!(
        picture.marks.iter().all(|mark| match mark {
            Mark::Bar(shape, _) => shape.height >= 0.0 && shape.width >= 0.0,
            _ => true,
        }),
        "a mark of the empty panel has a negative size"
    );
}

/// A film shaped like a large disc release: 73 subtitle tracks in 27
/// languages, two of them titled in a right-to-left script, commentary
/// subtitles with long credits, and five commentary audio tracks credited
/// to several people each. The names are invented.
fn release() -> Film {
    let credit = "Commentary by novelist Ann Example, screenwriter Bob Sample, and producers Cy Placeholder and Di Specimen";
    let languages = [
        "eng", "ara", "bul", "ces", "dan", "ger", "gre", "spa", "est", "fin", "fil", "fre", "heb",
        "hin", "hrv", "hun", "ind", "ita", "jpn", "kor", "dut", "nor", "pol", "por", "rus", "swe",
        "tur",
    ];
    let mut tracks = vec![
        json!({ "id": 1, "type": "audio", "lang": "eng", "codec": "truehd",
                "demux-channel-count": 8, "selected": true }),
    ];
    for id in 2..=6 {
        tracks.push(
            json!({ "id": id, "type": "audio", "lang": "eng", "codec": "ac3",
                            "demux-channel-count": 2, "title": credit }),
        );
    }
    let mut id = 0;
    for (at, lang) in languages.iter().enumerate() {
        let count = if at < 19 { 3 } else { 2 };
        for copy in 0..count {
            id += 1;
            let title = match (*lang, copy) {
                ("ara", 0) => "\u{627}\u{644}\u{639}\u{631}\u{628}\u{64a}\u{629}".to_string(),
                ("heb", 0) => "\u{5e2}\u{5d1}\u{5e8}\u{5d9}\u{5ea}".to_string(),
                (_, 1) => "SDH".to_string(),
                (_, 2) => credit.to_string(),
                _ => String::new(),
            };
            tracks.push(json!({ "id": id, "type": "sub", "lang": lang,
                                "codec": "hdmv_pgs_subtitle", "title": title,
                                "hearing-impaired": copy == 1 }));
        }
    }
    let mut film = Film::default();
    film.apply("track-list", &json!(tracks));
    film
}

/// Every line of a picture starts and ends inside the side margins, and
/// every place is finite.
fn inside_the_frame(picture: &Picture, name: &str) {
    let (left, right) = (crate::theme::MARGIN_X - 1.0, RIGHT + 1.0);
    for line in &picture.lines {
        assert!(
            line.at.x.is_finite() && line.at.y.is_finite(),
            "{name}: {:?} is at {:?}",
            line.content,
            line.at
        );
        assert!(
            starts(line) >= left && ends(line) <= right,
            "{name}: {:?} runs from {} to {}, outside {left} to {right}",
            line.content,
            starts(line),
            ends(line)
        );
    }
    for mark in &picture.marks {
        let (x, y) = match mark {
            Mark::Bar(shape, _) => (shape.x, shape.y),
            Mark::Dot(at, _, _) | Mark::Arrow(at, _, _) => (at.x, at.y),
        };
        assert!(
            x.is_finite() && y.is_finite() && (left..=right).contains(&x),
            "{name}: a mark is at {x}, {y}"
        );
    }
}

/// From the first row to the last, every line and mark of a long list
/// stays inside the frame, including the lines that name the hidden
/// languages and the rows with long titles.
#[test]
fn every_focus_of_a_long_list_stays_inside_the_frame() {
    let film = release();
    let mut strip = Strip::default();
    strip.press(Action::Right, &block(), &film);
    strip.press(Action::Select, &block(), &film);
    strip.handle(Action::Up, &block(), &film, 0);
    for press in 0..80 {
        let view = strip.panel(&block(), &film).expect("a panel");
        inside_the_frame(
            &picture(CENTRE, &view, press),
            &format!("subtitles after {press} downs"),
        );
        strip.handle(Action::Down, &block(), &film, press);
    }
}

/// A long title gives way: it ends in an ellipsis, and the format after it
/// stays whole and inside the frame.
#[test]
fn a_long_title_is_cut_and_its_format_stays() {
    let film = release();
    let mut strip = Strip::default();
    strip.press(Action::Select, &block(), &film);
    for press in 0..6 {
        let view = strip.panel(&block(), &film).expect("a panel");
        inside_the_frame(
            &picture(CENTRE, &view, press),
            &format!("audio after {press} downs"),
        );
        strip.handle(Action::Down, &block(), &film, press);
    }
    let view = strip.panel(&block(), &film).expect("a panel");
    let lines = picture(CENTRE, &view, 0).lines;
    assert!(
        lines
            .iter()
            .any(|line| line.content.ends_with(crate::canvas::ELLIPSIS))
    );
    assert!(
        lines
            .iter()
            .filter(|line| line.content == "AC3 2.0")
            .count()
            >= 1
    );
}

/// A line of hidden languages shows the nearest names that fit and a
/// count of the rest, and the playing row keeps its place with its dot
/// even when it is far from the rows that show.
#[test]
fn a_line_of_many_hidden_languages_fits_and_keeps_the_playing_one() {
    let film = release();
    let view = open(&film, &[Action::Down; 30]);
    let picture = picture(CENTRE, &view, 0);
    let words: Vec<&str> = picture
        .lines
        .iter()
        .map(|line| line.content.as_str())
        .collect();
    assert!(
        words
            .iter()
            .any(|word| word.starts_with("and ") && word.ends_with(" more")),
        "no count of the languages that do not fit: {words:?}"
    );
    // No subtitle track plays, so Off plays, and its line keeps it with
    // the dot.
    assert!(
        words.contains(&"Off"),
        "the playing row is not named: {words:?}"
    );
    assert!(
        picture
            .marks
            .iter()
            .any(|mark| matches!(mark, Mark::Dot(_, radius, _) if *radius == 4.0)),
        "the playing row has no dot on its line"
    );
    inside_the_frame(&picture, "the middle of the list");
}

/// Subtitle tracks of one language, from (language, codec, hearing
/// impaired, forced, title) rows.
fn subtitles_of(tracks: &[(&str, &str, bool, bool, &str)]) -> Film {
    let mut list = vec![json!({ "id": 1, "type": "audio", "lang": "eng", "selected": true })];
    for (at, (lang, codec, sdh, forced, title)) in tracks.iter().enumerate() {
        list.push(
            json!({ "id": at + 1, "type": "sub", "lang": lang, "codec": codec,
            "hearing-impaired": sdh, "forced": forced, "title": title, "selected": at == 0 }),
        );
    }
    let mut film = Film::default();
    film.apply("track-list", &json!(list));
    film
}

/// The words of the picture's lines, left of the centre line and right of
/// it, in order.
fn sides(film: &Film) -> (Vec<String>, Vec<String>) {
    let picture = picture(CENTRE, &open(film, &[]), 0);
    let rows: Vec<&Line> = picture
        .lines
        .iter()
        .filter(|line| line.at.y > scale_y(&open(film, &[])) + 20.0)
        .collect();
    let left = rows
        .iter()
        .filter(|line| ends(line) < CENTRE)
        .map(|line| line.content.clone())
        .collect();
    let right = rows
        .iter()
        .filter(|line| starts(line) > CENTRE)
        .map(|line| line.content.clone())
        .collect();
    (left, right)
}

/// When every track of a language has one format, the format is written
/// once, on the language's line, and each row starts with its role, with
/// *full* for the plain track.
#[test]
fn a_one_format_group_writes_its_format_with_the_language() {
    let film = subtitles_of(&[
        ("fre", "hdmv_pgs_subtitle", false, false, ""),
        ("fre", "hdmv_pgs_subtitle", true, false, ""),
        ("fre", "hdmv_pgs_subtitle", false, true, ""),
    ]);
    let (left, right) = sides(&film);
    assert_eq!(left, vec!["French", "PGS"]);
    assert_eq!(right, vec!["Off", "full", "SDH", "forced"]);
}

/// When the formats differ, each row keeps its own after its role.
#[test]
fn a_mixed_format_group_keeps_each_rows_format() {
    let film = subtitles_of(&[
        ("fre", "subrip", false, false, ""),
        ("fre", "hdmv_pgs_subtitle", true, false, ""),
    ]);
    let (left, right) = sides(&film);
    assert_eq!(left, vec!["French"]);
    assert_eq!(right, vec!["Off", "full", "SRT", "SDH", "PGS"]);
}

/// A language with one track keeps its format on the row, beside *full*,
/// so the row reads as a track to pick.
#[test]
fn a_one_track_group_keeps_its_format_on_the_row() {
    let film = subtitles_of(&[("spa", "subrip", false, false, "Castellano")]);
    let (left, right) = sides(&film);
    assert_eq!(left, vec!["Spanish"]);
    assert_eq!(right, vec!["Off", "full", "Castellano", "SRT"]);
}

/// The strip names a full track by its language alone, and the audio
/// panel keeps each track's format on its row, with no role word for a
/// plain track.
#[test]
fn the_word_full_is_only_in_the_subtitle_panel() {
    let subtitled = subtitles_of(&[("eng", "subrip", false, false, "")]);
    let stops = Strip::default().view(&block(), &subtitled, false);
    assert_eq!(stops[1].values[0].name, "English");
    assert_eq!(stops[1].values[0].role, "");

    let audio = picture(
        CENTRE,
        &{
            let mut strip = Strip::default();
            strip.press(Action::Select, &block(), &film(0.0));
            strip.panel(&block(), &film(0.0)).expect("a panel")
        },
        0,
    );
    assert!(audio.lines.iter().all(|line| line.content != "full"));
    assert!(
        audio
            .lines
            .iter()
            .any(|line| line.content == "E-AC3 6 ch" || line.content.starts_with("E-AC3"))
    );
}

/// The lines on one row of a picture, left of the centre line and right of
/// it, for the row whose middle is at `y`.
fn row_at(picture: &Picture, y: f32) -> (Vec<String>, Vec<String>) {
    let on = |line: &&Line| (line.at.y - y).abs() < 1.0;
    let left = picture
        .lines
        .iter()
        .filter(on)
        .filter(|line| ends(line) < CENTRE)
        .map(|line| line.content.clone())
        .collect();
    let right = picture
        .lines
        .iter()
        .filter(on)
        .filter(|line| starts(line) > CENTRE)
        .map(|line| line.content.clone())
        .collect();
    (left, right)
}

/// Whether a group writes its format once is a fact of the group, so a row
/// reads the same wherever the list is scrolled: no row of a group that
/// shares PGS carries it, and wherever the language is written, PGS is on
/// its line, also when only one row of the group shows.
#[test]
fn a_shared_format_reads_the_same_at_every_scroll() {
    let film = release();
    for downs in [0, 20, 40, 60, 74] {
        let view = open(&film, &vec![Action::Down; downs]);
        let picture = picture(CENTRE, &view, 0);
        let languages: Vec<&Line> = picture
            .lines
            .iter()
            .filter(|line| line.size == TYPE && ends(line) < CENTRE && line.content != "PGS")
            .collect();
        assert!(
            !languages.is_empty(),
            "after {downs} downs no language is written"
        );
        for language in languages {
            let (left, right) = row_at(&picture, language.at.y);
            assert_eq!(
                left,
                vec![language.content.clone(), "PGS".to_string()],
                "after {downs} downs"
            );
            assert!(
                !right.contains(&"PGS".to_string()),
                "after {downs} downs a row of {} carries PGS",
                language.content
            );
        }
        assert!(
            picture
                .lines
                .iter()
                .filter(|line| starts(line) > CENTRE && line.size == TYPE)
                .all(|line| line.content != "PGS"),
            "after {downs} downs a row carries PGS"
        );
    }
}

/// The longest pair, a long language name and a long format, fits on the
/// left of the centre line inside the side margin.
#[test]
fn the_longest_language_and_format_fit_on_the_left() {
    let film = subtitles_of(&[
        ("nn", "subrip", false, false, ""),
        ("nn", "subrip", true, false, ""),
    ]);
    let mut film = film;
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 1, "type": "sub", "lang": "nn", "codec": "subrip", "external": true,
              "external-filename": "/media/a.nn.srt", "selected": true },
            { "id": 2, "type": "sub", "lang": "nn", "codec": "subrip", "external": true,
              "external-filename": "/media/a.nn.sdh.srt" },
        ]),
    );
    let picture = picture(CENTRE, &open(&film, &[]), 0);
    let left: Vec<&Line> = picture
        .lines
        .iter()
        .filter(|line| ends(line) < CENTRE)
        .collect();
    assert!(
        left.iter().any(|line| line.content == "SRT file"),
        "no shared format on the left: {:?}",
        left.iter().map(|line| &line.content).collect::<Vec<_>>()
    );
    assert!(
        left.iter()
            .all(|line| starts(line) >= crate::theme::MARGIN_X)
    );
}

fn languages_of(names: &[&str], playing: &str) -> Vec<(String, bool)> {
    names
        .iter()
        .map(|name| (name.to_string(), *name == playing))
        .collect()
}

fn words(parts: &[(Part, bool)]) -> Vec<String> {
    parts
        .iter()
        .map(|(part, _)| part.text.clone())
        .filter(|text| text != " \u{b7} ")
        .collect()
}

/// The line above starts with the count and ends with the names nearest
/// the rows. The line below starts with the nearest names and ends with
/// the count. Off and the playing language keep their place in the list's
/// order.
#[test]
fn the_count_is_at_the_far_end_of_each_line() {
    let fits = |names: &[usize]| names.len() <= 4;
    let above = languages_of(
        &["Off", "English", "Chinese", "Czech", "Danish", "Dutch"],
        "English",
    );
    let chosen = edge_names(&above, true, fits);
    assert_eq!(
        words(&edge_parts(&above, &chosen, true)),
        vec!["2 more", "Off", "English", "Danish", "Dutch"]
    );
    let below = languages_of(
        &["German", "Greek", "Hebrew", "Hindi", "Swedish"],
        "Swedish",
    );
    let chosen = edge_names(&below, false, fits);
    assert_eq!(
        words(&edge_parts(&below, &chosen, false)),
        vec!["German", "Greek", "Hebrew", "Swedish", "and 1 more"]
    );
}

/// The lines of hidden languages are no wider than the scale with its end
/// words, so they read as part of the panel.
#[test]
fn a_line_of_hidden_languages_is_as_wide_as_the_panel_at_most() {
    let view = open(&release(), &[Action::Down; 30]);
    let picture = picture(CENTRE, &view, 0);
    let edges: Vec<&Line> = picture
        .lines
        .iter()
        .filter(|line| {
            line.size == SMALL
                && !line.italic
                && line.content != "PGS"
                && line.at.y > scale_y(&view) + 20.0
        })
        .collect();
    assert!(!edges.is_empty());
    let (left, right) = (CENTRE - EDGE_HALF, CENTRE + EDGE_HALF);
    for line in edges {
        assert!(
            starts(line) >= left - 1.0 && ends(line) <= right + 1.0,
            "{:?} runs from {} to {}, outside {left} to {right}",
            line.content,
            starts(line),
            ends(line)
        );
    }
}
