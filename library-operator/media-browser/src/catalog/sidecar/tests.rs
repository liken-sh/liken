mod draw;
mod franchises;
mod lists;
mod pages;
mod people;
mod plans;
mod plays;
mod read_scope;
mod recent;
mod recent_queries;
mod series;

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::Ordering;

use rusqlite::Connection;
use tempfile::TempDir;

use super::SidecarSource;
use crate::catalog::{
    Answer, Credit, FileFacts, Fold, InSeries, LibraryEntry, MovieDetails, PlayItem, Presentation,
    Query, Selection, SeriesDetails, Slot, Source,
};

// One library's wall, as the libraries strip opens it.
fn library(name: &str) -> Query {
    Query::Library {
        library: name.into(),
    }
}

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

// One movie with the two columns the recency reads order by.
fn insert_added_movie(path: &Path, library: &str, id: &str, released: &str, added: i64) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key, released, added, art) \
             VALUES (?, ?, 'movies', ?, ?, ?, ?, ?)",
            (
                library,
                id,
                format!("Film {id}"),
                format!("film {id}"),
                released,
                added,
                format!("{id}.jpg"),
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

// The art files beside a series, as the catalog's own two columns: the
// primary art, and the list of every art file. An episode with no still
// of its own draws out of these.
fn set_series_art(path: &Path, library: &str, id: &str, art: &str, arts: &[&str]) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "UPDATE series SET art = ?, arts = ? WHERE library = ? AND id = ?",
            (art, serde_json::to_string(arts).unwrap(), library, id),
        )
        .unwrap();
}

// The gap the fallback is about: an episode the catalog holds no still
// for, its art column empty and its list of art files empty with it.
fn clear_episode_art(path: &Path, library: &str, id: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "UPDATE episodes SET art = '', arts = '[]' WHERE library = ? AND id = ?",
            (library, id),
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
// One person's entry in one library's store. `files` is the biography and
// the headshot, in that order, as the contributor facts write them.
fn insert_contributor(
    path: &Path,
    library: &str,
    contributor: &str,
    name: &str,
    files: (i64, i64),
) {
    let (biography, headshot) = files;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO contributors (library, path, name, born, died, biography, headshot) \
             VALUES (?, ?, ?, '1950-01-02', '', ?, ?)",
            (library, contributor, name, biography, headshot),
        )
        .unwrap();
}

// One credited person on one title. `billing` is the key beside the item,
// and the crew hold the billing after the cast.
fn insert_credit(
    path: &Path,
    library: &str,
    item: &str,
    billing: i64,
    person: (&str, &str),
    part: (&str, &str),
) {
    let (contributor, name) = person;
    let (part, role) = part;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO credits (library, item, billing, contributor, name, part, role) \
             VALUES (?, ?, ?, ?, ?, ?, ?)",
            (library, item, billing, contributor, name, part, role),
        )
        .unwrap();
}

// One movie with everything a page reads: the duration, the set it
// belongs to, and a body in the shape the scanner writes.
fn insert_page(path: &Path, library: &str, id: &str, released: &str, set_id: &str, body: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key, released, art, slug, \
             duration, body, set_id) \
             VALUES (?, ?, 'movies', ?, ?, ?, ?, ?, 6720, ?, ?)",
            (
                library,
                id,
                format!("Film {id}"),
                format!("film {id}"),
                released,
                format!("{id}.jpg"),
                format!("film-{id}"),
                body,
                set_id,
            ),
        )
        .unwrap();
}

// The body a page reads every field out of.
const BODY: &str = r#"{"plot":"A plot.","tagline":"One line.","contentRating":"PG",
    "genres":["Drama","Mystery"],"directors":["A Director"],
    "writers":["A Writer","Another"],"studios":["A Studio"],
    "ratings":{"imdb":6.5,"metacritic":80,"themoviedb":7.1,"tomatometerallcritics":83},
    "cast":[{"name":"A Player","role":"The Part"}]}"#;

// One file on the volume with the technical columns the foot of a page
// draws, and the link that ties it to an item.
fn insert_video_file(path: &Path, library: &str, file: &str, item: &str, language: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO files (library, path, type, role, container, video_codec, \
             audio_codec, width, height, size_bytes, language) \
             VALUES (?, ?, 'video', 'primary', 'mkv', 'x265', 'AC3', 1920, 804, \
             4200000000, ?)",
            (library, file, language),
        )
        .unwrap();
    connection
        .execute(
            "INSERT INTO file_items (library, path, item) VALUES (?, ?, ?)",
            (library, file, item),
        )
        .unwrap();
}

// One series with everything its page reads: the release, the body in the
// shape the scanner writes, and the title a person reads.
fn insert_series_page(path: &Path, library: &str, id: &str, released: &str, body: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO series (library, id, kind, title, sort_key, released, art, body) \
             VALUES (?, ?, 'series', ?, ?, ?, ?, ?)",
            (
                library,
                id,
                format!("Serial {id}"),
                format!("serial {id}"),
                released,
                format!("{id}.jpg"),
                body,
            ),
        )
        .unwrap();
}

// One episode with the columns and the body its still and the header
// read.
fn insert_episode_page(
    path: &Path,
    library: &str,
    id: &str,
    series: &str,
    numbers: (i64, i64),
    released: &str,
    body: &str,
) {
    let (season, episode) = numbers;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO episodes (library, id, kind, title, series, season, episode, \
             released, art, duration, body) \
             VALUES (?, ?, 'series', ?, ?, ?, ?, ?, ?, 2760, ?)",
            (
                library,
                id,
                format!("Segment {episode}"),
                series,
                season,
                episode,
                released,
                format!("{id}.jpg"),
                body,
            ),
        )
        .unwrap();
}

fn insert_set(path: &Path, library: &str, id: &str, title: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO sets (library, id, kind, title, sort_key, released) \
             VALUES (?, ?, 'movies', ?, ?, '1994')",
            (library, id, title, title.to_lowercase()),
        )
        .unwrap();
}
