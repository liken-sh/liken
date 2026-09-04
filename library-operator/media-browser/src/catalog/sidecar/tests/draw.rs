// The genre read across libraries, and the pool read with its weights
// and floors.

use super::*;
use crate::catalog::pool::{Candidate, Kind};
use crate::catalog::recency::{WORKS_FLOOR, date_seconds};
use crate::catalog::{Answer, GenreEntry, Order};

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";

// One genre row of one title at its rank.
fn insert_genre(path: &Path, library: &str, item: &str, rank: i64, genre: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO genres (library, item, rank, genre) VALUES (?, ?, ?, ?)",
            (library, item, rank, genre),
        )
        .unwrap();
}

fn genre(name: &str, order: Order) -> Query {
    Query::Genre {
        name: name.into(),
        order,
    }
}

// Three films and a serial carry Western. Two films and the serial lead
// with it, one lists it second, and `added` runs the other way from
// `released`, so the two orders differ.
fn westerns(path: &Path) {
    insert_added_movie(
        path,
        FILMS,
        "old",
        "1969",
        date_seconds("2026-09-01").unwrap(),
    );
    insert_added_movie(
        path,
        FILMS,
        "new",
        "1992",
        date_seconds("2026-08-01").unwrap(),
    );
    insert_added_movie(
        path,
        FILMS,
        "streak",
        "2007",
        date_seconds("2026-07-01").unwrap(),
    );
    insert_added_movie(
        path,
        FILMS,
        "apart",
        "1994",
        date_seconds("2026-06-01").unwrap(),
    );
    insert_series_page(path, SHOWS, "serial", "2004", "{}");
    insert_genre(path, FILMS, "old", 0, "Western");
    insert_genre(path, FILMS, "new", 0, "Western");
    insert_genre(path, FILMS, "new", 1, "Drama");
    insert_genre(path, FILMS, "streak", 0, "Crime");
    insert_genre(path, FILMS, "streak", 1, "Western");
    insert_genre(path, FILMS, "apart", 0, "Drama");
    insert_genre(path, SHOWS, "serial", 0, "Western");
}

fn ids(answer: &Answer) -> Vec<&str> {
    answer.slots.iter().map(|slot| slot.id.as_str()).collect()
}

#[test]
fn a_genre_reads_the_titles_that_lead_with_it_first_and_then_newest_release() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    westerns(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&genre("Western", Order::Released));

    assert_eq!(answer.name, "Western");
    assert_eq!(ids(&answer), ["serial", "new", "old", "streak"]);
    assert_eq!(answer.slots[0].kind, "series");
    assert_eq!(answer.slots[0].library, SHOWS);
    assert_eq!(answer.slots[1].kind, "movies");
    assert_eq!(answer.slots[1].library, FILMS);
    assert_eq!(answer.slots[1].title, "Film new");
    assert_eq!(answer.slots[1].art, "new.jpg");
}

#[test]
fn a_genre_ordered_by_arrival_keeps_the_rank_first() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    westerns(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&genre("Western", Order::Added));

    assert_eq!(ids(&answer), ["old", "new", "serial", "streak"]);
}

#[test]
fn a_genre_no_title_carries_answers_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    westerns(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&genre("Musical", Order::Released));

    assert_eq!(answer.name, "Musical");
    assert!(answer.slots.is_empty());
}

#[test]
fn every_genre_carries_its_count_and_the_poster_of_its_newest_title() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    westerns(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);

    assert_eq!(
        source.genres(),
        [
            GenreEntry {
                name: "Crime".into(),
                titles: 1,
                library: FILMS.into(),
                art: "streak.jpg".into(),
            },
            GenreEntry {
                name: "Drama".into(),
                titles: 2,
                library: FILMS.into(),
                art: "apart.jpg".into(),
            },
            GenreEntry {
                name: "Western".into(),
                titles: 4,
                library: FILMS.into(),
                art: "streak.jpg".into(),
            },
        ]
    );
}

