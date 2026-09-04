// The read behind the `Genre` query: one indexed range through
// `genres (library, genre)` per library, joined to that library's item
// table by `(library, item)`, then one sort in this process by rank
// first and the order's column newest first, because the ranges come off
// separate libraries.

use rusqlite::Connection;

use super::{collect, item, item_table, people};
use crate::catalog::{Order, Slot};

/// Every title across every library that carries the genre, the titles
/// that lead with it first, then newest by the order's column, then by
/// library and id so the order is the same on every read.
pub fn titles(connection: &Connection, name: &str, order: Order) -> rusqlite::Result<Vec<Slot>> {
    let mut kinds: Vec<(String, String)> = people::kinds(connection)?.into_iter().collect();
    kinds.sort();
    let mut found: Vec<(i64, i64, Slot)> = Vec::new();
    for (library, kind) in kinds {
        let Some(table) = item_table(&kind) else {
            continue;
        };
        let sql = format!(
            "SELECT {columns}, genres.rank, {table}.added \
             FROM genres JOIN {table} ON {table}.library = genres.library \
             AND {table}.id = genres.item \
             WHERE genres.library = ?1 AND genres.genre = ?2",
            columns = item::COLUMNS,
        );
        let rows = collect(connection, &sql, &[&library, &name], |row| {
            Ok((
                row.get::<_, i64>(6)?,
                row.get::<_, i64>(7)?,
                Slot::of(&library, &kind, item::title(row)?),
            ))
        })?;
        found.extend(rows);
    }
    found.sort_by(|(rank, added, slot), (other_rank, other_added, other)| {
        rank.cmp(other_rank)
            .then_with(|| match order {
                Order::Released => other.released.cmp(&slot.released),
                Order::Added => other_added.cmp(added),
            })
            .then_with(|| slot.library.cmp(&other.library))
            .then_with(|| slot.id.cmp(&other.id))
    });
    Ok(found.into_iter().map(|(_, _, slot)| slot).collect())
}
