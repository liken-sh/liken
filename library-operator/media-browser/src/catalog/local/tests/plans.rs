use rusqlite::Connection;

use super::super::{details, files, item, people, progress};
use super::SCHEMA;
use super::progress::PROGRESS_SCHEMA;

fn shipped_schema() -> Connection {
    let connection = Connection::open_in_memory().unwrap();
    connection
        .execute_batch(&std::fs::read_to_string(SCHEMA).unwrap())
        .unwrap();
    connection
}

// Preparing the production read against the shipped schema checks both
// the index name and the columns. Removing that index then proves the read
// requires it, rather than leaving SQLite free to select another one.
fn requires_index(index: &str, read: impl Fn(&Connection) -> rusqlite::Result<()>) {
    requires_index_in(shipped_schema(), index, read);
}

fn requires_index_in(
    connection: Connection,
    index: &str,
    read: impl Fn(&Connection) -> rusqlite::Result<()>,
) {
    connection
        .execute(
            "INSERT INTO sets (library, id, title) VALUES ('test/films', 'set:1', 'A Set')",
            [],
        )
        .unwrap();
    read(&connection).unwrap();
    connection
        .execute_batch(&format!("DROP INDEX {index}"))
        .unwrap();
    assert!(read(&connection).is_err());
}

#[test]
fn the_alias_read_requires_the_shipped_path_index() {
    requires_index("contributor_aliases_library_path", |connection| {
        people::entries(connection, "test/films", "someone").map(|_| ())
    });
}

#[test]
fn the_art_read_requires_the_shipped_item_index() {
    requires_index("file_items_library_item", |connection| {
        item::art(connection, "test/films", "movie:1").map(|_| ())
    });
}

// The foot's read runs on every move across the wall. Without the item
// index SQLite walks every file of the library in path order to find the
// few of one episode.
#[test]
fn the_files_read_requires_the_shipped_item_index() {
    requires_index("file_items_library_item", |connection| {
        files::files(connection, "test/films", "movie:1").map(|_| ())
    });
}

// The shipped schema with the progress store's schema attached beside it,
// the way the source opens the two files.
fn with_progress(dir: &tempfile::TempDir) -> Connection {
    let path = dir.path().join("progress.db");
    Connection::open(&path)
        .unwrap()
        .execute_batch(&std::fs::read_to_string(PROGRESS_SCHEMA).unwrap())
        .unwrap();
    let connection = shipped_schema();
    connection
        .execute("ATTACH DATABASE ?1 AS progress", [path.to_string_lossy()])
        .unwrap();
    connection
}

// Every progress read carries the credits of each play's main file, one
// lookup for each play. Without the item index each lookup walks every file
// of the library, and a series with a play on each of 150 episodes, which
// one Pick up here writes, takes seconds to read.
fn progress_requires_index(read: impl Fn(&Connection, &[String]) -> rusqlite::Result<()>) {
    let dir = tempfile::tempdir().unwrap();
    let people = ["someone".to_string()];
    requires_index_in(
        with_progress(&dir),
        "file_items_library_item",
        |connection| read(connection, &people),
    );
}

#[test]
fn the_continue_watching_read_requires_the_shipped_item_index() {
    progress_requires_index(|connection, people| progress::resumes(connection, people).map(|_| ()));
}

#[test]
fn the_plays_read_requires_the_shipped_item_index() {
    progress_requires_index(|connection, people| {
        progress::plays(connection, "test/shows", "series:1", people).map(|_| ())
    });
}

#[test]
fn the_episode_progress_read_requires_the_shipped_item_index() {
    progress_requires_index(|connection, people| {
        progress::episodes(connection, "test/shows", "series:1", people).map(|_| ())
    });
}

#[test]
fn the_set_read_requires_the_shipped_members_index() {
    requires_index("movies_library_set_id", |connection| {
        details::set(connection, "test/films", "set:1").map(|_| ())
    });
}

#[test]
fn the_people_pool_has_a_covering_credit_index() {
    let connection = shipped_schema();
    let mut statement = connection
        .prepare("PRAGMA index_info(credits_library_contributor_item)")
        .unwrap();
    let columns: Vec<String> = statement
        .query_map([], |row| row.get(2))
        .unwrap()
        .collect::<rusqlite::Result<_>>()
        .unwrap();

    assert_eq!(columns, ["library", "contributor", "item"]);
}
