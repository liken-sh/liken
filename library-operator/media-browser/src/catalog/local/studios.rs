// The read behind a studio's wall. The studios of a title are an array in
// its body, as the .nfo file's `studio` elements name them, and no table
// or index holds them. So the read walks one library's rows through the
// primary key's library prefix, the same rows a library's own wall reads,
// and json_each looks into each body. The libraries are read one by one
// and sorted together in this process, because a studio spans libraries
// and each library has one item table.
//
// A studio is the exact name the body spells. The .nfo file and the
// enrichment carry names and no provider's company id, so "Fox
// Searchlight Pictures" and "Searchlight Pictures", one company's names
// before and after a rename, are two walls.

use std::collections::HashMap;

use rusqlite::Connection;

use super::{collect, item, item_table};
use crate::catalog::{Slot, Sort};

/// Every title across every library whose body names the studio, in the
/// order the sort names, then by library and id so the order is the same
/// on every read.
pub fn titles(
    connection: &Connection,
    name: &str,
    sort: Sort,
    kinds: &HashMap<String, String>,
) -> rusqlite::Result<Vec<Slot>> {
    let mut kinds: Vec<(&String, &String)> = kinds.iter().collect();
    kinds.sort();
    let mut found: Vec<(String, Slot)> = Vec::new();
    for (library, kind) in kinds {
        let Some(table) = item_table(kind) else {
            continue;
        };
        let sql = format!(
            "SELECT {columns}, {seasons} AS seasons, {table}.sort_key FROM {table} \
             WHERE {table}.library = ?1 \
             AND EXISTS (SELECT 1 FROM json_each({table}.body, '$.studios') AS studio \
                         WHERE studio.value = ?2)",
            columns = item::COLUMNS,
            seasons = item::seasons(table),
        );
        let rows = collect(connection, &sql, &[&library, &name], |row| {
            Ok((
                row.get(item::WIDTH + 1)?,
                Slot {
                    seasons: row.get(item::WIDTH)?,
                    ..Slot::of(library, kind, item::title(row)?)
                },
            ))
        })?;
        found.extend(rows);
    }
    found.sort_by(|(key, slot), (other_key, other)| {
        let ordered = match sort {
            Sort::Newest => other.released.cmp(&slot.released),
            Sort::Oldest => slot.released.cmp(&other.released),
            Sort::Title => key.cmp(other_key),
        };
        ordered
            .then_with(|| slot.library.cmp(&other.library))
            .then_with(|| slot.id.cmp(&other.id))
    });
    Ok(found.into_iter().map(|(_, slot)| slot).collect())
}
