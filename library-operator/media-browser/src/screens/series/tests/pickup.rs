// Pick up here on an episode's row: where the row offers it, which
// episodes a press marks watched and which it clears, and that the press
// plays the episode the way Play does.

use super::super::*;
use super::serials::{SERIES, Serials};
use crate::bus::mark::{self, TitleMark};
use crate::catalog::Progress;
use crate::catalog::Selection;
use crate::screens::upnext;

// The one `Person` these cases record their plays against. The name is
// invented; a test names no person of a cluster.
const WATCHER: &str = "someone";

// How long every episode of the invented series is.
const RUNTIME: i64 = 2_760;

// A play of the audience's that reached this second of one episode, out of
// this many seconds.
fn reached(numbers: (i64, i64), position: i64, duration: i64) -> Progress {
    Progress {
        position,
        duration,
        finished: crate::catalog::progress::finished(position, duration, &[]),
        recorded: 100,
        season: numbers.0,
        episode: numbers.1,
        ..Progress::default()
    }
}

// An episode the audience finished.
fn finished(numbers: (i64, i64)) -> Progress {
    reached(numbers, RUNTIME, RUNTIME)
}

// The page with the row of the episode these numbers name open, after the
// audience's plays in the source.
fn row_of(mut source: Serials, numbers: (i64, i64)) -> (Series, Serials) {
    let mut page = Series::open_at("screening/serials", SERIES, numbers, &mut source)
        .expect("the catalog holds it");
    progress::read(&mut page, &mut source, &[WATCHER.to_string()]);
    let Focus::Still(index) = page.focus else {
        panic!("the page opens on a still");
    };
    page.focus = Focus::Episode(index, 0);
    (page, source)
}

fn words(page: &Series) -> Vec<&'static str> {
    page.buttons().iter().map(|button| button.word()).collect()
}

// The Serials fixture's three seasons of five, three, and six episodes,
// with two specials before them.
fn with_specials(progress: Vec<Progress>) -> Serials {
    Serials {
        specials: 2,
        progress,
        ..Serials::default()
    }
}

// An episode the audience cleared, which the store holds at position 0.
fn cleared(numbers: (i64, i64)) -> Progress {
    reached(numbers, 0, RUNTIME)
}

#[test]
fn the_row_offers_pick_up_here_only_where_the_press_would_change_an_episode() {
    let cases = [
        ("the first episode", vec![], (1, 1), vec!["Play"]),
        (
            "the third, none watched",
            vec![],
            (1, 3),
            vec!["Play", "Pick up here"],
        ),
        (
            "the third, the second partly watched",
            vec![finished((1, 1)), reached((1, 2), 690, RUNTIME)],
            (1, 3),
            vec!["Play", "Pick up here"],
        ),
        (
            "the third, every earlier one finished",
            vec![finished((1, 1)), finished((1, 2))],
            (1, 3),
            vec!["Play"],
        ),
        (
            "the second season's first, the first season finished",
            (1..=5).map(|episode| finished((1, episode))).collect(),
            (2, 1),
            vec!["Play"],
        ),
        (
            "the second season's first, one of the first not finished",
            (1..=4).map(|episode| finished((1, episode))).collect(),
            (2, 1),
            vec!["Play", "Pick up here"],
        ),
        (
            "an episode they are in the middle of",
            vec![reached((1, 3), 690, RUNTIME)],
            (1, 3),
            vec!["Resume", "Start over", "Pick up here"],
        ),
        (
            "the first episode, a later one finished",
            vec![finished((3, 2))],
            (1, 1),
            vec!["Play", "Pick up here"],
        ),
        (
            "the first episode, a later one partly watched",
            vec![reached((2, 1), 690, RUNTIME)],
            (1, 1),
            vec!["Play", "Pick up here"],
        ),
        (
            "the first episode, a later one cleared",
            vec![cleared((2, 1))],
            (1, 1),
            vec!["Play"],
        ),
        (
            "the last episode, every earlier one finished",
            (1..=5)
                .map(|episode| finished((1, episode)))
                .chain((1..=3).map(|episode| finished((2, episode))))
                .chain((1..=5).map(|episode| finished((3, episode))))
                .collect(),
            (3, 6),
            vec!["Play"],
        ),
        ("a special", vec![], (0, 2), vec!["Play"]),
        (
            "a special, a later one finished",
            vec![finished((1, 2))],
            (0, 1),
            vec!["Play"],
        ),
        (
            "the third, only a special started",
            vec![finished((1, 1)), finished((1, 2)), finished((0, 2))],
            (1, 3),
            vec!["Play"],
        ),
    ];
    for (name, progress, numbers, want) in cases {
        let (page, _) = row_of(with_specials(progress), numbers);
        assert_eq!(words(&page), want, "{name}");
    }
}

