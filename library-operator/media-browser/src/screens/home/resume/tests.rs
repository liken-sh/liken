// The pure halves of the row: how offers fold into cards, and how the
// audience's plays land on the leaves of a container.

use super::containers::{Leaf, on_leaves};
use super::*;
use crate::catalog::{InSeries, Played, Progress};

const FILMS: &str = "screening/films";
const SHOWS: &str = "screening/serials";

fn film(id: &str) -> Slot {
    Slot {
        library: FILMS.into(),
        kind: MOVIES.into(),
        id: id.into(),
        ..Slot::default()
    }
}

fn episode(series: &str, season: i64, episode: i64) -> Slot {
    Slot {
        library: SHOWS.into(),
        kind: "episodes".into(),
        id: format!("{series}:{season}:{episode}"),
        episode: Some(InSeries {
            series: series.into(),
            name: "A Show".into(),
            season,
            episode,
        }),
        ..Slot::default()
    }
}

fn set(title: &str) -> Reason {
    Reason::Set(title.into())
}

fn drawn(cards: &[Card]) -> Vec<(String, String, i64)> {
    cards
        .iter()
        .map(|card| {
            (
                card.slot.id.clone(),
                reason::line(&card.reasons),
                card.recorded,
            )
        })
        .collect()
}

#[test]
fn two_offers_of_one_leaf_are_one_card_with_both_reasons_and_the_newer_time() {
    let mut cards = Vec::new();
    fold(&mut cards, film("one"), set("The Set"), 10);
    fold(&mut cards, film("one"), Reason::Series("A Show".into()), 30);
    fold(&mut cards, film("two"), set("The Set"), 20);
    for card in &mut cards {
        reason::stacked(&mut card.reasons);
    }

    assert_eq!(
        drawn(&cards),
        [
            ("one".into(), "Next in A Show · Next in The Set".into(), 30),
            ("two".into(), "Next in The Set".into(), 20),
        ]
    );
}

#[test]
fn a_thread_that_resumes_a_leaf_puts_its_position_on_the_card() {
    let mut cards = Vec::new();
    fold(&mut cards, film("one"), set("The Set"), 10);
    let mut resumed = film("one");
    resumed.progress = Some(Played {
        position: 600,
        duration: 5_400,
    });
    fold(
        &mut cards,
        resumed,
        Reason::Resume {
            series: String::new(),
        },
        5,
    );

    assert_eq!(cards.len(), 1);
    assert_eq!(
        cards[0].slot.progress.map(|played| played.position),
        Some(600)
    );
    assert_eq!(cards[0].recorded, 10);
}

#[test]
fn one_id_in_two_libraries_is_two_cards() {
    let mut cards = Vec::new();
    fold(&mut cards, film("one"), set("The Set"), 10);
    let mut other = film("one");
    other.library = "screening/shorts".into();
    fold(&mut cards, other, set("The Set"), 10);

    assert_eq!(cards.len(), 2);
}

// One play as the store answers it, of a film or of an episode.
fn play(library: &str, id: &str, numbers: (i64, i64), recorded: i64, exact: bool) -> Resume {
    Resume {
        library: library.into(),
        kind: match numbers == (0, 0) {
            true => "movie".into(),
            false => "series".into(),
        },
        id: id.into(),
        progress: Progress {
            play: format!("play-{recorded}"),
            recorded,
            season: numbers.0,
            episode: numbers.1,
            ..Progress::default()
        },
        exact,
        ..Resume::default()
    }
}

fn leaves(slots: Vec<Slot>) -> Vec<Leaf> {
    slots
        .into_iter()
        .map(|slot| Leaf { slot, member: 0 })
        .collect()
}

#[test]
fn every_play_lands_on_the_leaf_it_names_and_no_other() {
    let leaves = leaves(vec![
        film("one"),
        episode("show", 1, 1),
        episode("show", 1, 2),
        film("two"),
    ]);
    let plays = grouped(vec![
        play(FILMS, "two", (0, 0), 40, true),
        play(SHOWS, "show", (1, 2), 30, false),
        play(SHOWS, "show", (2, 1), 20, true),
        play(FILMS, "nine", (0, 0), 10, true),
    ]);

    let on: Vec<(usize, i64, bool)> = on_leaves(&leaves, &plays)
        .into_iter()
        .map(|play| (play.leaf, play.progress.recorded, play.exact))
        .collect();

    assert_eq!(on, [(2, 30, false), (3, 40, true)]);
}

#[test]
fn a_film_of_another_library_under_the_same_id_lands_on_no_leaf() {
    let leaves = leaves(vec![film("one")]);
    let plays = grouped(vec![play("screening/shorts", "one", (0, 0), 10, true)]);

    assert!(on_leaves(&leaves, &plays).is_empty());
}
