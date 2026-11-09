// The continue-watching row over a real catalog file and a real progress
// file, where the show's next episode arrives after the audience's last
// play of it. A thread that finished every episode the catalog held moves
// to the time the next one arrived; any other thread keeps the time of its
// play.

use super::*;
use crate::screens::home::resume::{self, Card};

// How long every episode of the show runs.
const RUNTIME: i64 = 1_320;

// One episode of season 1 of the show, and the second its file arrived.
fn an_episode(catalog: &Path, number: i64, added: i64) {
    let id = format!("episode:tvdb:{number}");
    insert_episode(catalog, SHOWS, &id, SHOW, 1, number);
    Connection::open(catalog)
        .unwrap()
        .execute(
            "UPDATE episodes SET added = ? WHERE library = ? AND id = ?",
            (added, SHOWS, &id),
        )
        .unwrap();
}

// The film, started at second 30, and the show's three episodes. The
// first two arrived long ago, and the third at `third`. The audience's
// last play of the show is on episode 2 at second 20, at `position`.
fn row(third: i64, position: i64) -> Vec<Card> {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    a_show(&catalog);
    an_episode(&catalog, 1, 1);
    an_episode(&catalog, 2, 2);
    an_episode(&catalog, 3, third);
    show_play(&store, "first", (RUNTIME, RUNTIME, 10), (1, 1));
    show_play(&store, "second", (position, RUNTIME, 20), (1, 2));
    film_play(&store, "film", &["first"], (600, 6000, 30));
    let mut source = source_over(&catalog, store);
    resume::cards(&mut source, &names(&["first"]))
}

// Each card as its id, its second line, and the time the row sorts it by.
fn drawn(cards: Vec<Card>) -> Vec<(String, String, i64)> {
    cards
        .into_iter()
        .map(|card| {
            let under = resume::under(&card.slot, &card.reasons);
            (card.slot.id, under, card.recorded)
        })
        .collect()
}

#[test]
fn an_episode_that_arrives_after_the_audience_caught_up_moves_the_show_to_the_front() {
    assert_eq!(
        drawn(row(40, RUNTIME)),
        [
            (
                "episode:tvdb:3".to_string(),
                "New · Some Show · S01".to_string(),
                40
            ),
            (FILM.to_string(), "Resume".to_string(), 30),
        ]
    );
}

#[test]
fn an_episode_the_catalog_held_before_the_last_play_keeps_the_show_at_its_play() {
    assert_eq!(
        drawn(row(15, RUNTIME)),
        [
            (FILM.to_string(), "Resume".to_string(), 30),
            (
                "episode:tvdb:3".to_string(),
                "Next in Some Show · S01".to_string(),
                20
            ),
        ]
    );
}

#[test]
fn a_show_left_inside_an_episode_keeps_its_play_when_the_next_arrives() {
    assert_eq!(
        drawn(row(40, 600)),
        [
            (FILM.to_string(), "Resume".to_string(), 30),
            (
                "episode:tvdb:2".to_string(),
                "Resume · Some Show · S01".to_string(),
                20
            ),
        ]
    );
}

// Episodes 3 and 4 both arrived after the audience finished episode 2,
// episode 4 first. The audience was caught up, and the row moves the show
// to the arrival of episode 3, the episode it offers.
#[test]
fn the_show_moves_to_the_arrival_of_the_episode_it_offers() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    an_episode(&catalog, 1, 1);
    an_episode(&catalog, 2, 2);
    an_episode(&catalog, 3, 50);
    an_episode(&catalog, 4, 40);
    show_play(&store, "second", (RUNTIME, RUNTIME, 20), (1, 2));
    let mut source = source_over(&catalog, store);

    let cards = resume::cards(&mut source, &names(&["first"]));
    assert_eq!(cards[0].recorded, 50);
}

// An episode 4 the catalog held when the audience finished episode 2 means
// they had not caught up, even though episode 3 arrived after the play.
#[test]
fn a_later_episode_the_catalog_held_before_the_play_keeps_the_show_at_its_play() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    an_episode(&catalog, 1, 1);
    an_episode(&catalog, 2, 2);
    an_episode(&catalog, 3, 50);
    an_episode(&catalog, 4, 3);
    show_play(&store, "second", (RUNTIME, RUNTIME, 20), (1, 2));
    let mut source = source_over(&catalog, store);

    let cards = drawn(resume::cards(&mut source, &names(&["first"])));
    assert_eq!(
        cards,
        [(
            "episode:tvdb:3".to_string(),
            "Next in Some Show · S01".to_string(),
            20
        )]
    );
}
