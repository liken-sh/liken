// The status line under a movie's playback row: the status it reads, the
// marks it offers, and how focus moves between the row, the marks, and
// the blocks under them.

use super::super::watch::Mark;
use super::super::*;
use super::resume::{own, progress, reached, watched};
use super::{Films, page};

// The words of the marks the page offers.
fn marks(page: &Movie) -> Vec<&'static str> {
    page.marks().iter().map(|mark| mark.word()).collect()
}

// Press these keys in order, and answer where focus stands after the last.
fn after(page: &mut Movie, source: &mut Films, keys: &[&str]) -> Focus {
    for key in keys {
        page.key(key, source);
    }
    page.focus
}

#[test]
fn down_from_any_playback_button_focuses_the_first_mark() {
    let (mut page, mut source) = watched(progress(false));

    assert_eq!(after(&mut page, &mut source, &["down"]), Focus::Marks(0));
    page.focus = Focus::Buttons(2);
    assert_eq!(after(&mut page, &mut source, &["down"]), Focus::Marks(0));
}

#[test]
fn left_and_right_move_between_the_marks_and_stop_at_the_ends() {
    let (mut page, mut source) = watched(progress(false));
    page.focus = Focus::Marks(0);

    assert_eq!(after(&mut page, &mut source, &["right"]), Focus::Marks(1));
    assert_eq!(after(&mut page, &mut source, &["right"]), Focus::Marks(1));
    assert_eq!(after(&mut page, &mut source, &["left"]), Focus::Marks(0));
    assert_eq!(after(&mut page, &mut source, &["left"]), Focus::Marks(0));
}

#[test]
fn up_from_a_mark_returns_to_the_playback_row() {
    let (mut page, mut source) = watched(progress(false));
    page.focus = Focus::Marks(1);

    assert!(matches!(page.key("up", &mut source), Step::Stay));
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn down_from_the_marks_reaches_the_set_strip_and_up_returns_to_the_marks() {
    let (mut page, mut source) = page(Films {
        set: true,
        ..Films::default()
    });

    assert_eq!(
        after(&mut page, &mut source, &["down", "down"]),
        Focus::Strip(1)
    );
    assert_eq!(after(&mut page, &mut source, &["up"]), Focus::Marks(0));
}

#[test]
fn down_from_the_marks_reaches_the_cast_and_up_returns_to_the_marks() {
    let (mut page, mut source) = page(Films {
        credits: true,
        ..Films::default()
    });

    assert_eq!(
        after(&mut page, &mut source, &["down", "down"]),
        Focus::Stripe(0, 0)
    );
    assert_eq!(after(&mut page, &mut source, &["up"]), Focus::Marks(0));
}

#[test]
fn down_from_the_marks_with_nothing_under_them_stays_on_the_mark() {
    let (mut page, mut source) = page(Films::default());

    assert_eq!(
        after(&mut page, &mut source, &["down", "down"]),
        Focus::Marks(0)
    );
}

#[test]
fn the_marks_follow_where_the_audience_stands() {
    let cases = [
        (None, vec!["Mark watched"]),
        (progress(false), vec!["Mark watched", "Clear progress"]),
        (progress(true), vec!["Clear progress"]),
    ];
    for (progress, words) in cases {
        let (page, _) = watched(progress);
        assert_eq!(marks(&page), words);
    }
}

#[test]
fn the_status_reads_the_catalogs_running_time_on_a_film_no_play_names() {
    let (page, _) = watched(None);

    assert_eq!(page.status().words, "Not started · 1h 52m");
}

#[test]
fn the_status_reads_the_second_the_audience_reached() {
    let (page, _) = watched(progress(false));

    assert_eq!(page.status().words, "0:48:32 watched · 1h 3m remaining");
}

#[test]
fn the_status_of_a_finished_film_reads_watched_with_its_check() {
    let (page, _) = watched(progress(true));

    let status = page.status();
    assert_eq!(status.words, "Watched");
    assert!(status.finished);
}

// What a press on the focused mark sends: the choice, the mark, and the
// duration it states.
fn marked(page: &mut Movie, source: &mut Films) -> (Selection, TitleMark, i64) {
    let Step::Mark {
        selection,
        mark,
        duration,
        ..
    } = page.key("enter", source)
    else {
        panic!("a press on a mark marks the film");
    };
    (selection, mark, duration)
}

#[test]
fn mark_watched_marks_the_film_at_the_duration_of_the_audiences_play() {
    let (mut page, mut source) = watched(progress(false));
    page.focus = Focus::Marks(0);

    let (selection, mark, duration) = marked(&mut page, &mut source);

    assert_eq!(selection, Selection::Movie { id: "two".into() });
    assert_eq!(mark, TitleMark::Watched);
    assert_eq!(duration, 6_720);
}

#[test]
fn clear_progress_clears_the_film() {
    let (mut page, mut source) = watched(progress(false));
    page.focus = Focus::Marks(1);

    let (_, mark, _) = marked(&mut page, &mut source);

    assert_eq!(mark, TitleMark::Cleared);
}

#[test]
fn a_mark_on_a_film_no_play_names_takes_the_catalogs_running_time() {
    let (mut page, mut source) = watched(None);
    page.focus = Focus::Marks(0);

    let (_, mark, duration) = marked(&mut page, &mut source);

    assert_eq!(mark, TitleMark::Watched);
    assert_eq!(duration, page.duration);
    assert!(duration > 0);
}

#[test]
fn a_progress_read_that_drops_a_mark_keeps_focus_on_a_mark_it_holds() {
    let (mut page, mut source) = watched(progress(false));
    page.focus = Focus::Marks(1);
    source.plays = vec![own(reached(6_720, true, 20), true)];

    page.read_progress(&mut source, &["someone".to_string()]);

    assert_eq!(page.marks(), [Mark::Cleared]);
    assert_eq!(page.focus, Focus::Marks(0));
}

#[test]
fn a_select_past_the_last_mark_marks_nothing() {
    let (mut page, mut source) = watched(None);
    page.focus = Focus::Marks(5);

    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}
