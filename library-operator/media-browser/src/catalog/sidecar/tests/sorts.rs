// The four orders a wall's rail cycles through, as the sidecar answers
// them: the three plain orders on a library wall, and those three under
// the leading order on a genre wall.

use super::*;
use crate::catalog::{GenreSort, Order};

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";

// One film with the two columns the orders read: the sort key and the
// release.
fn insert_film(path: &Path, id: &str, sort_key: &str, released: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key, released) \
             VALUES (?, ?, 'movies', ?, ?, ?)",
            (FILMS, id, format!("Film {id}"), sort_key, released),
        )
        .unwrap();
}

// One serial with the same two columns, in the serials library.
fn insert_show(path: &Path, id: &str, sort_key: &str, released: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO series (library, id, kind, title, sort_key, released) \
             VALUES (?, ?, 'series', ?, ?, ?)",
            (SHOWS, id, format!("Serial {id}"), sort_key, released),
        )
        .unwrap();
}

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

fn ids(answer: &Answer) -> Vec<String> {
    answer.slots.iter().map(|slot| slot.id.clone()).collect()
}

// Four films whose sort keys and releases disagree, and two of them
// released in the same year, so every order answers a different list
// and the sort key breaks the tie.
fn films(path: &Path) {
    insert_film(path, "a", "amber", "1999");
    insert_film(path, "b", "brine", "2004");
    insert_film(path, "c", "cusp", "1994");
    insert_film(path, "d", "delta", "1999");
}

#[test]
fn a_library_wall_answers_each_of_its_three_orders() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    films(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut wall = |sort| {
        source.wall(&Query::Library {
            library: FILMS.into(),
            sort,
        })
    };
    assert_eq!(ids(&wall(Sort::Title)), ["a", "b", "c", "d"]);
    assert_eq!(ids(&wall(Sort::Newest)), ["b", "a", "d", "c"]);
    assert_eq!(ids(&wall(Sort::Oldest)), ["c", "a", "d", "b"]);
}

#[test]
fn a_series_library_answers_the_same_three_orders() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_show(&path, "one", "amber", "1999");
    insert_show(&path, "two", "brine", "1994");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut wall = |sort| {
        source.wall(&Query::Library {
            library: SHOWS.into(),
            sort,
        })
    };
    assert_eq!(ids(&wall(Sort::Title)), ["one", "two"]);
    assert_eq!(ids(&wall(Sort::Newest)), ["one", "two"]);
    assert_eq!(ids(&wall(Sort::Oldest)), ["two", "one"]);
}

// Three titles carrying Action across two libraries: two lead with it,
// one lists it second, and no two of the four orders agree.
fn action(path: &Path) {
    insert_film(path, "a", "amber", "1999");
    insert_film(path, "b", "brine", "2004");
    insert_show(path, "c", "cusp", "1994");
    insert_genre(path, FILMS, "a", 1, "Action");
    insert_genre(path, FILMS, "b", 0, "Action");
    insert_genre(path, SHOWS, "c", 0, "Action");
}

#[test]
fn a_genre_wall_answers_each_of_its_four_orders() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    action(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut wall = |sort| {
        source.wall(&Query::Genre {
            name: "Action".into(),
            order: Order::Released,
            sort,
        })
    };
    assert_eq!(ids(&wall(GenreSort::Leads)), ["b", "c", "a"]);
    assert_eq!(ids(&wall(GenreSort::By(Sort::Newest))), ["b", "a", "c"]);
    assert_eq!(ids(&wall(GenreSort::By(Sort::Oldest))), ["c", "a", "b"]);
    assert_eq!(ids(&wall(GenreSort::By(Sort::Title))), ["a", "b", "c"]);
}

#[test]
fn a_genre_wall_is_headed_by_its_name_and_the_counts_by_kind() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    action(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let query = Query::Genre {
        name: "Action".into(),
        order: Order::Released,
        sort: GenreSort::default(),
    };
    let answer = source.wall(&query);
    let counts = Counts::of(answer.slots.iter().map(|slot| slot.kind.as_str()));
    assert_eq!(
        query.heading(&answer.name, counts),
        "Action · 2 movies, 1 series"
    );
}
