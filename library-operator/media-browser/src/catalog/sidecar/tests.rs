use std::fs;
use std::path::{Path, PathBuf};

use rusqlite::Connection;
use tempfile::TempDir;

use super::SidecarSource;
use crate::catalog::{LibraryEntry, PlayItem, Presentation, Selection, Source};

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
            "INSERT INTO movies (library, id, kind, title, sort_key, released, art, slug) \
             VALUES (?, ?, 'movies', ?, ?, '1999', ?, ?)",
            (
                library,
                id,
                title,
                sort_key,
                format!("{id}.jpg"),
                format!("{}-1999", sort_key),
            ),
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
    insert_released_episode(path, library, id, series, season, episode, "");
}

// An episode with the release the catalog holds, which is what decides
// whether its presentation carries a date or a year.
fn insert_released_episode(
    path: &Path,
    library: &str,
    id: &str,
    series: &str,
    season: i64,
    episode: i64,
    released: &str,
) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO episodes (library, id, kind, title, series, season, episode, released, art, slug) \
             VALUES (?, ?, 'series', ?, ?, ?, ?, ?, ?, ?)",
            (
                library,
                id,
                format!("Episode {episode}"),
                series,
                season,
                episode,
                released,
                format!("{id}.jpg"),
                format!("s{season:02}e{episode:02}"),
            ),
        )
        .unwrap();
}

// One file on the volume, and the link that ties it to an item. The
// trickplay path is derived from the file's own, the way a scanner writes it.
fn insert_file(path: &Path, library: &str, file: &str, item: &str, kind: &str, role: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO files (library, path, trickplay, type, role) VALUES (?, ?, ?, ?, ?)",
            (library, file, format!("{file}.trickplay"), kind, role),
        )
        .unwrap();
    connection
        .execute(
            "INSERT INTO file_items (library, path, item) VALUES (?, ?, ?)",
            (library, file, item),
        )
        .unwrap();
}

// The one file a play reaches: the primary video of a title.
fn insert_main_file(path: &Path, library: &str, file: &str, item: &str) {
    insert_file(path, library, file, item, "video", "primary");
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

    assert!(source.play("default/films", &movie_chosen()).is_empty());
    assert!(source.play("default/shows", &episode_chosen(1)).is_empty());

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

// The series every episode test hangs under, and the two choices those
// tests resolve.
const SERIES: &str = "series:tvdb:73739";

fn movie_chosen() -> Selection {
    Selection::Movie {
        id: "movie:tmdb:603".into(),
    }
}

fn episode_chosen(episode: i64) -> Selection {
    Selection::Episode {
        series: SERIES.into(),
        season: 1,
        episode,
    }
}

// A season of three episodes, each with its main file, under a series row
// that carries the title an episode's presentation names.
fn a_season(path: &Path) {
    insert_series(path, "default/shows", SERIES, "Lost", "lost");
    for episode in 1..=3 {
        let id = format!("episode:tvdb:{episode}");
        insert_episode(path, "default/shows", &id, SERIES, 1, episode);
        insert_main_file(
            path,
            "default/shows",
            &format!("Lost/S01E{episode}.mkv"),
            &id,
        );
    }
}

#[test]
fn a_movie_plays_its_primary_video_file() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(
        &path,
        "default/films",
        "The Matrix/The Matrix.mkv",
        "movie:tmdb:603",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.play("default/films", &movie_chosen()),
        vec![PlayItem {
            path: "The Matrix/The Matrix.mkv".into(),
            slug: "matrix-1999".into(),
            presentation: Presentation {
                kind: "video".into(),
                hint: "movie".into(),
                title: "The Matrix".into(),
                year: 1999,
                art: "movie:tmdb:603.jpg".into(),
                trickplay: "The Matrix/The Matrix.mkv.trickplay".into(),
                ..Presentation::default()
            },
        }]
    );
}

