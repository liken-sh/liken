// The bounded SQL read behind the recency folds: its payload mapping,
// candidate order, limit, and agreement with a paged reference.

use super::super::recent as recent_read;
use super::*;
use crate::catalog::recency::{CANDIDATES, Candidate, PAGES};
use crate::catalog::{Order, Title};

const SHOWS: &str = "default/shows";
const FILMS: &str = "default/films";
const SERIAL: &str = "series:tvdb:1";

fn candidates(connection: &Connection, order: Order) -> Vec<Candidate> {
    recent_read::candidates(connection, order).unwrap()
}

// One movie and one episode with every payload field the query maps. The
// NULL JSON values must become empty strings, and an episode body is unused.
fn payload_catalog() -> (TempDir, Connection) {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let connection = Connection::open(path).unwrap();
    connection
        .execute_batch(
            "INSERT INTO movies (library, id, kind, title, released, added, art, duration, body) \
             VALUES ('default/films', 'same', 'movies', 'A film', '2026-09-03', 8, \
                     'film.jpg', 6000, '{\"contentRating\":\"PG\",\"tagline\":null}'); \
             INSERT INTO series (library, id, kind, title, released, art, duration, body) \
             VALUES ('default/shows', 'series:tvdb:1', 'series', 'A serial', '2004', \
                     'serial.jpg', 3600, '{\"contentRating\":null}'); \
             INSERT INTO episodes (library, id, kind, title, series, season, episode, \
                                    released, added, art, duration, body) \
             VALUES ('default/shows', 'same', 'series', 'A segment', 'series:tvdb:1', \
                     2, 3, '2026-09-02', 9, 'still.jpg', 2700, 'not json');",
        )
        .unwrap();
    (dir, connection)
}

#[test]
fn a_movie_candidate_maps_its_full_payload() {
    let (_dir, connection) = payload_catalog();
    let answer = candidates(&connection, Order::Added);

    assert_eq!(
        answer[1],
        Candidate::Movie {
            slot: Slot {
                library: FILMS.into(),
                kind: "movies".into(),
                id: "same".into(),
                title: "A film".into(),
                released: "2026-09-03".into(),
                art: "film.jpg".into(),
                duration: 6000,
                rating: "PG".into(),
                tagline: String::new(),
                ..Slot::default()
            }
        }
    );
}

#[test]
fn an_episode_candidate_maps_its_full_payload_and_series() {
    let (_dir, connection) = payload_catalog();
    let answer = candidates(&connection, Order::Added);

    assert_eq!(
        answer[0],
        Candidate::Episode {
            library: SHOWS.into(),
            episode: Title {
                id: "same".into(),
                title: "A segment".into(),
                released: "2026-09-02".into(),
                art: "still.jpg".into(),
                duration: 2700,
                rating: String::new(),
                tagline: String::new(),
            },
            added: 9,
            season: 2,
            number: 3,
            series: Title {
                id: SERIAL.into(),
                title: "A serial".into(),
                released: "2004".into(),
                art: "serial.jpg".into(),
                duration: 3600,
                rating: String::new(),
                tagline: String::new(),
            },
        }
    );
}

fn candidate_identity(candidate: &Candidate) -> (&str, &str, &str) {
    match candidate {
        Candidate::Movie { slot } => (&slot.library, &slot.id, "movies"),
        Candidate::Episode {
            library, episode, ..
        } => (library, &episode.id, "episodes"),
    }
}

// Two libraries carry a movie and an episode under the same id and order
// value. This leaves only the library and kind tie-breakers to order them.
fn tied_candidates() -> (TempDir, Connection) {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let connection = Connection::open(path).unwrap();
    connection
        .execute_batch(
            "INSERT INTO series (library, id, kind, title) VALUES \
                 ('a/shows', 'series:tvdb:1', 'series', 'A serial'), \
                 ('z/shows', 'series:tvdb:1', 'series', 'A serial'); \
             INSERT INTO movies (library, id, kind, title, released, added) VALUES \
                 ('a/shows', 'same', 'movies', 'A film', '2026-09-03', 7), \
                 ('z/shows', 'same', 'movies', 'A film', '2026-09-03', 7); \
             INSERT INTO episodes \
                 (library, id, kind, title, series, season, episode, released, added) VALUES \
                 ('a/shows', 'same', 'series', 'A segment', 'series:tvdb:1', 1, 1, \
                  '2026-09-03', 7), \
                 ('z/shows', 'same', 'series', 'A segment', 'series:tvdb:1', 1, 1, \
                  '2026-09-03', 7);",
        )
        .unwrap();
    (dir, connection)
}

#[test]
fn tied_candidates_order_by_library_and_put_the_movie_first() {
    let (_dir, connection) = tied_candidates();
    let answer = candidates(&connection, Order::Released);
    let identities: Vec<_> = answer.iter().map(candidate_identity).collect();

    assert_eq!(
        identities,
        [
            ("a/shows", "same", "movies"),
            ("a/shows", "same", "episodes"),
            ("z/shows", "same", "movies"),
            ("z/shows", "same", "episodes"),
        ]
    );
}

