// A progress update for a series whose page is open, from the person's own
// mark or from a play on another screen: the page lands focus once, when
// it opens, and no later update moves it off what the person chose.

use super::super::*;
use super::serials::{SERIES, Serials};
use crate::catalog::Progress;

// The one `Person` these cases record their plays against. The name is
// invented; a test names no person of a cluster.
const WATCHER: &str = "someone";

fn people() -> Vec<String> {
    vec![WATCHER.to_string()]
}

// A play of the audience's in the middle of one episode.
fn midway(numbers: (i64, i64)) -> Progress {
    Progress {
        position: 900,
        duration: 2_760,
        recorded: 100,
        season: numbers.0,
        episode: numbers.1,
        ..Progress::default()
    }
}

// The same episode finished.
fn done(numbers: (i64, i64)) -> Progress {
    Progress {
        position: 2_700,
        finished: true,
        ..midway(numbers)
    }
}

// The page as the browser opens it: read, then its progress read once.
fn opened(source: Serials) -> (Series, Serials) {
    let mut source = source;
    let mut page =
        Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it");
    progress::read(&mut page, &mut source, &people());
    (page, source)
}

// The audience's plays change to these, and the browser reads the
// progress again.
fn update(page: &mut Series, source: &mut Serials, plays: Vec<Progress>) {
    source.progress = plays;
    progress::read(page, source, &people());
}

#[test]
fn a_fresh_page_lands_on_the_episode_to_watch_next() {
    let (page, _) = opened(Serials {
        progress: vec![done((1, 1)), done((1, 2))],
        ..Serials::default()
    });

    assert_eq!(page.focus, Focus::Still(2));
}

#[test]
fn an_update_keeps_an_open_row_and_the_mark_focus_is_on() {
    let (mut page, mut source) = opened(Serials {
        progress: vec![done((1, 1))],
        ..Serials::default()
    });
    page.focus = Focus::EpisodeMark(4, 0);

    update(&mut page, &mut source, vec![done((1, 1)), midway((1, 5))]);

    assert_eq!(page.focus, Focus::EpisodeMark(4, 0));
    assert_eq!(page.marks(), [watch::Mark::Watched, watch::Mark::Cleared]);
}

#[test]
fn an_update_keeps_the_button_by_what_it_does_when_the_row_gains_one() {
    let (mut page, mut source) = opened(Serials {
        progress: vec![midway((1, 5))],
        ..Serials::default()
    });
    page.focus = Focus::Episode(4, 1);
    assert_eq!(page.buttons()[1], row::Button::StartOver);

    update(&mut page, &mut source, vec![midway((1, 5))]);

    assert_eq!(page.focus, Focus::Episode(4, 1));
}

#[test]
fn an_update_that_removes_the_mark_moves_focus_to_a_mark_of_the_same_row() {
    let (mut page, mut source) = opened(Serials {
        progress: vec![midway((1, 5))],
        ..Serials::default()
    });
    page.focus = Focus::EpisodeMark(4, 0);
    assert_eq!(page.marks()[0], watch::Mark::Watched);

    update(&mut page, &mut source, vec![done((1, 5))]);

    assert_eq!(page.marks(), [watch::Mark::Cleared]);
    assert_eq!(page.focus, Focus::EpisodeMark(4, 0));
}

#[test]
fn an_update_that_removes_the_button_moves_focus_to_a_button_of_the_same_row() {
    let (mut page, mut source) = opened(Serials {
        progress: vec![midway((1, 5))],
        ..Serials::default()
    });
    page.focus = Focus::Episode(4, 1);

    update(&mut page, &mut source, Vec::new());

    assert_eq!(page.buttons(), [row::Button::Play, row::Button::PickUp]);
    assert_eq!(page.focus, Focus::Episode(4, 0));
}

#[test]
fn an_update_keeps_focus_on_a_still_deep_in_the_wall_and_the_scroll_with_it() {
    let (mut page, mut source) = opened(Serials {
        seasons: vec![8; 5],
        ..Serials::default()
    });
    page.focus = Focus::Still(26);

    update(&mut page, &mut source, vec![done((1, 1))]);

    assert_eq!(page.focus, Focus::Still(26));
}

#[test]
fn an_update_after_a_home_card_arrival_keeps_the_episode_s_row_open() {
    let mut source = Serials::default();
    let mut page = Series::open_at("screening/serials", SERIES, (1, 3), &mut source)
        .expect("the catalog holds it");
    page.arrive();
    progress::read(&mut page, &mut source, &people());
    assert_eq!(page.focus, Focus::Episode(2, 0));

    update(&mut page, &mut source, vec![midway((1, 3))]);

    assert_eq!(page.focus, Focus::Episode(2, 0));
    assert!(page.escape().is_none());
}
