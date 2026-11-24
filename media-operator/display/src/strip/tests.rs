use serde_json::json;

use super::*;

/// A film with English and French audio, and English and Spanish
/// subtitles of which the English play, with these delays.
fn film(audio_delay: f64, sub_delay: f64) -> Film {
    let mut film = Film::default();
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "selected": true },
            { "id": 1, "type": "audio", "lang": "eng", "codec": "eac3",
              "demux-channel-count": 6, "selected": true },
            { "id": 2, "type": "audio", "lang": "fra", "codec": "aac",
              "demux-channel-count": 2 },
            { "id": 1, "type": "sub", "lang": "eng", "codec": "subrip", "selected": true },
            { "id": 2, "type": "sub", "lang": "spa", "codec": "subrip" },
        ]),
    );
    film.apply("audio-delay", &json!(audio_delay));
    film.apply("sub-delay", &json!(sub_delay));
    film
}

/// The film after mpv reports that the Spanish subtitles play.
fn spanish() -> Film {
    let mut film = film(0.0, 0.0);
    film.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "selected": true },
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 2, "type": "audio", "lang": "fra" },
            { "id": 1, "type": "sub", "lang": "eng" },
            { "id": 2, "type": "sub", "lang": "spa", "selected": true },
        ]),
    );
    film
}

fn block(text: &str) -> Presentation {
    let mut presentation = Presentation::default();
    presentation.receive(text);
    presentation
}

/// A strip with the panel of `stop` open on the film.
fn open(stop: usize, film: &Film) -> Strip {
    let mut strip = Strip::default();
    for _ in 0..stop {
        strip.press(Action::Right, &block("{}"), film);
    }
    strip.press(Action::Select, &block("{}"), film);
    strip
}

fn focus(strip: &Strip, film: &Film) -> Option<PanelFocus> {
    strip.panel(&block("{}"), film).map(|view| view.focus)
}

/// A case of the key map: its name, the presses, the focus after them,
/// and the commands of the last press.
type Moves = (&'static str, &'static [Action], PanelFocus, Vec<Command>);

/// A case of a select: the track, the presses, the commands of the last
/// press, and the panel open after it.
type Selects = (&'static str, &'static [Action], Vec<Command>, Option<Track>);

fn set(property: &str, value: serde_json::Value) -> Vec<Command> {
    vec![vec![json!("set_property"), json!(property), value]]
}

#[test]
fn a_stop_shows_for_each_kind_the_file_has() {
    let mut angles = film(0.0, 0.0);
    angles.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "selected": true },
            { "id": 2, "type": "video" },
            { "id": 1, "type": "audio", "selected": true },
        ]),
    );
    let cases = [
        (
            "a film with audio and subtitles",
            "{}",
            film(0.0, 0.0),
            vec![Track::Audio, Track::Subtitles],
        ),
        (
            "a film with two angles and no subtitles",
            "{}",
            angles,
            vec![Track::Audio, Track::Video],
        ),
        ("an album", r#"{"type":"music"}"#, film(0.0, 0.0), vec![]),
        ("a file with no tracks", "{}", Film::default(), vec![]),
    ];
    for (name, presentation, film, stops) in cases {
        assert_eq!(Strip::present(&block(presentation), &film), stops, "{name}");
    }
}

/// Each stop names the track that plays, and the delay when it is not
/// zero. With the subtitles off, the stop reads Off and no delay.
#[test]
fn a_stop_names_the_playing_track_and_its_delay() {
    let read = |film: &Film| -> Vec<(String, Option<String>)> {
        Strip::default()
            .view(&block("{}"), film, false)
            .into_iter()
            .map(|stop| (stop.values[0].text(), stop.delay))
            .collect()
    };
    let mut off = film(0.0, 0.7);
    off.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 1, "type": "sub", "lang": "eng" },
        ]),
    );
    let cases = [
        (
            "no delay",
            film(0.0, 0.0),
            vec![
                ("English E-AC3 5.1".to_string(), None),
                ("English".to_string(), None),
            ],
        ),
        (
            "both delays set",
            film(0.1, -0.7),
            vec![
                (
                    "English E-AC3 5.1".to_string(),
                    Some("0.10 s later".to_string()),
                ),
                ("English".to_string(), Some("0.7 s sooner".to_string())),
            ],
        ),
        (
            "subtitles off",
            off,
            vec![("English".to_string(), None), ("Off".to_string(), None)],
        ),
    ];
    for (name, film, want) in cases {
        assert_eq!(read(&film), want, "{name}");
    }
}

#[test]
fn left_and_right_move_across_the_stops_and_stop_at_the_ends() {
    let film = film(0.0, 0.0);
    let focused = |strip: &Strip| {
        strip
            .view(&block("{}"), &film, true)
            .iter()
            .position(|stop| stop.focused)
    };
    let mut strip = Strip::default();
    strip.press(Action::Left, &block("{}"), &film);
    assert_eq!(focused(&strip), Some(0));
    strip.press(Action::Right, &block("{}"), &film);
    strip.press(Action::Right, &block("{}"), &film);
    assert_eq!(focused(&strip), Some(1));
    assert_eq!(
        Strip::default()
            .view(&block("{}"), &film, false)
            .iter()
            .filter(|stop| stop.focused)
            .count(),
        0
    );
}

