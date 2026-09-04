// The two reads over the genres table. The `Genre` query's titles are
// one indexed range through `genres (library, genre)` per library,
// joined to that library's item table by `(library, item)`, then one
// sort in this process by rank first and the order's column newest
// first, because the ranges come off separate libraries. The genres
// strip's entries are one grouped read per library through the same
// index, folded across libraries here for the same reason.

use std::collections::BTreeMap;

use rusqlite::Connection;

use super::{collect, item, item_table, people};
use crate::catalog::{GenreEntry, Order, Slot};

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

// What one library's read adds to a genre: the count of its titles, and
// the newest-released title that has art, held with its release so the
// fold across libraries can compare.
#[derive(Default)]
struct Found {
    titles: u64,
    released: String,
    library: String,
    art: String,
}

/// Every genre with its count of titles and its art, in name order. One
/// grouped read per library through the `(library, genre)` index, then
/// the fold across libraries in this process, because a genre spans
/// libraries and each library has one item table.
pub fn entries(connection: &Connection) -> rusqlite::Result<Vec<GenreEntry>> {
    let mut kinds: Vec<(String, String)> = people::kinds(connection)?.into_iter().collect();
    kinds.sort();
    let mut found: BTreeMap<String, Found> = BTreeMap::new();
    for (library, kind) in kinds {
        let Some(table) = item_table(&kind) else {
            continue;
        };
        // The MAX over the release of the titles that have art is the only
        // min or max in the read, so SQLite takes the bare art column from
        // that same row. A group whose titles all lack art answers NULL,
        // and the empty art beside it.
        let sql = format!(
            "SELECT genres.genre, COUNT(DISTINCT genres.item), \
                    MAX(CASE WHEN {table}.art != '' THEN {table}.released END), {table}.art \
             FROM genres JOIN {table} ON {table}.library = genres.library \
             AND {table}.id = genres.item \
             WHERE genres.library = ?1 AND genres.genre != '' \
             GROUP BY genres.genre"
        );
        let rows = collect(connection, &sql, &[&library], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, i64>(1)?,
                item::text(row, 2)?,
                row.get::<_, String>(3)?,
            ))
        })?;
        for (genre, titles, released, art) in rows {
            let entry = found.entry(genre).or_default();
            entry.titles += titles.max(0) as u64;
            if !art.is_empty() && (entry.art.is_empty() || released > entry.released) {
                entry.released = released;
                entry.library = library.clone();
                entry.art = art;
            }
        }
    }
    Ok(found
        .into_iter()
        .map(|(name, found)| GenreEntry {
            name,
            titles: found.titles,
            library: found.library,
            art: found.art,
        })
        .collect())
}
