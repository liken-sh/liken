// The identity read against the schema every agent loads: what a movie, an
// episode, and a trailer each record a play against.

use std::collections::BTreeMap;

use super::*;
use crate::catalog::Identity;

const FILMS: &str = "screening/films";
const SHOWS: &str = "screening/shows";

// One film the catalog holds under three names, which is what a sidecar
// with two provider ids and a folder key leaves behind.
fn a_named_film(path: &Path) {
    insert_movie(path, FILMS, "movie:tmdb:603", "Some Film", "some film");
    for alias in [
        "movie:tmdb:603",
        "movie:imdb:tt0133093",
        "movie:path:some-film-1999",
    ] {
        insert_alias(path, FILMS, alias, "movie:tmdb:603");
    }
}

// One series under two names, with the season every episode test uses.
fn a_named_series(path: &Path) {
    insert_series(path, SHOWS, SERIES, "Some Show", "some show");
    insert_alias(path, SHOWS, "series:tvdb:73739", SERIES);
    insert_alias(path, SHOWS, "series:path:some-show", SERIES);
}

fn identity(path: &Path, library: &str, selection: &Selection) -> Identity {
    SidecarSource::new(path, NO_AGENT).identity(library, selection)
}

#[test]
fn a_movie_records_against_every_name_the_catalog_holds_for_it() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_named_film(&path);

    let identity = identity(&path, FILMS, &movie_chosen());

    assert_eq!(
        identity.aliases,
        BTreeMap::from([
            ("tmdb".to_string(), "603".to_string()),
            ("imdb".to_string(), "tt0133093".to_string()),
            ("path".to_string(), "some-film-1999".to_string()),
        ])
    );
    assert_eq!((identity.season, identity.episode), (0, 0));
}

#[test]
fn an_episode_records_against_its_series_and_the_two_numbers() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_named_series(&path);

    let identity = identity(&path, SHOWS, &episode_chosen(2));

    assert_eq!(
        identity.aliases,
        BTreeMap::from([
            ("tvdb".to_string(), "73739".to_string()),
            ("path".to_string(), "some-show".to_string()),
        ])
    );
    assert_eq!((identity.season, identity.episode), (1, 2));
}

#[test]
fn a_trailer_records_against_no_work() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_named_film(&path);

    let identity = identity(
        &path,
        FILMS,
        &Selection::Trailer {
            id: "movie:tmdb:603".into(),
        },
    );

    assert_eq!(identity, Identity::default());
}

#[test]
fn a_work_the_catalog_names_nowhere_records_against_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(&path, FILMS, "movie:tmdb:603", "Some Film", "some film");

    assert_eq!(identity(&path, FILMS, &movie_chosen()), Identity::default());
}

#[test]
fn a_name_another_library_holds_is_not_this_works() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_named_film(&path);
    insert_alias(&path, SHOWS, "movie:tvdb:12345", "movie:tmdb:603");

    let identity = identity(&path, FILMS, &movie_chosen());

    assert!(!identity.aliases.contains_key("tvdb"));
}
