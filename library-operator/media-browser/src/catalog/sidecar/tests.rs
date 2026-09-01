use std::fs;
use std::path::{Path, PathBuf};

use rusqlite::Connection;
use tempfile::TempDir;

use super::SidecarSource;
use crate::catalog::{LibraryEntry, Source};

// The tests build their fixture from the schema every agent loads, the
// one file in corrosion/schema, so a schema change tests the reads
// against itself.
const SCHEMA: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../corrosion/schema/catalog.sql"
);

// No agent answers on port 1, so the read tests exercise the file
// alone and the stream threads only back off.
const NO_AGENT: &str = "http://127.0.0.1:1";

fn fixture(dir: &TempDir) -> PathBuf {
    let path = dir.path().join("catalog.db");
    let connection = Connection::open(&path).unwrap();
    let schema = fs::read_to_string(SCHEMA).unwrap();
    connection.execute_batch(&schema).unwrap();
    path
}

fn insert_movie(path: &Path, library: &str, id: &str, title: &str, sort_key: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key, released, art) \
             VALUES (?, ?, 'movies', ?, ?, '1999', ?)",
            (library, id, title, sort_key, format!("{id}.jpg")),
        )
        .unwrap();
}

fn insert_series(path: &Path, library: &str, id: &str, title: &str, sort_key: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO series (library, id, kind, title, sort_key, released, art) \
             VALUES (?, ?, 'series', ?, ?, '2004', ?)",
            (library, id, title, sort_key, format!("{id}.jpg")),
        )
        .unwrap();
}

fn insert_episode(path: &Path, library: &str, id: &str, series: &str, season: i64, episode: i64) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO episodes (library, id, kind, title, series, season, episode) \
             VALUES (?, ?, 'series', ?, ?, ?, ?)",
            (
                library,
                id,
                format!("Episode {episode}"),
                series,
                season,
                episode,
            ),
        )
        .unwrap();
}

#[test]
fn libraries_come_back_counted_and_ordered_by_name() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series(&path, "default/shows", "series:tvdb:73739", "Lost", "lost");
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:604",
        "The Matrix Reloaded",
        "matrix reloaded",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.libraries(),
        vec![
            LibraryEntry {
                library: "default/films".into(),
                kind: "movies".into(),
                items: 2,
            },
            LibraryEntry {
                library: "default/shows".into(),
                kind: "series".into(),
                items: 1,
            },
        ]
    );
}

#[test]
fn titles_come_back_in_sort_key_order() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:604",
        "The Matrix Reloaded",
        "matrix reloaded",
    );
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_movie(
        &path,
        "default/other",
        "movie:tmdb:1",
        "Elsewhere",
        "elsewhere",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let titles = source.titles("default/films", "movies");
    let ids: Vec<&str> = titles.iter().map(|title| title.id.as_str()).collect();
    assert_eq!(ids, ["movie:tmdb:603", "movie:tmdb:604"]);
    assert_eq!(titles[0].title, "The Matrix");
    assert_eq!(titles[0].released, "1999");
    assert_eq!(titles[0].art, "movie:tmdb:603.jpg");
}

#[test]
fn titles_read_the_table_the_kind_names() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/mixed",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_series(&path, "default/mixed", "series:tvdb:73739", "Lost", "lost");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let series = source.titles("default/mixed", "series");
    assert_eq!(series.len(), 1);
    assert_eq!(series[0].id, "series:tvdb:73739");
}

#[test]
fn titles_refuse_a_kind_that_names_no_item_table() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.titles("default/films", "episodes").is_empty());
    assert!(
        source
            .titles("default/films", "movies; DROP TABLE movies")
            .is_empty()
    );
    assert!(source.titles("default/films", "").is_empty());
}

#[test]
fn seasons_come_back_distinct_and_ordered() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_episode(&path, "default/shows", "episode:tvdb:2", series, 2, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:1a", series, 1, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:1b", series, 1, 2);
    insert_episode(
        &path,
        "default/shows",
        "episode:tvdb:other",
        "series:tvdb:999",
        5,
        1,
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.seasons("default/shows", series), vec![1, 2]);
    assert!(source.seasons("default/empty", series).is_empty());
}

#[test]
fn episodes_come_back_in_episode_order_for_one_season() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_episode(&path, "default/shows", "episode:tvdb:1b", series, 1, 2);
    insert_episode(&path, "default/shows", "episode:tvdb:1a", series, 1, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:2", series, 2, 1);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let episodes = source.episodes("default/shows", series, 1);
    let ids: Vec<&str> = episodes.iter().map(|episode| episode.id.as_str()).collect();
    assert_eq!(ids, ["episode:tvdb:1a", "episode:tvdb:1b"]);
    assert_eq!(episodes[0].season, 1);
    assert_eq!(episodes[0].episode, 1);
    assert_eq!(episodes[0].title, "Episode 1");
}

#[test]
fn a_missing_file_reads_as_empty_until_the_sidecar_writes_it() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("catalog.db");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.libraries().is_empty());
    assert!(source.titles("default/films", "movies").is_empty());
    assert!(
        source
            .seasons("default/shows", "series:tvdb:73739")
            .is_empty()
    );
    assert!(
        source
            .episodes("default/shows", "series:tvdb:73739", 1)
            .is_empty()
    );

    fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    assert_eq!(source.libraries().len(), 1);
}

#[test]
fn a_file_without_the_schema_reads_as_empty() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("catalog.db");
    Connection::open(&path).unwrap();

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.libraries().is_empty());
    assert!(source.titles("default/films", "movies").is_empty());
}

#[test]
fn a_fresh_source_reports_no_change() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(!source.changed());
}
