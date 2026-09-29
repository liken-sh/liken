// An episode's own row: select on a still opens it in the header, it
// offers the buttons and the marks the movie page offers a film, and back
// closes it.

use super::super::*;
use super::serials::{SERIES, Serials};
use crate::catalog::Progress;
use crate::screens::TitleMark;

// The one `Person` these cases record their plays against. The name is
// invented; a test names no person of a cluster.
const WATCHER: &str = "someone";

// How long every episode of the invented series is.
const RUNTIME: i64 = 2_760;

// The page with its focus on the first still, after the audience stopped
// at this second of the first episode, or with no play of theirs.
fn stopped_at(position: Option<i64>) -> (Series, Serials) {
    let mut source = Serials {
        progress: position
            .map(|position| Progress {
                position,
                duration: RUNTIME,
                finished: crate::catalog::progress::finished(position, RUNTIME, &[]),
                recorded: 100,
                season: 1,
                episode: 1,
                ..Progress::default()
            })
            .into_iter()
            .collect(),
        ..Serials::default()
    };
    let mut page = Series::open_at("screening/serials", SERIES, (1, 1), &mut source)
        .expect("the catalog holds it");
    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);
    (page, source)
}

fn words(page: &Series) -> Vec<&'static str> {
    page.buttons().iter().map(|button| button.word()).collect()
}

fn marks(page: &Series) -> Vec<&'static str> {
    page.marks().iter().map(|mark| mark.word()).collect()
}

#[test]
fn select_on_a_still_opens_its_row_with_focus_on_the_first_button() {
    let (mut page, mut source) = stopped_at(Some(690));

    assert!(matches!(page.key("enter", &mut source), Step::Stay));

    assert_eq!(page.focus, Focus::Episode(0, 0));
    assert_eq!(page.focused().map(|still| still.episode), Some(1));
    assert_eq!(words(&page), ["Resume", "Start over"]);
    assert_eq!(marks(&page), ["Mark watched", "Clear progress"]);
}

#[test]
fn the_row_of_an_episode_they_have_not_started_offers_play_and_the_watched_mark() {
    let (mut page, mut source) = stopped_at(None);
    page.key("enter", &mut source);

    assert_eq!(words(&page), ["Play"]);
    assert_eq!(marks(&page), ["Mark watched"]);
}

#[test]
fn the_row_of_an_episode_they_finished_offers_play_and_the_clear() {
    let (mut page, mut source) = stopped_at(Some(RUNTIME));
    page.focus = Focus::Still(0);
    page.key("enter", &mut source);

    assert_eq!(words(&page), ["Play"]);
    assert_eq!(marks(&page), ["Clear progress"]);
}

#[test]
fn back_closes_the_row_and_returns_to_the_still() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.key("enter", &mut source);

    assert!(matches!(page.escape(), Some(Step::Stay)));

    assert_eq!(page.focus, Focus::Still(0));
    assert!(page.escape().is_none());
}

#[test]
fn up_from_the_row_reaches_the_strip() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.key("enter", &mut source);

    assert!(matches!(page.key("up", &mut source), Step::Still));
    assert_eq!(page.focus, Focus::Episode(0, 0));
}

// Press these keys in order, and answer where focus stands after the last.
fn after(page: &mut Series, source: &mut Serials, keys: &[&str]) -> Focus {
    for key in keys {
        page.key(key, source);
    }
    page.focus
}

#[test]
fn down_from_any_button_of_the_row_focuses_the_first_mark() {
    let (mut page, mut source) = stopped_at(Some(690));

    assert_eq!(
        after(&mut page, &mut source, &["enter", "right", "down"]),
        Focus::EpisodeMark(0, 0)
    );
    assert_eq!(page.focused().map(|still| still.episode), Some(1));
}

#[test]
fn left_and_right_move_between_the_marks_and_stop_at_the_ends() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(0, 0);

    assert_eq!(
        after(&mut page, &mut source, &["right"]),
        Focus::EpisodeMark(0, 1)
    );
    assert_eq!(
        after(&mut page, &mut source, &["right"]),
        Focus::EpisodeMark(0, 1)
    );
    assert_eq!(
        after(&mut page, &mut source, &["left", "left"]),
        Focus::EpisodeMark(0, 0)
    );
}

