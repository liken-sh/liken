// The continue-watching read over a real catalog file and a real progress
// file, and the fixtures every progress read is tested from.

mod store;
mod work;

use super::*;
use crate::catalog::{Played, Progress, Resume};

// The progress store's own schema, the one file every agent of the progress
// cluster loads, so a schema change tests the reads against itself.
const PROGRESS_SCHEMA: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../corrosion/progress-schema/progress.sql"
);

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";
const FILM: &str = "movie:tmdb:603";
const SHOW: &str = "series:tvdb:73739";

// The provider ids a play carries for each of the two works. The reads
// resolve them against the catalog's aliases.
const FILM_ALIAS: (&str, &str) = ("tmdb", "603");
const SHOW_ALIAS: (&str, &str) = ("tvdb", "73739");

fn progress_fixture(dir: &TempDir) -> PathBuf {
    let path = dir.path().join("progress.db");
    let connection = Connection::open(&path).unwrap();
    let schema = fs::read_to_string(PROGRESS_SCHEMA).unwrap();
    connection.execute_batch(&schema).unwrap();
    path
}

// One play of one work: the aliases that name it, the people who were in
// the room, where it reached, and the season and episode a series play
// carries.
fn insert_play(
    path: &Path,
    play: &str,
    aliases: &[(&str, &str)],
    people: &[&str],
    reached: (i64, i64, i64),
    numbers: (i64, i64),
) {
    let (position, duration, recorded) = reached;
    let (season, episode) = numbers;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO plays (play, player, library, position, duration, recorded, \
             season, episode, ended) VALUES (?, 'den', 'films', ?, ?, ?, ?, ?, 0)",
            (play, position, duration, recorded, season, episode),
        )
        .unwrap();
    for (provider, id) in aliases {
        connection
            .execute(
                "INSERT INTO play_aliases (play, provider, id) VALUES (?, ?, ?)",
                (play, provider, id),
            )
            .unwrap();
    }
    for person in people {
        connection
            .execute(
                "INSERT INTO play_people (play, person) VALUES (?, ?)",
                (play, person),
            )
            .unwrap();
    }
}

// One play of the film, which carries no season and no episode.
fn film_play(store: &Path, play: &str, people: &[&str], reached: (i64, i64, i64)) {
    insert_play(store, play, &[FILM_ALIAS], people, reached, (0, 0));
}

// One play of one episode of the show. An episode play keys on the series'
// aliases and its own numbers.
fn show_play(store: &Path, play: &str, reached: (i64, i64, i64), numbers: (i64, i64)) {
    insert_play(store, play, &[SHOW_ALIAS], &["first"], reached, numbers);
}

// Close one play, the way the store does when a Play ends.
fn end_play(path: &Path, play: &str, ended: i64) {
    Connection::open(path)
        .unwrap()
        .execute("UPDATE plays SET ended = ? WHERE play = ?", (ended, play))
        .unwrap();
}

fn insert_alias(path: &Path, library: &str, alias: &str, item: &str) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO aliases (library, alias, item, source) VALUES (?, ?, ?, 'nfo')",
            (library, alias, item),
        )
        .unwrap();
}

// One library of one film, with the alias a play names it by.
fn a_film(catalog: &Path) {
    insert_movie(catalog, FILMS, FILM, "Some Film (1999)", "some film");
    insert_alias(catalog, FILMS, FILM, FILM);
}

// One library of one show, with the alias a play names it by.
fn a_show(catalog: &Path) {
    insert_series(catalog, SHOWS, SHOW, "Some Show", "some show");
    insert_alias(catalog, SHOWS, SHOW, SHOW);
}

// The source both files are read through.
fn source_over(catalog: &Path, store: PathBuf) -> SidecarSource {
    SidecarSource::new(catalog, NO_AGENT).with_progress(store, NO_AGENT)
}

fn watching(source: &mut dyn Source, people: &[&str]) -> Vec<Resume> {
    source.continue_watching(&names(people))
}

fn names(people: &[&str]) -> Vec<String> {
    people.iter().map(|name| (*name).to_string()).collect()
}

