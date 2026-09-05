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

// The serial's own art: no landscape file, so the fanart is the 16:9 art
// an episode with no still of its own draws.
fn a_serial_with_art(path: &Path) {
    set_series_art(
        path,
        SHOWS,
        SERIAL,
        "Serial/poster.jpg",
        &["Serial/poster.jpg", "Serial/fanart.jpg"],
    );
}

#[test]
fn an_episode_with_no_still_draws_the_art_of_its_series() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);
    a_serial_with_art(&path);
    clear_episode_art(&path, SHOWS, "episode:tvdb:3");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released {
        fold: Fold::Episodes,
    });
    assert_eq!(answer.slots[0].id, "episode:tvdb:3");
    assert_eq!(answer.slots[0].art, "Serial/fanart.jpg");
    assert_eq!(answer.slots[2].art, "episode:tvdb:2.jpg");
}

#[test]
fn a_show_folded_on_an_episode_with_no_still_draws_the_art_of_its_series() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);
    a_serial_with_art(&path);
    clear_episode_art(&path, SHOWS, "episode:tvdb:3");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released {
        fold: Fold::Shows {
            today: date_seconds("2026-09-03").unwrap(),
        },
    });
    assert_eq!(answer.slots[0].id, SERIAL);
    assert_eq!(answer.slots[0].art, "Serial/fanart.jpg");
}

#[test]
fn an_episode_of_a_series_with_no_art_draws_no_still_at_all() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_catalog(&path);
    set_series_art(&path, SHOWS, SERIAL, "", &[]);
    clear_episode_art(&path, SHOWS, "episode:tvdb:3");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Released {
        fold: Fold::Episodes,
    });
    assert_eq!(answer.slots[0].id, "episode:tvdb:3");
    assert_eq!(answer.slots[0].art, "");
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
fn a_full_page_of_titles_is_the_read_s_fill() {
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

// A season drop of one serial that fills a whole page of rows, with a
// few films that arrived just before it. A read that counted rows would
// answer the serial alone.
#[test]
fn a_season_drop_does_not_starve_the_read() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series_page(&path, SHOWS, SERIAL, "2004", r#"{"contentRating":"TV-14"}"#);
    for number in 0..CANDIDATES as i64 + 10 {
        insert_arrived_episode(
            &path,
            &format!("episode:tvdb:{number}"),
            (1, number),
            "2004-03-01",
            1_000_000 + number,
        );
    }
    for number in 0..5 {
        insert_added_movie(
            &path,
            FILMS,
            &format!("movie:tmdb:{number}"),
            "1999",
            500 + number,
        );
    }

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&Query::Added { fold: Fold::Titles });
    let ids: Vec<&str> = answer.slots.iter().map(|slot| slot.id.as_str()).collect();
    assert_eq!(
        ids,
        [
            SERIAL,
            "movie:tmdb:4",
            "movie:tmdb:3",
            "movie:tmdb:2",
            "movie:tmdb:1",
            "movie:tmdb:0"
        ]
    );
}