#[test]
fn an_earlier_episode_with_no_duration_is_not_one_the_press_can_mark() {
    let source = Serials {
        untimed: vec![(1, 1), (1, 2)],
        ..Serials::default()
    };
    let (page, _) = row_of(source, (1, 3));

    assert_eq!(words(&page), ["Play"]);
}

// Press the button of the open row that carries this word.
fn press(page: &mut Series, source: &mut Serials, word: &str) -> Step {
    let Focus::Episode(index, _) = page.focus else {
        panic!("the row is open");
    };
    let button = words(page)
        .iter()
        .position(|held| *held == word)
        .expect("the row holds the button");
    page.focus = Focus::Episode(index, button);
    page.key("enter", source)
}

fn listed(numbers: (i64, i64), duration: i64) -> mark::Episode {
    mark::Episode {
        season: numbers.0,
        episode: numbers.1,
        duration,
        mark: TitleMark::Watched,
    }
}

fn unlisted(numbers: (i64, i64), duration: i64) -> mark::Episode {
    mark::Episode {
        mark: TitleMark::Cleared,
        ..listed(numbers, duration)
    }
}

#[test]
fn a_press_marks_every_earlier_episode_across_seasons_they_have_not_finished() {
    let source = with_specials(vec![finished((1, 1)), reached((1, 2), 690, 2_700)]);
    let (mut page, mut source) = row_of(source, (2, 2));

    let Step::PickUp {
        library,
        series,
        episodes,
        ..
    } = press(&mut page, &mut source, "Pick up here")
    else {
        panic!("the press asks for no mark and play");
    };

    assert_eq!(
        (library.as_str(), series.as_str()),
        ("screening/serials", SERIES)
    );
    assert_eq!(
        episodes,
        [
            listed((1, 2), 2_700),
            listed((1, 3), RUNTIME),
            listed((1, 4), RUNTIME),
            listed((1, 5), RUNTIME),
            listed((2, 1), RUNTIME),
        ]
    );
}

// Every later episode the audience started or finished is cleared, as a
// Clear progress mark on its row would clear it. A later episode they never
// started, or already cleared, holds nothing to clear, and a special is in
// no order, so the press lists none of them.
#[test]
fn a_press_clears_every_later_episode_they_started_or_finished() {
    let source = with_specials(vec![
        finished((1, 1)),
        finished((1, 2)),
        reached((1, 4), 690, 2_700),
        finished((2, 2)),
        cleared((3, 1)),
        finished((3, 6)),
        finished((0, 1)),
    ]);
    let (mut page, mut source) = row_of(source, (1, 3));

    let Step::PickUp { episodes, .. } = press(&mut page, &mut source, "Pick up here") else {
        panic!("the press asks for no mark and play");
    };

    assert_eq!(
        episodes,
        [
            unlisted((1, 4), 2_700),
            unlisted((2, 2), RUNTIME),
            unlisted((3, 6), RUNTIME),
        ]
    );
}

// What a Play step asks for, where the step is one.
fn played(step: Step) -> (Selection, Option<i64>, Option<Box<upnext::Next>>) {
    let Step::Play {
        selection,
        start,
        next,
        ..
    } = step
    else {
        panic!("the step is no play");
    };
    (selection, start, next)
}

#[test]
fn a_press_plays_the_episode_the_way_play_does_and_returns_focus_to_its_still() {
    let (mut page, mut source) = row_of(Serials::default(), (1, 3));
    let play = played(press(&mut page, &mut source, "Play"));
    page.focus = Focus::Episode(2, 0);

    let Step::PickUp { play: picked, .. } = press(&mut page, &mut source, "Pick up here") else {
        panic!("the press asks for no mark and play");
    };

    assert_eq!(played(*picked), play);
    assert_eq!(page.focus, Focus::Still(2));
}

#[test]
fn a_press_on_an_episode_they_are_in_the_middle_of_starts_it_from_the_beginning() {
    let (mut page, mut source) = row_of(
        Serials {
            progress: vec![reached((1, 3), 690, RUNTIME)],
            ..Serials::default()
        },
        (1, 3),
    );

    let Step::PickUp { play, .. } = press(&mut page, &mut source, "Pick up here") else {
        panic!("the press asks for no mark and play");
    };

    let (selection, start, _) = played(*play);
    assert_eq!(
        selection,
        Selection::Episode {
            series: SERIES.into(),
            season: 1,
            episode: 3,
        }
    );
    assert_eq!(start, None);
}
