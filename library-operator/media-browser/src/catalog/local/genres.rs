// The two reads over the genres table. The `Genre` query's titles are
// one indexed range through `genres (library, genre)` per library,
// joined to that library's item table by `(library, item)`, then one
// sort in this process by rank first and the order's column newest
// first, because the ranges come off separate libraries.
// The genres strip's entries are two reads per library through the same
// index, the counts and the candidate posters, folded across libraries
// here for the same reason.

use std::collections::{BTreeMap, HashMap};

use rusqlite::Connection;

use super::{collect, item, item_table};
use crate::catalog::{GenreEntry, GenreSort, Order, Slot, Sort, TILE_CANDIDATES, unrepeated};

/// Every title across every library that carries the genre, in the
/// order the sort names, then by library and id so the order is the
/// same on every read.
// `Leads` is the order the genre wall opens in: the titles that lead
// with the genre first. The three plain sorts drop the rank and order by
// the release or the sort key alone.
pub fn titles(
    connection: &Connection,
    name: &str,
    order: Order,
    sort: GenreSort,
    kinds: &HashMap<String, String>,
) -> rusqlite::Result<Vec<Slot>> {
    let mut kinds: Vec<(&String, &String)> = kinds.iter().collect();
    kinds.sort();
    let mut found: Vec<(Keys, Slot)> = Vec::new();
    for (library, kind) in kinds {
        let Some(table) = item_table(kind) else {
            continue;
        };
        let sql = format!(
            "SELECT {columns}, {seasons} AS seasons, genres.rank, {table}.added, \
                    {table}.sort_key \
             FROM genres JOIN {table} ON {table}.library = genres.library \
             AND {table}.id = genres.item \
             WHERE genres.library = ?1 AND genres.genre = ?2",
            columns = item::COLUMNS,
            seasons = item::seasons(table),
        );
        let rows = collect(connection, &sql, &[&library, &name], |row| {
            Ok((
                Keys {
                    rank: row.get(item::WIDTH + 1)?,
                    added: row.get(item::WIDTH + 2)?,
                    sort_key: row.get(item::WIDTH + 3)?,
                },
                Slot {
                    seasons: row.get(item::WIDTH)?,
                    ..Slot::of(library, kind, item::title(row)?)
                },
            ))
        })?;
        found.extend(rows);
    }
    found.sort_by(|(keys, slot), (other_keys, other)| {
        ordered(sort, order, (keys, slot), (other_keys, other))
            .then_with(|| slot.library.cmp(&other.library))
            .then_with(|| slot.id.cmp(&other.id))
    });
    Ok(found.into_iter().map(|(_, slot)| slot).collect())
}

// The three keys a genre row carries beyond its slot, which the four
// orders compare on.
struct Keys {
    rank: i64,
    added: i64,
    sort_key: String,
}

// How one sort compares two genre rows. `Leads` compares the rank
// first and then the order's own column. The three plain sorts drop the
// rank.
fn ordered(
    sort: GenreSort,
    order: Order,
    one: (&Keys, &Slot),
    other: (&Keys, &Slot),
) -> std::cmp::Ordering {
    let (keys, slot) = one;
    let (other_keys, other_slot) = other;
    let newest = || match order {
        Order::Released => other_slot.released.cmp(&slot.released),
        Order::Added => other_keys.added.cmp(&keys.added),
    };
    match sort {
        GenreSort::Leads => keys.rank.cmp(&other_keys.rank).then_with(newest),
        GenreSort::By(Sort::Newest) => other_slot.released.cmp(&slot.released),
        GenreSort::By(Sort::Oldest) => slot.released.cmp(&other_slot.released),
        GenreSort::By(Sort::Title) => keys.sort_key.cmp(&other_keys.sort_key),
    }
}

// One candidate poster of a genre: whether the title leads with the
// genre, its release, its id, and the poster with the library it resolves
// against. The first three order the candidates of the whole namespace.
struct Candidate {
    main: bool,
    released: String,
    item: String,
    library: String,
    art: String,
}

