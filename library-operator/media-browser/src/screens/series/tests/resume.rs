// The episode wall over a series the audience already played: the bar
// under every still a play names, the episode the wall opens on, and the
// second a press on a still starts the play at.

use super::super::*;
use super::serials::{SERIES, Serials};
use crate::catalog::Progress;
use crate::screens::InFranchise;
use crate::views::Card;

// The one `Person` these cases record their plays against. The name is
// invented; a test names no person of a cluster.
const WATCHER: &str = "someone";

// How long every episode of the invented series is.
const RUNTIME: i64 = 2_760;

// One play of one episode: the aired numbers, the second it was recorded,
// and how far it reached.
fn played(season: i64, episode: i64, recorded: i64, position: i64) -> Progress {
    Progress {
        position,
        duration: RUNTIME,
        finished: crate::catalog::progress::finished(position, RUNTIME),
        recorded,
        season,
        episode,
        ..Progress::default()
    }
}

// The page of the invented series after the browser read these plays of
// it.
fn watched(progress: Vec<Progress>) -> (Series, Serials) {
    let mut source = Serials {
        progress,
        ..Serials::default()
    };
    let mut page =
        Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it");
    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);
    (page, source)
}

// The share of each still's bar, in aired order.
fn bars(page: &Series) -> Vec<Option<f32>> {
    page.stills.iter().map(|still| still.watched()).collect()
}

#[test]
fn every_episode_a_play_names_carries_a_bar_and_no_other_does() {
    let (page, source) = watched(vec![played(1, 1, 100, RUNTIME), played(1, 2, 200, 690)]);

    assert_eq!(bars(&page)[..3], [Some(1.0), Some(0.25), None]);
    assert_eq!(source.watching, [WATCHER.to_string()]);
}

#[test]
fn the_wall_opens_on_the_episode_the_latest_play_stopped_in() {
    let (page, _) = watched(vec![played(1, 1, 100, RUNTIME), played(1, 2, 200, 690)]);

    assert_eq!(page.focus, Focus::Still(1));
}

#[test]
fn the_wall_opens_on_the_episode_after_the_one_they_finished() {
    let (page, _) = watched(vec![played(1, 5, 100, RUNTIME)]);

    assert_eq!(page.focus, Focus::Still(5));
    assert_eq!(page.focused().map(|still| still.season), Some(2));
}

#[test]
fn a_series_no_play_names_opens_on_its_first_episode() {
    let (page, _) = watched(Vec::new());

    assert_eq!(page.focus, Focus::Still(0));
}

#[test]
fn a_page_opened_on_an_episode_keeps_it_whatever_the_plays_name() {
    let mut source = Serials {
        progress: vec![played(1, 2, 200, 690)],
        ..Serials::default()
    };
    let mut page = Series::open_at("screening/serials", SERIES, (2, 3), &mut source)
        .expect("the catalog holds it");

    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);

    assert_eq!(page.focus, Focus::Still(7));
    assert_eq!(bars(&page)[1], Some(0.25));
}

#[test]
fn a_press_on_an_episode_they_are_in_the_middle_of_plays_from_there() {
    let (mut page, mut source) = watched(vec![played(1, 2, 200, 690)]);

    let Step::Play {
        selection, start, ..
    } = page.key("enter", &mut source)
    else {
        panic!("a select on a still plays it");
    };

    assert_eq!(
        selection,
        Selection::Episode {
            series: SERIES.into(),
            season: 1,
            episode: 2,
        }
    );
    assert_eq!(start, Some(690));
}

#[test]
fn a_press_on_every_other_episode_plays_from_the_beginning() {
    let (mut page, mut source) = watched(vec![played(1, 1, 100, RUNTIME)]);

    let Step::Play { start, .. } = page.key("enter", &mut source) else {
        panic!("a select on a still plays it");
    };

    assert_eq!(page.focus, Focus::Still(1));
    assert_eq!(start, None);
}

#[test]
fn a_reread_holds_the_focus_the_wall_already_had() {
    let (mut page, mut source) = watched(vec![played(1, 2, 200, 690)]);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Still(2));

    page.reread(&mut source);

    assert_eq!(page.focus, Focus::Still(2));
}

// The page of a series of four seasons of five, opened as the franchise
// member these runs cut out of it, after the browser read these plays.
fn inside(runs: Vec<(i64, i64)>, progress: Vec<Progress>) -> Series {
    let mut source = Serials {
        seasons: vec![5, 5, 5, 5],
        progress,
        ..Serials::default()
    };
    let mut page =
        Series::open("screening/serials", SERIES, &mut source).expect("the catalog holds it");
    page.via = Some(InFranchise {
        library: "screening/orders".into(),
        id: "franchise:name:the-cycle".into(),
        position: 2,
        runs,
    });
    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);
    page
}

// The aired numbers of the episode focus is on.
fn landed(page: &Series) -> Option<(i64, i64)> {
    page.focused().map(|still| (still.season, still.episode))
}

#[test]
fn a_page_opened_inside_a_run_opens_on_the_first_episode_of_the_run() {
    let page = inside(vec![(3, 0), (4, 0)], Vec::new());

    assert_eq!(page.focus, Focus::Still(10));
    assert_eq!(landed(&page), Some((3, 1)));
}

#[test]
fn a_page_opened_inside_a_run_opens_after_the_episode_they_finished() {
    let page = inside(vec![(3, 0), (4, 0)], vec![played(3, 1, 100, RUNTIME)]);

    assert_eq!(landed(&page), Some((3, 2)));
}

#[test]
fn a_play_outside_the_run_leaves_the_page_on_the_first_episode_of_the_run() {
    let page = inside(vec![(3, 0), (4, 0)], vec![played(1, 5, 100, 690)]);

    assert_eq!(landed(&page), Some((3, 1)));
}

#[test]
fn a_run_the_catalog_holds_no_episode_of_opens_on_the_first_still() {
    let page = inside(vec![(9, 0)], Vec::new());

    assert_eq!(page.focus, Focus::Still(0));
    assert_eq!(landed(&page), Some((1, 1)));
}

#[test]
fn a_run_of_two_seasons_apart_opens_on_the_first_episode_of_the_later_one() {
    let page = inside(vec![(2, 0), (4, 0)], vec![played(2, 5, 100, RUNTIME)]);

    assert_eq!(landed(&page), Some((4, 1)));
}
