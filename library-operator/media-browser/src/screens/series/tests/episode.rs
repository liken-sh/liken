// An episode's own row: select on a still opens it in the header, it
// offers the buttons the movie page offers a film, and back closes it.

use super::super::*;
use super::serials::{SERIES, Serials};
use crate::catalog::Progress;

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
                finished: crate::catalog::progress::finished(position, RUNTIME),
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

#[test]
fn select_on_a_still_opens_its_row_with_focus_on_the_first_button() {
    let (mut page, mut source) = stopped_at(Some(690));

    assert!(matches!(page.key("enter", &mut source), Step::Stay));

    assert_eq!(page.focus, Focus::Episode(0, 0));
    assert_eq!(page.focused().map(|still| still.episode), Some(1));
    assert_eq!(
        words(&page),
        ["Resume", "Start over", "Mark watched", "Clear progress"]
    );
}

#[test]
fn the_row_of_an_episode_they_have_not_started_offers_play_and_the_watched_mark() {
    let (mut page, mut source) = stopped_at(None);
    page.key("enter", &mut source);

    assert_eq!(words(&page), ["Play", "Mark watched"]);
}

#[test]
fn the_row_of_an_episode_they_finished_offers_play_and_the_clear() {
    let (mut page, mut source) = stopped_at(Some(RUNTIME));
    page.focus = Focus::Still(0);
    page.key("enter", &mut source);

    assert_eq!(words(&page), ["Play", "Clear progress"]);
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
fn down_returns_to_the_still_and_up_reaches_the_strip() {
    let (mut page, mut source) = stopped_at(Some(690));
    page.key("enter", &mut source);

    assert!(matches!(page.key("up", &mut source), Step::Still));
    assert_eq!(page.focus, Focus::Episode(0, 0));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Still(0));
}

// What the button this many presses right of the first one asks for.
fn pressed(page: &mut Series, source: &mut Serials, right: usize) -> Step {
    page.key("enter", source);
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
    } = pressed(&mut page, &mut source, 2)
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

    let Step::Mark { mark, .. } = pressed(&mut page, &mut source, 3) else {
        panic!("Clear progress marks the episode");
    };

    assert_eq!(mark, TitleMark::Cleared);
}

#[test]
fn a_mark_on_an_episode_no_play_names_takes_the_catalogs_running_time() {
    let (mut page, mut source) = stopped_at(None);

    let Step::Mark { duration, .. } = pressed(&mut page, &mut source, 1) else {
        panic!("Mark watched marks the episode");
    };

    assert_eq!(duration, page.stills[0].duration);
    assert!(duration > 0);
}
