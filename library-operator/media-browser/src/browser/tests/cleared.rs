// The continue-watching row after a mark: a mark is a row of the store
// like a play, newer than every play before it, so the thread rule reads
// it the same way. Watched moves the thread on, and cleared takes the
// container off the row.

use super::resume::{play, plays, reached, row, watched};
use super::*;

// The row after the plays of `resume::plays` and one mark recorded after
// all of them.
fn marked(mark: Resume) -> Vec<(String, Option<InSeries>)> {
    let mut plays = plays();
    plays.push(mark);
    let (browser, _bus) = watched(plays, false);
    row(&browser)
        .items
        .iter()
        .map(|item| (item.id.clone(), item.episode.clone()))
        .collect()
}

// Whether the row holds a card of this series.
fn holds_series(cards: &[(String, Option<InSeries>)], series: &str) -> bool {
    cards
        .iter()
        .any(|(_, episode)| episode.as_ref().is_some_and(|place| place.series == series))
}

// Whether the row holds a card of this film.
fn holds_film(cards: &[(String, Option<InSeries>)], id: &str) -> bool {
    cards.iter().any(|(held, _)| held == id)
}

#[test]
fn every_container_of_the_plays_stands_on_the_row_before_a_mark() {
    let (browser, _bus) = watched(plays(), false);
    let cards: Vec<(String, Option<InSeries>)> = row(&browser)
        .items
        .iter()
        .map(|item| (item.id.clone(), item.episode.clone()))
        .collect();

    assert!(holds_film(&cards, "movies:1"));
    assert!(holds_series(&cards, SERIAL));
    assert!(holds_series(&cards, OTHER_SERIAL));
}

#[test]
fn watched_on_a_film_card_removes_the_card() {
    let cards = marked(play(
        "movie",
        "movies:1",
        "Entry 1",
        reached(5_400, 5_400, (0, 0), 60),
    ));

    assert!(!holds_film(&cards, "movies:1"));
}

#[test]
fn cleared_on_a_film_card_removes_the_card() {
    let cards = marked(play(
        "movie",
        "movies:1",
        "Entry 1",
        reached(0, 5_400, (0, 0), 60),
    ));

    assert!(!holds_film(&cards, "movies:1"));
}

#[test]
fn watched_on_an_episode_card_puts_the_next_episode_on_it() {
    let mut plays = plays();
    plays.push(play(
        "series",
        SERIAL,
        "The Serial",
        reached(2_760, 2_760, (1, 2), 60),
    ));
    let (browser, _bus) = watched(plays, false);
    let reasons: Vec<String> = row(&browser)
        .items
        .iter()
        .map(|item| item.under.clone())
        .collect();

    assert!(
        reasons
            .iter()
            .any(|under| under.contains("Next in The Serial · S01")),
        "{reasons:?}"
    );
    assert!(
        !reasons
            .iter()
            .any(|under| under.contains("Resume · The Serial")),
        "{reasons:?}"
    );
}

#[test]
fn cleared_on_an_episode_card_takes_the_series_off_the_row() {
    let cards = marked(play(
        "series",
        SERIAL,
        "The Serial",
        reached(0, 2_760, (1, 2), 60),
    ));

    assert!(!holds_series(&cards, SERIAL));
    assert!(holds_series(&cards, OTHER_SERIAL));
}

#[test]
fn cleared_on_a_next_episode_card_takes_the_series_off_the_row() {
    let cards = marked(play(
        "series",
        OTHER_SERIAL,
        "Another Serial",
        reached(0, 2_760, (1, 3), 60),
    ));

    assert!(!holds_series(&cards, OTHER_SERIAL));
}