#[test]
fn a_movie_plays_neither_its_art_nor_its_extras() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_file(
        &path,
        "default/films",
        "The Matrix/poster.jpg",
        "movie:tmdb:603",
        "image",
        "primary",
    );
    insert_file(
        &path,
        "default/films",
        "The Matrix/behind.mkv",
        "movie:tmdb:603",
        "video",
        "extra",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/films", &movie_chosen()).is_empty());
}

#[test]
fn a_movie_with_two_encodings_plays_one_of_them() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(&path, "default/films", "The Matrix/b.mkv", "movie:tmdb:603");
    insert_main_file(&path, "default/films", "The Matrix/a.mkv", "movie:tmdb:603");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/films", &movie_chosen());
    assert_eq!(items.len(), 1);
    assert_eq!(items[0].path, "The Matrix/a.mkv");
    assert_eq!(
        items[0].presentation.trickplay,
        "The Matrix/a.mkv.trickplay"
    );
}

#[test]
fn an_episode_plays_itself_and_the_rest_of_its_season() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    insert_episode(&path, "default/shows", "episode:tvdb:next", SERIES, 2, 1);
    insert_main_file(
        &path,
        "default/shows",
        "Lost/S02E1.mkv",
        "episode:tvdb:next",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(2));
    let paths: Vec<&str> = items.iter().map(|item| item.path.as_str()).collect();
    assert_eq!(paths, ["Lost/S01E2.mkv", "Lost/S01E3.mkv"]);
}

// The operator names a Play after the chosen item, so every item
// carries the slug its own row holds.
#[test]
fn every_item_carries_the_slug_its_row_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(2));
    let slugs: Vec<&str> = items.iter().map(|item| item.slug.as_str()).collect();
    assert_eq!(slugs, ["s01e02", "s01e03"]);
}

#[test]
fn an_episodes_presentation_names_its_series_and_its_numbers() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(3));
    assert_eq!(
        items[0].presentation,
        Presentation {
            kind: "video".into(),
            hint: "series".into(),
            series: "Lost".into(),
            season: 1,
            episode: 3,
            episode_title: "Episode 3".into(),
            art: "episode:tvdb:3.jpg".into(),
            trickplay: "Lost/S01E3.mkv.trickplay".into(),
            ..Presentation::default()
        }
    );
}

#[test]
fn an_episode_the_catalog_dates_carries_the_date_and_not_the_year() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_released_episode(&path, "default/shows", "e1", SERIES, 1, 1, "2004-09-22");
    insert_main_file(&path, "default/shows", "Lost/S01E1.mkv", "e1");
    insert_released_episode(&path, "default/shows", "e2", SERIES, 1, 2, "2004");
    insert_main_file(&path, "default/shows", "Lost/S01E2.mkv", "e2");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(1));
    assert_eq!(items[0].presentation.date, "2004-09-22");
    assert_eq!(items[0].presentation.year, 0);
    assert_eq!(items[1].presentation.date, "");
    assert_eq!(items[1].presentation.year, 2004);
}

#[test]
fn an_episode_under_no_series_row_names_no_series() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_episode(&path, "default/shows", "e1", SERIES, 1, 1);
    insert_main_file(&path, "default/shows", "Lost/S01E1.mkv", "e1");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let items = source.play("default/shows", &episode_chosen(1));
    assert_eq!(items[0].presentation.series, "");
    assert_eq!(items[0].presentation.year, 0);
}

#[test]
fn an_episode_with_no_file_of_its_own_plays_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series(&path, "default/shows", SERIES, "Lost", "lost");
    insert_episode(&path, "default/shows", "e1", SERIES, 1, 1);
    insert_episode(&path, "default/shows", "e2", SERIES, 1, 2);
    insert_main_file(&path, "default/shows", "Lost/S01E2.mkv", "e2");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/shows", &episode_chosen(1)).is_empty());
}

#[test]
fn a_choice_in_another_library_plays_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_season(&path);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_main_file(
        &path,
        "default/films",
        "The Matrix/The Matrix.mkv",
        "movie:tmdb:603",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.play("default/other", &movie_chosen()).is_empty());
    assert!(source.play("default/other", &episode_chosen(1)).is_empty());
}
