// The movie page over a film the audience already played: the row of
// buttons each state draws, where focus lands, and the second a press on
// each button starts the play at.

use super::super::*;
use super::{Films, words};
use crate::catalog::Progress;

// The one `Person` these cases record their plays against. The name is
// invented; a test names no person of a cluster.
const WATCHER: &str = "someone";

// How far into the film the audience reached, and how long the film is.
const REACHED: i64 = 2_912;
const RUNTIME: i64 = 6_720;

// A page of a film this audience played, with a trailer file, after the
// browser read the progress of it.
fn watched(progress: Option<Progress>) -> (Movie, Films) {
    let mut source = Films {
        trailer: true,
        progress,
        ..Films::default()
    };
    let mut page =
        Movie::open("screening/films", "two", &mut source).expect("the catalog holds this movie");
    page.read_progress(&mut source, &[WATCHER.to_string()]);
    (page, source)
}

// A play that stopped in the middle of the film, or one that reached the
// end of it.
fn progress(finished: bool) -> Option<Progress> {
    Some(Progress {
        position: REACHED,
        duration: RUNTIME,
        finished,
        ..Progress::default()
    })
}

#[test]
fn a_film_no_play_names_draws_the_row_it_always_drew() {
    let (page, source) = watched(None);

    assert_eq!(words(&page), ["Play", "Trailer"]);
    assert_eq!(page.progress, None);
    assert_eq!(source.watching, [WATCHER.to_string()]);
}

#[test]
fn a_film_the_audience_is_in_the_middle_of_leads_with_resume() {
    let (page, _) = watched(progress(false));

    assert_eq!(words(&page), ["Resume", "Start over", "Trailer"]);
    assert_eq!(page.focus, Focus::Buttons(0));
}

#[test]
fn a_film_the_audience_finished_leads_with_play() {
    let (page, _) = watched(progress(true));

    assert_eq!(words(&page), ["Play", "Trailer"]);
    assert_eq!(page.focus, Focus::Buttons(0));
}

// What a press on the focused button asks the browser to play: the choice
// and the second it starts at.
fn played(page: &mut Movie, source: &mut Films) -> (Selection, Option<i64>) {
    let Step::Play {
        selection, start, ..
    } = page.key("enter", source)
    else {
        panic!("a press on a button of this row plays something");
    };
    (selection, start)
}

#[test]
fn resume_plays_from_the_second_the_audience_reached() {
    let (mut page, mut source) = watched(progress(false));

    let (selection, start) = played(&mut page, &mut source);

    assert_eq!(selection, Selection::Movie { id: "two".into() });
    assert_eq!(start, Some(REACHED));
}

#[test]
fn start_over_plays_from_the_beginning() {
    let (mut page, mut source) = watched(progress(false));
    page.key("right", &mut source);

    let (selection, start) = played(&mut page, &mut source);

    assert_eq!(selection, Selection::Movie { id: "two".into() });
    assert_eq!(start, None);
}

#[test]
fn the_trailer_button_stands_after_both_and_plays_the_trailer() {
    let (mut page, mut source) = watched(progress(false));
    page.key("right", &mut source);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Buttons(2));

    let (selection, start) = played(&mut page, &mut source);

    assert_eq!(selection, Selection::Trailer { id: "two".into() });
    assert_eq!(start, None);
}

#[test]
fn a_film_the_audience_finished_plays_from_the_beginning() {
    let (mut page, mut source) = watched(progress(true));

    let (selection, start) = played(&mut page, &mut source);

    assert_eq!(selection, Selection::Movie { id: "two".into() });
    assert_eq!(start, None);
}

#[test]
fn a_progress_read_that_shortens_the_row_keeps_focus_on_a_button_it_holds() {
    let (mut page, mut source) = watched(progress(false));
    page.key("right", &mut source);
    page.key("right", &mut source);
    source.progress = progress(true);

    page.read_progress(&mut source, &[WATCHER.to_string()]);

    assert_eq!(words(&page), ["Play", "Trailer"]);
    assert_eq!(page.focus, Focus::Buttons(1));
}

#[test]
fn a_reread_holds_focus_on_the_button_the_row_already_drew() {
    let (mut page, mut source) = watched(progress(false));
    page.key("right", &mut source);
    page.key("right", &mut source);

    page.reread(&mut source);

    assert_eq!(words(&page), ["Resume", "Start over", "Trailer"]);
    assert_eq!(page.focus, Focus::Buttons(2));
}
