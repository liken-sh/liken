use rusqlite::Connection;

use super::super::{details, item, people};
use super::SCHEMA;

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
    let connection = shipped_schema();
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
        people::entries(connection, "test/films", "a-person").map(|_| ())
    });
}

#[test]
fn the_art_read_requires_the_shipped_item_index() {
    requires_index("file_items_library_item", |connection| {
        item::art(connection, "test/films", "movie:1").map(|_| ())
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
