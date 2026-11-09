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
use crate::catalog::{Slot, Sort, Studio};

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

// The read behind the More from strip of a title's page: every title
// whose body names any of the page's studios, with the position of the
// name it matched. A page reads it when it opens, so it is one statement
// over both item tables, and each table is walked once. The names, as a
// JSON array in ?1, are the inner loop over each title's own studios.
// The CROSS JOINs hold that order: with the names outside, SQLite walks
// the whole table once for each name. The derived tables keep the item
// table's name on its rows, so the columns every list reads apply
// unchanged, and json_each's own `id` column never shadows the title's.
const NAMED: &str = "\
SELECT {columns}, position, 'movies' AS kind, library, {movie_seasons} AS seasons
FROM (SELECT names.position AS position, movies.*
      FROM movies CROSS JOIN json_each(movies.body, '$.studios') AS studio
      CROSS JOIN (SELECT key AS position, value AS name FROM json_each(?1)) AS names
      WHERE names.name = studio.value) AS movies
UNION ALL
SELECT {columns}, position, 'series' AS kind, library, {series_seasons} AS seasons
FROM (SELECT names.position AS position, series.*
      FROM series CROSS JOIN json_each(series.body, '$.studios') AS studio
      CROSS JOIN (SELECT key AS position, value AS name FROM json_each(?1)) AS names
      WHERE names.name = studio.value) AS series
ORDER BY position, released DESC, library, id";

fn named_sql() -> String {
    NAMED
        .replace("{columns}", item::COLUMNS)
        .replace("{movie_seasons}", &item::seasons("movies"))
        .replace("{series_seasons}", &item::seasons("series"))
}

/// The titles of each studio, one answer for each name in the order
/// given, each newest release first, then by library and id, the order
/// of the studio's wall. A title that names one studio twice is in its
/// answer once.
pub fn named(connection: &Connection, names: &[String]) -> rusqlite::Result<Vec<Studio>> {
    let mut studios: Vec<Studio> = names
        .iter()
        .map(|name| Studio {
            name: name.clone(),
            titles: Vec::new(),
        })
        .collect();
    if names.is_empty() {
        return Ok(studios);
    }
    let asked = serde_json::to_string(names).unwrap_or_default();
    let rows = collect(connection, &named_sql(), &[&asked], |row| {
        let position: i64 = row.get(item::WIDTH)?;
        let kind: String = row.get(item::WIDTH + 1)?;
        let library: String = row.get(item::WIDTH + 2)?;
        Ok((
            position,
            Slot {
                seasons: row.get(item::WIDTH + 3)?,
                ..Slot::of(&library, &kind, item::title(row)?)
            },
        ))
    })?;
    for (position, slot) in rows {
        let Some(studio) = studios.get_mut(position as usize) else {
            continue;
        };
        if studio.titles.last().map(|last| (&last.library, &last.id))
            != Some((&slot.library, &slot.id))
        {
            studio.titles.push(slot);
        }
    }
    Ok(studios)
}

/// The plan SQLite takes for the read, one line per step, which the plan
/// test reads.
#[cfg(test)]
pub fn plan(connection: &Connection) -> rusqlite::Result<Vec<String>> {
    collect(
        connection,
        &format!("EXPLAIN QUERY PLAN {}", named_sql()),
        &[&"[\"A Studio\"]"],
        |row| row.get(3),
    )
}
