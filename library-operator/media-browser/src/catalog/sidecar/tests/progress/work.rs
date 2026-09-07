// The three reads that name a work or a library rather than the whole
// audience: where the audience last reached in one movie or series, where
// they reached in each episode, and the map of every film of one library
// they have a play of, which a wall draws its bars from.

use super::*;

#[test]
fn a_movie_answers_where_its_latest_play_reached() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "old", &["first"], (60, 6000, 10));
    film_play(&store, "new", &["first"], (900, 6000, 20));
    let mut source = source_over(&catalog, store);

    let progress = source.progress_of(FILMS, FILM, &names(&["first"])).unwrap();
    assert_eq!(progress.play, "new");
    assert_eq!(progress.position, 900);
}

#[test]
fn a_series_answers_its_latest_episode_row() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    show_play(&store, "earlier", (1300, 1320, 10), (1, 1));
    show_play(&store, "later", (200, 1320, 20), (1, 2));
    let mut source = source_over(&catalog, store);

    let progress = source.progress_of(SHOWS, SHOW, &names(&["first"])).unwrap();
    assert_eq!((progress.season, progress.episode), (1, 2));
}

#[test]
fn a_work_no_play_names_has_no_progress() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    let mut source = source_over(&catalog, store);

    assert_eq!(source.progress_of(FILMS, FILM, &names(&["first"])), None);
}

#[test]
fn a_play_the_audience_was_not_in_is_no_progress_of_the_work() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["second"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert_eq!(source.progress_of(FILMS, FILM, &names(&["first"])), None);
}

#[test]
fn every_episode_answers_its_latest_play_in_aired_order() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    for (play, numbers, reached) in [
        ("s02e01", (2, 1), (400, 1320, 30)),
        ("s01e02", (1, 2), (1300, 1320, 20)),
        ("s01e02-again", (1, 2), (60, 1320, 25)),
        ("s01e01", (1, 1), (1320, 1320, 10)),
    ] {
        show_play(&store, play, reached, numbers);
    }
    let mut source = source_over(&catalog, store);

    let rows = source.episode_progress(SHOWS, SHOW, &names(&["first"]));
    let played: Vec<(i64, i64, &str)> = rows
        .iter()
        .map(|row| (row.season, row.episode, row.play.as_str()))
        .collect();
    assert_eq!(
        played,
        [(1, 1, "s01e01"), (1, 2, "s01e02-again"), (2, 1, "s02e01")]
    );
}

#[test]
fn an_episode_the_audience_finished_reads_as_finished() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    show_play(&store, "s01e01", (1320, 1320, 10), (1, 1));
    show_play(&store, "s01e02", (60, 1320, 20), (1, 2));
    let mut source = source_over(&catalog, store);

    let finished: Vec<bool> = source
        .episode_progress(SHOWS, SHOW, &names(&["first"]))
        .iter()
        .map(|row| row.finished)
        .collect();
    assert_eq!(finished, [true, false]);
}

// A second film of the same library, with the alias a play names it by.
const OTHER_FILM: &str = "movie:tmdb:604";
const OTHER_ALIAS: (&str, &str) = ("tmdb", "604");

// A third film of the same library, which no play in these tests names.
const SPARE_FILM: &str = "movie:tmdb:605";

// A series in the film library, so the wall read proves it drops series
// ids by kind and not by library.
const FILMS_SHOW: &str = "series:tvdb:73740";
const FILMS_SHOW_ALIAS: (&str, &str) = ("tvdb", "73740");

// The three films and the one show one library holds, each with the alias
// a play names it by.
fn a_shelf(catalog: &Path) {
    a_film(catalog);
    insert_movie(catalog, FILMS, OTHER_FILM, "Another Film (1999)", "another");
    insert_alias(catalog, FILMS, OTHER_FILM, OTHER_FILM);
    insert_movie(catalog, FILMS, SPARE_FILM, "A Third Film (1999)", "third");
    insert_alias(catalog, FILMS, SPARE_FILM, SPARE_FILM);
    insert_series(catalog, FILMS, FILMS_SHOW, "Some Show", "some show");
    insert_alias(catalog, FILMS, FILMS_SHOW, FILMS_SHOW);
}

// The map a wall of one library reads, as pairs a test compares in order.
fn by_item(source: &mut dyn Source, library: &str, people: &[&str]) -> Vec<(String, Played)> {
    let mut pairs: Vec<(String, Played)> = source
        .progress_by_item(library, &names(people))
        .into_iter()
        .collect();
    pairs.sort_by(|one, other| one.0.cmp(&other.0));
    pairs
}

#[test]
fn every_film_of_one_library_the_audience_played_answers_its_latest_play() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_shelf(&catalog);
    film_play(&store, "old", &["first"], (60, 6000, 10));
    film_play(&store, "new", &["first"], (900, 6000, 20));
    insert_play(
        &store,
        "other",
        &[OTHER_ALIAS],
        &["first"],
        (5900, 6000, 30),
        (0, 0),
    );
    let mut source = source_over(&catalog, store);

    assert_eq!(
        by_item(&mut source, FILMS, &["first"]),
        [
            (
                FILM.to_string(),
                Played {
                    position: 900,
                    duration: 6000
                }
            ),
            (
                OTHER_FILM.to_string(),
                Played {
                    position: 5900,
                    duration: 6000
                }
            ),
        ]
    );
}

#[test]
fn a_series_the_audience_played_is_no_item_of_the_map() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_shelf(&catalog);
    insert_play(
        &store,
        "show",
        &[FILMS_SHOW_ALIAS],
        &["first"],
        (300, 1320, 10),
        (2, 5),
    );
    let mut source = source_over(&catalog, store);

    assert_eq!(by_item(&mut source, FILMS, &["first"]), []);
}

#[test]
fn a_film_no_play_of_the_audience_names_is_no_item_of_the_map() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_shelf(&catalog);
    film_play(&store, "one", &["second"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    let played = source.progress_by_item(FILMS, &names(&["first"]));
    assert!(!played.contains_key(SPARE_FILM), "{played:?}");
    assert!(played.is_empty(), "{played:?}");
}

#[test]
fn a_film_the_audience_finished_still_answers_its_play() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_shelf(&catalog);
    film_play(&store, "one", &["first"], (6000, 6000, 10));
    end_play(&store, "one", 30);
    let mut source = source_over(&catalog, store);

    assert_eq!(
        by_item(&mut source, FILMS, &["first"]),
        [(
            FILM.to_string(),
            Played {
                position: 6000,
                duration: 6000
            }
        )]
    );
}

#[test]
fn a_library_the_audience_played_nothing_in_answers_an_empty_map() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_shelf(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert!(
        source
            .progress_by_item(SHOWS, &names(&["first"]))
            .is_empty()
    );
}