// What one library's read adds to a genre: the count of its titles, and
// the candidate posters the tile draws from.
#[derive(Default)]
struct Found {
    titles: u64,
    candidates: Vec<Candidate>,
}

/// Every genre with its count of titles and its art, in name order. One
/// grouped read per library through the `(library, genre)` index, then
/// the fold across libraries in this process, because a genre spans
/// libraries and each library has one item table.
pub fn entries(
    connection: &Connection,
    kinds: &HashMap<String, String>,
) -> rusqlite::Result<Vec<GenreEntry>> {
    let mut kinds: Vec<(&String, &String)> = kinds.iter().collect();
    kinds.sort();
    let mut found: BTreeMap<String, Found> = BTreeMap::new();
    for (library, kind) in kinds {
        let Some(table) = item_table(kind) else {
            continue;
        };
        counted(connection, library, table, &mut found)?;
        for (genre, candidate) in candidates(connection, library, table)? {
            found.entry(genre).or_default().candidates.push(candidate);
        }
    }
    let mut entries: Vec<GenreEntry> = found
        .into_iter()
        .map(|(name, found)| GenreEntry {
            name,
            titles: found.titles,
            art: chosen(found.candidates),
        })
        .collect();
    unrepeated(&mut entries);
    Ok(entries)
}

// One library's count of the titles that carry each genre, added to the
// counts the earlier libraries answered.
fn counted(
    connection: &Connection,
    library: &str,
    table: &str,
    found: &mut BTreeMap<String, Found>,
) -> rusqlite::Result<()> {
    let sql = format!(
        "SELECT genres.genre, COUNT(DISTINCT genres.item) \
         FROM genres JOIN {table} ON {table}.library = genres.library \
         AND {table}.id = genres.item \
         WHERE genres.library = ?1 AND genres.genre != '' \
         GROUP BY genres.genre"
    );
    let rows = collect(connection, &sql, &[&library], |row| {
        Ok((row.get::<_, String>(0)?, row.get::<_, i64>(1)?))
    })?;
    for (genre, titles) in rows {
        found.entry(genre).or_default().titles += titles.max(0) as u64;
    }
    Ok(())
}

// The candidate posters one library holds for each of its genres: the
// titles that lead with the genre first, and the newest release next.
// The window numbers each genre's own rows, so one read answers every
// genre and no genre answers more rows than the tiles can use.
fn candidates(
    connection: &Connection,
    library: &str,
    table: &str,
) -> rusqlite::Result<Vec<(String, Candidate)>> {
    let sql = format!(
        "SELECT genre, rank, released, item, art FROM (\
           SELECT genres.genre AS genre, genres.rank AS rank, {table}.released AS released, \
                  genres.item AS item, {table}.art AS art, \
                  ROW_NUMBER() OVER (PARTITION BY genres.genre \
                    ORDER BY genres.rank != 0, {table}.released DESC, genres.item) AS place \
           FROM genres JOIN {table} ON {table}.library = genres.library \
           AND {table}.id = genres.item \
           WHERE genres.library = ?1 AND genres.genre != '' AND {table}.art != ''\
         ) WHERE place <= {TILE_CANDIDATES}"
    );
    collect(connection, &sql, &[&library], |row| {
        Ok((
            row.get::<_, String>(0)?,
            Candidate {
                main: row.get::<_, i64>(1)? == 0,
                released: row.get(2)?,
                item: row.get(3)?,
                library: library.to_string(),
                art: row.get(4)?,
            },
        ))
    })
}

// The candidates of one genre across every library, in the order the
// tile fills from: the titles that lead with the genre first, then the
// newest release, then the library and the id, so a read answers the
// same order every time.
fn chosen(mut candidates: Vec<Candidate>) -> Vec<(String, String)> {
    candidates.sort_by(|one, other| {
        other
            .main
            .cmp(&one.main)
            .then_with(|| other.released.cmp(&one.released))
            .then_with(|| one.library.cmp(&other.library))
            .then_with(|| one.item.cmp(&other.item))
    });
    candidates
        .into_iter()
        .take(TILE_CANDIDATES)
        .map(|candidate| (candidate.library, candidate.art))
        .collect()
}
