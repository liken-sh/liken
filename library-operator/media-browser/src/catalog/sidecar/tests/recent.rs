// The two recency reads: movies and episodes off every library, newest
// first, and the fold each query applies.

use super::*;
use crate::catalog::recency::{CANDIDATES, date_seconds};

const SHOWS: &str = "default/shows";
const FILMS: &str = "default/films";
const SERIAL: &str = "series:tvdb:1";

// One episode of the serial with its release and its arrival, which is
// what the airing fold reads.
fn insert_arrived_episode(path: &Path, id: &str, numbers: (i64, i64), released: &str, added: i64) {
    let (season, episode) = numbers;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO episodes (library, id, kind, title, series, season, episode, \
             released, added, art, duration) \
             VALUES (?, ?, 'series', ?, ?, ?, ?, ?, ?, ?, 2760)",
            (
                SHOWS,
                id,
                format!("Segment {episode}"),
                SERIAL,
                season,
                episode,
                released,
                added,
                format!("{id}.jpg"),
            ),
        )
        .unwrap();
}

// A serial with an airing season: two episodes that arrived the day
// after they aired, and one from a back catalog that arrived years late.
fn a_catalog(path: &Path) {
    insert_series_page(path, SHOWS, SERIAL, "2004", r#"{"contentRating":"TV-14"}"#);
    let day = 86_400;
    insert_arrived_episode(
        path,
        "episode:tvdb:3",
        (2, 1),
        "2026-09-02",
        date_seconds("2026-09-02").unwrap() + day,
    );
    insert_arrived_episode(
        path,
        "episode:tvdb:2",
        (1, 2),
        "2026-08-26",
        date_seconds("2026-08-26").unwrap() + day,
    );
    insert_arrived_episode(
        path,
        "episode:tvdb:1",
        (1, 1),
        "2004-09-22",
        date_seconds("2026-08-20").unwrap(),
    );
    insert_added_movie(
        path,
        FILMS,
        "movie:tmdb:2",
        "2026-08-30",
        date_seconds("2026-09-01").unwrap(),
    );
    insert_added_movie(
        path,
        FILMS,
        "movie:tmdb:1",
        "1999",
        date_seconds("2026-08-01").unwrap(),
    );
}

fn ids(answer: &crate::catalog::Answer) -> Vec<&str> {
    answer.slots.iter().map(|slot| slot.id.as_str()).collect()
}

#[test]
fn released_comes_back_newest_first_across_the_libraries() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released {
        fold: Fold::Episodes,
    });
    assert_eq!(answer.name, "");
    assert_eq!(
        ids(&answer),
        [
            "episode:tvdb:3",
            "movie:tmdb:2",
            "episode:tvdb:2",
            "episode:tvdb:1",
            "movie:tmdb:1",
        ]
    );
    assert_eq!(
        answer.slots[0],
        Slot {
            library: SHOWS.into(),
            kind: "episodes".into(),
            id: "episode:tvdb:3".into(),
            title: "Segment 1".into(),
            released: "2026-09-02".into(),
            art: "episode:tvdb:3.jpg".into(),
            duration: 2760,
            episode: Some(InSeries {
                series: SERIAL.into(),
                name: format!("Serial {SERIAL}"),
                season: 2,
                episode: 1,
            }),
            ..Slot::default()
        }
    );
    assert_eq!(answer.slots[1].kind, "movies");
    assert_eq!(answer.slots[1].library, FILMS);
}

#[test]
fn added_comes_back_by_arrival_and_not_by_release() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Added {
        fold: Fold::Episodes,
    });
    assert_eq!(
        ids(&answer),
        [
            "episode:tvdb:3",
            "movie:tmdb:2",
            "episode:tvdb:2",
            "episode:tvdb:1",
            "movie:tmdb:1",
        ]
    );
}

#[test]
fn the_airing_fold_keeps_the_new_episodes_and_folds_the_back_catalog() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released { fold: Fold::Airing });
    assert_eq!(
        ids(&answer),
        [
            "episode:tvdb:3",
            "movie:tmdb:2",
            "episode:tvdb:2",
            SERIAL,
            "movie:tmdb:1"
        ]
    );
    let folded = &answer.slots[3];
    assert_eq!(folded.kind, "series");
    assert_eq!(folded.title, format!("Serial {SERIAL}"));
    assert_eq!(folded.art, format!("{SERIAL}.jpg"));
    assert_eq!(folded.released, "2004-09-22");
    assert_eq!(folded.rating, "TV-14");
}

#[test]
fn the_titles_fold_is_all_posters() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Added { fold: Fold::Titles });
    assert_eq!(ids(&answer), [SERIAL, "movie:tmdb:2", "movie:tmdb:1"]);
    assert!(answer.slots.iter().all(|slot| !slot.still()));
    assert_eq!(answer.slots[0].released, "2026-09-02");
}

#[test]
fn an_episode_whose_series_row_is_missing_is_left_out() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_arrived_episode(&path, "episode:tvdb:9", (1, 1), "2026-09-01", 0);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(
        source
            .wall(&Query::Released {
                fold: Fold::Episodes
            })
            .slots
            .is_empty()
    );
}

#[test]
fn the_read_takes_a_bounded_number_of_candidates() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    for number in 0..CANDIDATES + 5 {
        insert_added_movie(
            &path,
            FILMS,
            &format!("movie:tmdb:{number}"),
            &format!("{}", 1900 + number),
            number as i64,
        );
    }

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released { fold: Fold::Titles });
    assert_eq!(answer.slots.len(), CANDIDATES);
    assert_eq!(answer.slots[0].id, format!("movie:tmdb:{}", CANDIDATES + 4));
}