/// The panel opens with the focus on the track that plays.
#[test]
fn a_panel_opens_on_the_track_that_plays() {
    let film = film(0.0, 0.0);
    let audio = open(0, &film);
    assert_eq!(audio.capturing(), Some(Track::Audio));
    assert_eq!(focus(&audio, &film), Some(PanelFocus::Track(Some(1))));
    let subtitles = open(1, &film);
    assert_eq!(subtitles.capturing(), Some(Track::Subtitles));
    assert_eq!(focus(&subtitles, &film), Some(PanelFocus::Track(Some(1))));
}

/// Each case opens the subtitle panel, presses the keys, and checks the
/// focus and the commands of the last press. The rows are the timing,
/// Off, English, and Spanish.
#[test]
fn the_panel_moves_the_focus_on_up_and_down_and_the_delay_on_left_and_right() {
    let cases: [Moves; 7] = [
        (
            "up reaches the timing row",
            &[Action::Up, Action::Up],
            PanelFocus::Timing,
            vec![],
        ),
        (
            "up stops at the timing row",
            &[Action::Up, Action::Up, Action::Up],
            PanelFocus::Timing,
            vec![],
        ),
        (
            "down reaches the last track",
            &[Action::Down],
            PanelFocus::Track(Some(2)),
            vec![],
        ),
        (
            "down stops at the last track",
            &[Action::Down, Action::Down],
            PanelFocus::Track(Some(2)),
            vec![],
        ),
        (
            "left on a track shows the subtitles sooner",
            &[Action::Left],
            PanelFocus::Track(Some(1)),
            set("sub-delay", json!(-0.1)),
        ),
        (
            "right on the timing row shows them later",
            &[Action::Up, Action::Up, Action::Right],
            PanelFocus::Timing,
            set("sub-delay", json!(0.1)),
        ),
        (
            "down from the timing row reaches Off",
            &[Action::Up, Action::Up, Action::Down],
            PanelFocus::Track(None),
            vec![],
        ),
    ];
    for (name, presses, want_focus, want_commands) in cases {
        let film = film(0.0, 0.0);
        let mut strip = open(1, &film);
        let mut commands = Vec::new();
        for press in presses {
            commands = strip.handle(*press, &block("{}"), &film, 0);
        }
        assert_eq!(focus(&strip, &film), Some(want_focus), "{name}");
        assert_eq!(commands, want_commands, "{name}");
    }
}

/// A select on another track plays it and leaves the panel open, and a
/// select on the track that plays closes the panel.
#[test]
fn a_select_plays_a_track_and_a_select_on_the_playing_track_closes() {
    let cases: [Selects; 3] = [
        (
            "Spanish",
            &[Action::Down, Action::Select],
            set("sid", json!("2")),
            Some(Track::Subtitles),
        ),
        (
            "Off",
            &[Action::Up, Action::Select],
            set("sid", json!("no")),
            Some(Track::Subtitles),
        ),
        ("the English that play", &[Action::Select], vec![], None),
    ];
    for (name, presses, want, capturing) in cases {
        let film = film(0.0, 0.0);
        let mut strip = open(1, &film);
        let mut commands = Vec::new();
        for press in presses {
            commands = strip.handle(*press, &block("{}"), &film, 0);
        }
        assert_eq!(commands, want, "a select on {name}");
        assert_eq!(strip.capturing(), capturing, "after a select on {name}");
    }
}

/// A select on the timing row compares: the delay goes to zero, the hint
/// names the way back, and the next select sets the delay back.
#[test]
fn a_select_on_the_timing_row_compares_with_the_files_timing() {
    let mut strip = open(1, &film(0.0, -0.7));
    strip.handle(Action::Up, &block("{}"), &film(0.0, -0.7), 0);
    strip.handle(Action::Up, &block("{}"), &film(0.0, -0.7), 0);
    assert_eq!(
        strip.handle(Action::Select, &block("{}"), &film(0.0, -0.7), 0),
        set("sub-delay", json!(0.0))
    );
    let timing = strip
        .panel(&block("{}"), &film(0.0, 0.0))
        .and_then(|view| view.timing);
    assert_eq!(
        timing.as_ref().map(|timing| (
            timing.reading.as_str(),
            timing.hint.as_deref(),
            timing.returns_to
        )),
        Some((
            "Subtitles as timed in the file",
            Some("Select returns to 0.7 s sooner"),
            Some(crate::timing::Delay::Subtitles.share(-0.7))
        ))
    );
    assert_eq!(
        strip.handle(Action::Select, &block("{}"), &film(0.0, 0.0), 0),
        set("sub-delay", json!(-0.7))
    );
}

