// Where an episode is in its main file: a play of a later episode of a file
// of several starts at that episode's share of the file, and the file's
// last episode is the one the offer after a play follows.

use super::*;
use crate::catalog::Place;

// The season of `a_season` with S01E01 and S01E02 in one double of the
// probe's duration in place of their own files, and S01E03 alone.
fn a_season_with_a_double(path: &Path, duration_ms: i64) {
    insert_series(path, "default/shows", SERIES, "Harrow", "harrow");
    for episode in 1..=3 {
        let id = format!("episode:tvdb:{episode}");
        insert_episode(path, "default/shows", &id, SERIES, 1, episode);
    }
    let connection = Connection::open(path).unwrap();
    for (file, duration) in [
        ("Harrow/S01E01-E02.mkv", duration_ms),
        ("Harrow/S01E03.mkv", 1_320_000),
    ] {
        connection
            .execute(
                "INSERT INTO files (library, path, type, role, duration_ms) \
                 VALUES ('default/shows', ?, 'video', 'primary', ?)",
                (file, duration),
            )
            .unwrap();
    }
    for (file, item, part, parts) in [
        ("Harrow/S01E01-E02.mkv", "episode:tvdb:1", 1, 2),
        ("Harrow/S01E01-E02.mkv", "episode:tvdb:2", 2, 2),
        ("Harrow/S01E03.mkv", "episode:tvdb:3", 0, 0),
    ] {
        connection
            .execute(
                "INSERT INTO file_items (library, path, item, part, parts) \
                 VALUES ('default/shows', ?, ?, ?, ?)",
                (file, item, part, parts),
            )
            .unwrap();
    }
}

// The play list of each episode starts at its part of its file, and a
// double with no duration to split starts nowhere in particular.
#[test]
fn an_episode_plays_from_its_part_of_its_file() {
    let cases = [
        ("E01 of a double", 2_640_000, 1, Some(0)),
        ("E02 of a double", 2_640_000, 2, Some(1_320)),
        ("E03 alone", 2_640_000, 3, Some(0)),
        ("E02 of a double with no duration", 0, 2, None),
    ];
    for (case, duration_ms, episode, start) in cases {
        let dir = TempDir::new().unwrap();
        let path = fixture(&dir);
        a_season_with_a_double(&path, duration_ms);
        let mut source = LocalCatalog::new(&path, NO_AGENT);

        let items = source.play("default/shows", &episode_chosen(episode));

        let starts: Vec<Option<i64>> = items.iter().map(|item| item.start).collect();
        assert_eq!(starts, [start], "{case}");
    }
}

#[test]
fn a_film_plays_from_the_start_of_its_file() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(&path, "default/films", "movie:tmdb:7001", "Film", "film");
    insert_main_file(&path, "default/films", "Film.mkv", "movie:tmdb:7001");
    let mut source = LocalCatalog::new(&path, NO_AGENT);

    let items = source.play("default/films", &movie_chosen());

    assert_eq!(items[0].start, Some(0));
}

// Each episode's place, which names the file's last episode for the offer
// after a play of it.
#[test]
fn each_episode_answers_its_place_in_its_file() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season_with_a_double(&path, 2_640_000);
    let mut source = LocalCatalog::new(&path, NO_AGENT);

    let places: Vec<Place> = (1..=4)
        .map(|episode| source.place("default/shows", SERIES, (1, episode)))
        .collect();
    let double = |part| Place {
        part,
        parts: 2,
        duration: 2_640,
        last: (1, 2),
    };
    assert_eq!(
        places,
        [
            double(1),
            double(2),
            Place {
                duration: 1_320,
                ..Place::whole((1, 3))
            },
            Place::whole((1, 4)),
        ]
    );
}