#[test]
fn a_film_the_audience_started_comes_back_as_a_resume() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert_eq!(
        watching(&mut source, &["first"]),
        [Resume {
            library: FILMS.into(),
            kind: "movie".into(),
            id: FILM.into(),
            title: "Some Film (1999)".into(),
            released: "1999".into(),
            art: format!("{FILM}.jpg"),
            progress: Progress {
                play: "one".into(),
                position: 600,
                duration: 6000,
                finished: false,
                running: true,
                recorded: 10,
                season: 0,
                episode: 0,
            },
            exact: true,
        }]
    );
}

#[test]
fn a_play_no_library_holds_is_skipped() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    insert_play(
        &store,
        "gone",
        &[("tmdb", "1")],
        &["first"],
        (60, 600, 10),
        (0, 0),
    );
    let mut source = source_over(&catalog, store);

    assert_eq!(watching(&mut source, &["first"]), []);
}

#[test]
fn one_work_answers_every_play_of_it_newest_first() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "old", &["first"], (60, 6000, 10));
    film_play(&store, "new", &["first"], (5900, 6000, 20));
    let mut source = source_over(&catalog, store);

    let resumes = watching(&mut source, &["first"]);
    let plays: Vec<&str> = resumes
        .iter()
        .map(|resume| resume.progress.play.as_str())
        .collect();
    assert_eq!(plays, ["new", "old"]);
    assert!(resumes[0].progress.finished);
}

#[test]
fn a_play_that_names_the_audience_and_nobody_more_is_exact() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first", "second"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert!(!watching(&mut source, &["first"])[0].exact);
    assert!(watching(&mut source, &["first", "second"])[0].exact);
}

#[test]
fn a_play_carrying_two_aliases_of_one_work_answers_once() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    insert_alias(&catalog, FILMS, "movie:imdb:tt0133093", FILM);
    let aliases = [FILM_ALIAS, ("imdb", "tt0133093")];
    insert_play(&store, "one", &aliases, &["first"], (600, 6000, 10), (0, 0));
    let mut source = source_over(&catalog, store);

    assert_eq!(watching(&mut source, &["first"]).len(), 1);
}

#[test]
fn a_work_two_libraries_hold_resumes_in_each() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    insert_movie(&catalog, "default/spares", FILM, "Some Film (1999)", "some");
    insert_alias(&catalog, "default/spares", FILM, FILM);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    let libraries: Vec<String> = watching(&mut source, &["first"])
        .into_iter()
        .map(|resume| resume.library)
        .collect();
    assert_eq!(libraries, [FILMS, "default/spares"]);
}

#[test]
fn a_play_with_more_people_than_the_audience_still_counts() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first", "second"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert_eq!(watching(&mut source, &["first"]).len(), 1);
    assert_eq!(watching(&mut source, &["first", "second"]).len(), 1);
}

#[test]
fn a_play_missing_one_of_the_audience_does_not_count() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    assert_eq!(watching(&mut source, &["first", "second"]), []);
}

#[test]
fn an_empty_audience_reads_the_plays_that_name_nobody() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "named", &["first"], (60, 6000, 20));
    film_play(&store, "alone", &[], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    let resumes = watching(&mut source, &[]);
    assert_eq!(resumes.len(), 1);
    assert_eq!(resumes[0].progress.play, "alone");
    assert!(resumes[0].exact);
}

#[test]
fn resumes_come_back_newest_first() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    a_show(&catalog);
    film_play(&store, "film", &["first"], (600, 6000, 10));
    show_play(&store, "show", (300, 1320, 20), (2, 5));
    let mut source = source_over(&catalog, store);

    let kinds: Vec<String> = watching(&mut source, &["first"])
        .into_iter()
        .map(|resume| resume.kind)
        .collect();
    assert_eq!(kinds, ["series", "movie"]);
}

#[test]
fn a_series_answers_its_latest_episode_play_first() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    show_play(&store, "earlier", (1300, 1320, 10), (2, 4));
    show_play(&store, "later", (300, 1320, 20), (2, 5));
    let mut source = source_over(&catalog, store);

    let resumes = watching(&mut source, &["first"]);
    assert_eq!(resumes.len(), 2);
    assert_eq!(resumes[0].id, SHOW);
    assert_eq!(
        (resumes[0].progress.season, resumes[0].progress.episode),
        (2, 5)
    );
}

#[test]
fn a_closed_play_no_longer_runs() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    end_play(&store, "one", 30);
    let mut source = source_over(&catalog, store);

    assert!(!watching(&mut source, &["first"])[0].progress.running);
}