#[test]
fn up_from_a_mark_returns_to_the_row() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(0, 1);

    assert!(matches!(page.key("up", &mut source), Step::Stay));
    assert_eq!(page.focus, Focus::Episode(0, 0));
}

#[test]
fn down_from_a_mark_returns_to_the_still() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(0, 1);

    assert_eq!(after(&mut page, &mut source, &["down"]), Focus::Still(0));
}

#[test]
fn back_over_a_mark_closes_the_row() {
    let (mut page, _) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(0, 0);

    assert!(matches!(page.escape(), Some(Step::Stay)));
    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn the_status_under_the_row_reads_where_the_audience_stands() {
    let cases = [
        (None, "Not started · 46m"),
        (Some(690), "11:30 watched · 34m remaining"),
        (Some(RUNTIME), "Watched"),
    ];
    for (position, words) in cases {
        let (page, _) = stopped_at(position);
        assert_eq!(page.stills[0].status().words, words);
    }
}

#[test]
fn a_progress_read_that_drops_a_mark_keeps_focus_on_a_mark_the_line_holds() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(0, 1);
    source.progress[0].position = RUNTIME;
    source.progress[0].finished = true;

    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);

    assert_eq!(marks(&page), ["Clear progress"]);
    assert_eq!(page.focus, Focus::EpisodeMark(0, 0));
}

#[test]
fn a_progress_read_that_shortens_the_row_keeps_focus_on_a_button_it_holds() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::Episode(0, 1);
    source.progress[0].position = RUNTIME;
    source.progress[0].finished = true;

    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);

    assert_eq!(words(&page), ["Play"]);
    assert_eq!(page.focus, Focus::Episode(0, 0));
}

#[test]
fn the_wall_offers_no_marks_until_a_row_is_open() {
    let (page, _) = stopped_at(Some(690));

    assert_eq!(page.focus, Focus::Still(0));
    assert!(page.marks().is_empty());
}

#[test]
fn a_mark_on_a_still_the_wall_no_longer_holds_returns_to_the_first_still() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.focus = Focus::EpisodeMark(99, 0);

    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn a_select_past_the_last_mark_marks_nothing() {
    let (mut page, mut source) = stopped_at(None);
    page.focus = Focus::EpisodeMark(0, 5);

    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    assert_eq!(page.focus, Focus::EpisodeMark(0, 5));
}

// What the mark this many presses right of the first one asks for.
fn pressed(page: &mut Series, source: &mut Serials, right: usize) -> Step {
    page.key("enter", source);
    page.key("down", source);
    for _ in 0..right {
        page.key("right", source);
    }
    page.key("enter", source)
}

#[test]
fn mark_watched_marks_the_episode_for_its_series_and_returns_to_the_still() {
    let (mut page, mut source) = stopped_at(Some(690));

    let Step::Mark {
        library,
        selection,
        mark,
        duration,
    } = pressed(&mut page, &mut source, 0)
    else {
        panic!("Mark watched marks the episode");
    };

    assert_eq!(library, "screening/serials");
    assert_eq!(
        selection,
        Selection::Episode {
            series: SERIES.into(),
            season: 1,
            episode: 1,
        }
    );
    assert_eq!(mark, TitleMark::Watched);
    assert_eq!(duration, RUNTIME);
    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn clear_progress_clears_the_episode() {
    let (mut page, mut source) = stopped_at(Some(690));

    let Step::Mark { mark, .. } = pressed(&mut page, &mut source, 1) else {
        panic!("Clear progress marks the episode");
    };

    assert_eq!(mark, TitleMark::Cleared);
}

#[test]
fn a_mark_on_an_episode_no_play_names_takes_the_catalogs_running_time() {
    let (mut page, mut source) = stopped_at(None);

    let Step::Mark { duration, .. } = pressed(&mut page, &mut source, 0) else {
        panic!("Mark watched marks the episode");
    };

    assert_eq!(duration, page.stills[0].duration);
    assert!(duration > 0);
}