/// The rows sort again when another language plays, and the focus stays
/// on the track it was on.
#[test]
fn the_focus_follows_its_track_when_the_rows_sort_again() {
    let mut strip = open(1, &film(0.0, 0.0));
    strip.handle(Action::Down, &block("{}"), &film(0.0, 0.0), 0);
    strip.handle(Action::Select, &block("{}"), &film(0.0, 0.0), 0);
    let view = strip
        .panel(&block("{}"), &spanish())
        .expect("an open panel");
    let ids: Vec<Option<i64>> = view.rows.iter().map(|row| row.id).collect();
    assert_eq!(ids, vec![None, Some(2), Some(1)]);
    assert_eq!(view.focus, PanelFocus::Track(Some(2)));
    assert_eq!(view.focused_row(), Some(1));
}

/// A track that leaves the list gives the focus to the track that plays.
#[test]
fn a_focus_on_a_track_that_left_moves_to_the_track_that_plays() {
    let mut strip = open(1, &film(0.0, 0.0));
    strip.handle(Action::Down, &block("{}"), &film(0.0, 0.0), 0);
    let mut fewer = film(0.0, 0.0);
    fewer.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 1, "type": "sub", "lang": "eng", "selected": true },
        ]),
    );
    assert_eq!(focus(&strip, &fewer), Some(PanelFocus::Track(Some(1))));
    strip.handle(Action::Up, &block("{}"), &fewer, 0);
    assert_eq!(focus(&strip, &fewer), Some(PanelFocus::Track(None)));
}

/// A video panel and a photo's panels have no timing row, so up stops at
/// the first track and left and right change nothing.
#[test]
fn a_panel_with_no_delay_has_no_timing_row() {
    let mut angles = film(0.0, 0.0);
    angles.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "video", "selected": true },
            { "id": 2, "type": "video" },
            { "id": 1, "type": "audio", "selected": true },
        ]),
    );
    let mut video = open(1, &angles);
    assert_eq!(video.capturing(), Some(Track::Video));
    video.handle(Action::Up, &block("{}"), &angles, 0);
    assert_eq!(focus(&video, &angles), Some(PanelFocus::Track(Some(1))));
    assert!(
        video
            .handle(Action::Right, &block("{}"), &angles, 0)
            .is_empty()
    );
    assert!(
        video
            .handle(Action::Select, &block("{}"), &angles, 0)
            .is_empty()
    );
    assert_eq!(
        video.capturing(),
        None,
        "a select on the playing angle closes the panel"
    );

    let photo = block(r#"{"type":"image"}"#);
    let mut audio = Strip::default();
    audio.press(Action::Select, &photo, &angles);
    assert_eq!(
        audio.panel(&photo, &angles).and_then(|view| view.timing),
        None
    );
}

#[test]
fn back_and_a_closed_strip_change_nothing() {
    let film = film(0.0, 0.0);
    let mut strip = open(1, &film);
    assert!(
        strip
            .handle(Action::Back, &block("{}"), &film, 0)
            .is_empty()
    );
    strip.close();
    assert_eq!(strip.panel(&block("{}"), &film), None);
    assert_eq!(strip.panel_top(&block("{}"), &film), None);
    assert!(
        strip
            .handle(Action::Left, &block("{}"), &film, 0)
            .is_empty()
    );
    assert!(
        strip
            .press(Action::Up, &block("{}"), &Film::default())
            .is_empty()
    );
}

/// When the next file has no track of the open panel's kind, the panel
/// closes, because it has nothing to list.
#[test]
fn a_panel_whose_kind_leaves_the_file_closes() {
    let mut strip = open(0, &film(0.0, 0.0));
    let mut silent = Film::default();
    silent.apply(
        "track-list",
        &json!([{ "id": 1, "type": "sub", "lang": "eng", "selected": true }]),
    );
    assert!(
        !strip.on_tracks(&block("{}"), &film(0.0, 0.0)),
        "the panel closes while its kind is in the file"
    );
    assert!(
        strip.on_tracks(&block("{}"), &silent),
        "the panel stays open with no audio track, want it closed"
    );
    assert_eq!(strip.capturing(), None);
}

/// A press that keeps the drawn focus where it is starts no glide, even
/// when the focused track has left the list and the focus moved to the
/// track that plays.
#[test]
fn a_press_that_does_not_move_the_drawn_focus_starts_no_glide() {
    let mut strip = open(1, &film(0.0, 0.0));
    strip.handle(Action::Down, &block("{}"), &film(0.0, 0.0), 0);
    let mut fewer = film(0.0, 0.0);
    fewer.apply(
        "track-list",
        &json!([
            { "id": 1, "type": "audio", "lang": "eng", "selected": true },
            { "id": 1, "type": "sub", "lang": "eng", "selected": true },
        ]),
    );
    strip.handle(Action::Down, &block("{}"), &fewer, 100);
    assert!(
        !strip.moving(101),
        "a press on the last row starts a glide, want none"
    );
}