#[test]
fn a_genres_poster_is_the_newest_title_that_has_one_in_any_library() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    westerns(&path);
    insert_added_movie(
        &path,
        FILMS,
        "bare",
        "2020",
        date_seconds("2026-10-01").unwrap(),
    );
    let connection = Connection::open(&path).unwrap();
    connection
        .execute("UPDATE movies SET art = '' WHERE id = 'bare'", ())
        .unwrap();
    insert_genre(&path, FILMS, "bare", 0, "Western");
    insert_genre(&path, FILMS, "bare", 1, "Silent");
    insert_genre(&path, SHOWS, "serial", 1, "Drama");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let entries = source.genres();

    assert_eq!(
        entries.iter().find(|entry| entry.name == "Western"),
        Some(&GenreEntry {
            name: "Western".into(),
            titles: 5,
            library: FILMS.into(),
            art: "streak.jpg".into(),
        })
    );
    assert_eq!(
        entries.iter().find(|entry| entry.name == "Drama"),
        Some(&GenreEntry {
            name: "Drama".into(),
            titles: 3,
            library: SHOWS.into(),
            art: "serial.jpg".into(),
        })
    );
    assert_eq!(
        entries.iter().find(|entry| entry.name == "Silent"),
        Some(&GenreEntry {
            name: "Silent".into(),
            titles: 1,
            library: String::new(),
            art: String::new(),
        })
    );
}

// A person over the works floor, one at it, a set of two, and a set of
// one, beside the westerns.
fn a_pool(path: &Path) {
    westerns(path);
    insert_contributor(path, FILMS, ".contributors/busy", "A Busy One", (0, 1));
    insert_contributor(path, FILMS, ".contributors/rare", "A Rare One", (0, 0));
    for (billing, item) in ["old", "new", "streak", "apart"].into_iter().enumerate() {
        insert_credit(
            path,
            FILMS,
            item,
            billing as i64,
            (".contributors/busy", "A Busy One"),
            ("actor", "The Part"),
        );
    }
    // A second credit on one title is one work, not two.
    insert_credit(
        path,
        FILMS,
        "old",
        9,
        (".contributors/busy", "A Busy One"),
        ("director", ""),
    );
    for (billing, item) in ["old", "new", "streak"].into_iter().enumerate() {
        insert_credit(
            path,
            FILMS,
            item,
            10 + billing as i64,
            (".contributors/rare", "A Rare One"),
            ("writer", ""),
        );
    }
    insert_credit(path, FILMS, "apart", 20, ("", "Nobody"), ("writer", ""));
    insert_set(path, FILMS, "set:pair", "The Pair");
    insert_set(path, FILMS, "set:lone", "The Lone");
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "UPDATE movies SET set_id = 'set:pair' WHERE id IN ('old', 'new')",
            (),
        )
        .unwrap();
    connection
        .execute(
            "UPDATE movies SET set_id = 'set:lone' WHERE id = 'apart'",
            (),
        )
        .unwrap();
}

#[test]
fn the_pool_holds_every_genre_weighed_with_its_leading_titles_twice() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_pool(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let pool = source.pool();

    let genres: Vec<(&str, u64)> = pool
        .iter()
        .filter(|candidate| candidate.kind() == Kind::Genre)
        .map(|candidate| (candidate.name.as_str(), candidate.weight))
        .collect();
    assert_eq!(genres, [("Crime", 2), ("Drama", 3), ("Western", 7)]);
    assert_eq!(
        pool[0].query,
        Query::Genre {
            name: "Crime".into(),
            order: Order::Released,
        }
    );
}

#[test]
fn the_pool_holds_a_person_over_the_floor_and_a_set_of_two() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_pool(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let pool = source.pool();

    let rest: Vec<&Candidate> = pool
        .iter()
        .filter(|candidate| candidate.kind() != Kind::Genre)
        .collect();
    assert_eq!(
        rest,
        [
            &Candidate {
                query: Query::Person {
                    library: FILMS.into(),
                    path: ".contributors/busy".into(),
                },
                name: "A Busy One".into(),
                weight: WORKS_FLOOR + 1,
            },
            &Candidate {
                query: Query::Set {
                    library: FILMS.into(),
                    id: "set:pair".into(),
                },
                name: "The Pair".into(),
                weight: 2,
            },
        ]
    );
}

#[test]
fn an_empty_catalog_has_an_empty_pool() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.pool().is_empty());
}