// More orphan episodes than the candidate limit sort ahead of one valid
// movie. The key selection must remove them before it applies the limit.
fn orphan_flood() -> (TempDir, Connection) {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let mut connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, released, added) \
             VALUES (?1, 'movie:valid', 'movies', 'Valid', '2000', 1)",
            [FILMS],
        )
        .unwrap();
    let transaction = connection.transaction().unwrap();
    for number in 0..PAGES * CANDIDATES {
        transaction
            .execute(
                "INSERT INTO episodes (library, id, kind, title, series, released, added) \
                 VALUES (?1, ?2, 'series', 'Orphan', 'missing', '2999', 2)",
                (SHOWS, format!("orphan:{number}")),
            )
            .unwrap();
    }
    transaction.commit().unwrap();
    (dir, connection)
}

#[test]
fn orphan_episodes_do_not_take_the_candidate_limit() {
    let (_dir, connection) = orphan_flood();
    let answer = candidates(&connection, Order::Released);

    assert_eq!(answer.len(), 1);
    assert_eq!(
        candidate_identity(&answer[0]),
        (FILMS, "movie:valid", "movies")
    );
}

fn paged_keys(connection: &Connection, order: Order) -> Vec<(String, String, String)> {
    let sql = format!(
        "SELECT library, id, kind FROM (\
           SELECT library, id, released, added, 'movies' AS kind FROM movies \
           UNION ALL \
           SELECT episodes.library, episodes.id, episodes.released, episodes.added, \
                  'episodes' AS kind \
           FROM episodes JOIN series ON series.library = episodes.library \
           AND series.id = episodes.series\
         ) ORDER BY {} DESC, library, id LIMIT ?1 OFFSET ?2",
        recent_read::column(order)
    );
    let mut keys = Vec::new();
    for page in 0..PAGES {
        let mut statement = connection.prepare(&sql).unwrap();
        let rows = statement
            .query_map([(CANDIDATES as i64), (page * CANDIDATES) as i64], |row| {
                Ok((row.get(0)?, row.get(1)?, row.get(2)?))
            })
            .unwrap();
        keys.extend(rows.map(Result::unwrap));
    }
    keys
}

// The catalog has 961 movies split across two libraries and enough newer
// episodes that both union branches occur inside the 960-candidate prefix.
fn cross_library_candidate_catalog() -> (TempDir, Connection) {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let mut connection = Connection::open(path).unwrap();
    let transaction = connection.transaction().unwrap();
    transaction
        .execute(
            "INSERT INTO series (library, id, kind, title) \
             VALUES ('a/shows', 'series:1', 'series', 'A serial')",
            [],
        )
        .unwrap();
    for number in 0..=PAGES * CANDIDATES {
        let library = if number % 2 == 0 {
            "a/films"
        } else {
            "z/films"
        };
        transaction
            .execute(
                "INSERT INTO movies (library, id, kind, title, released, added) \
                 VALUES (?1, ?2, 'movies', 'Film', ?3, ?4)",
                (
                    library,
                    format!("movie:{number:04}"),
                    format!("{:04}", 1900 + number),
                    number as i64,
                ),
            )
            .unwrap();
    }
    for number in 0..20 {
        transaction
            .execute(
                "INSERT INTO episodes \
                 (library, id, kind, title, series, season, episode, released, added) \
                 VALUES ('a/shows', ?1, 'series', 'Segment', 'series:1', 1, ?2, \
                         '2999', ?3)",
                (
                    format!("episode:{number:02}"),
                    number as i64,
                    2_000 + number as i64,
                ),
            )
            .unwrap();
    }
    transaction.commit().unwrap();
    (dir, connection)
}

#[test]
fn one_read_matches_the_paged_candidate_prefix() {
    let (_dir, connection) = cross_library_candidate_catalog();
    let expected = paged_keys(&connection, Order::Added);
    let answer = candidates(&connection, Order::Added);
    let actual: Vec<_> = answer
        .iter()
        .map(candidate_identity)
        .map(|(library, id, kind)| (library.to_string(), id.to_string(), kind.to_string()))
        .collect();

    assert_eq!(expected.len(), PAGES * CANDIDATES);
    assert!(expected.iter().any(|key| key.2 == "episodes"));
    assert_eq!(actual, expected);
}

// Exactly 960 valid rows sort ahead of one row whose body is invalid JSON.
// Evaluating payload fields before the candidate limit would fail the read.
fn invalid_json_past_limit() -> (TempDir, Connection) {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let mut connection = Connection::open(path).unwrap();
    let transaction = connection.transaction().unwrap();
    for number in 0..PAGES * CANDIDATES {
        transaction
            .execute(
                "INSERT INTO movies (library, id, kind, title, released, body) \
                 VALUES (?1, ?2, 'movies', 'Selected', '2026', '{}')",
                (FILMS, format!("selected:{number:04}")),
            )
            .unwrap();
    }
    transaction
        .execute(
            "INSERT INTO movies (library, id, kind, title, released, body) \
             VALUES (?1, 'dropped', 'movies', 'Dropped', '1900', 'not json')",
            [FILMS],
        )
        .unwrap();
    transaction.commit().unwrap();
    (dir, connection)
}

#[test]
fn invalid_json_after_the_candidate_limit_is_not_evaluated() {
    let (_dir, connection) = invalid_json_past_limit();
    let answer = candidates(&connection, Order::Released);

    assert_eq!(answer.len(), PAGES * CANDIDATES);
    assert!(
        answer
            .iter()
            .all(|candidate| candidate_identity(candidate).1 != "dropped")
    );
}
