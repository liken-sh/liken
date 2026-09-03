// The reads behind the people of a catalog: one title's credits, one
// person's entry, and every title that person is credited in. A person
// has one row per library, and the ids in contributor_aliases are what
// join two libraries' copies of them.

use std::collections::HashMap;

use rusqlite::Connection;

use super::{collect, item_table};
use crate::catalog::{CreditSlot, Credits, Person, Work};

/// One title's credits, split into the three stripes, as a list of one.
/// The join to contributors is what says whether a slot has a headshot,
/// and a credit with no contributor path never matches an entry.
pub fn credits(connection: &Connection, library: &str, id: &str) -> rusqlite::Result<Vec<Credits>> {
    let sql = "SELECT credits.part, credits.name, credits.role, credits.contributor, \
                      COALESCE(contributors.headshot, 0) \
               FROM credits \
               LEFT JOIN contributors ON contributors.library = credits.library \
               AND contributors.path = credits.contributor \
               WHERE credits.library = ? AND credits.item = ? \
               ORDER BY credits.billing";
    let rows = collect(connection, sql, &[&library, &id], |row| {
        let contributor: String = row.get(3)?;
        let headshot = !contributor.is_empty() && row.get::<_, i64>(4)? != 0;
        Ok((
            row.get::<_, String>(0)?,
            CreditSlot {
                name: row.get(1)?,
                role: row.get(2)?,
                contributor,
                headshot,
            },
        ))
    })?;

    let mut credits = Credits::default();
    for (part, slot) in rows {
        match part.as_str() {
            "director" => credits.directors.push(slot),
            "writer" => credits.writers.push(slot),
            "actor" => credits.cast.push(slot),
            _ => {}
        }
    }
    Ok(vec![credits])
}

/// One person, as a list of one, or an empty list where that library
/// holds no entry under that path. The dates come off the opening
/// library's row, and each of the two files comes off the first entry
/// that holds it, the opening library first.
pub fn person(connection: &Connection, library: &str, path: &str) -> rusqlite::Result<Vec<Person>> {
    let sql = "SELECT name, born, died, biography, headshot \
               FROM contributors WHERE library = ? AND path = ?";
    let mut found = collect(connection, sql, &[&library, &path], |row| {
        Ok(Person {
            library: library.to_string(),
            path: path.to_string(),
            name: row.get(0)?,
            born: row.get(1)?,
            died: row.get(2)?,
            biography: row.get::<_, i64>(3)? != 0,
            headshot: row.get::<_, i64>(4)? != 0,
            biography_library: String::new(),
            biography_path: String::new(),
            headshot_library: String::new(),
            headshot_path: String::new(),
        })
    })?;

    let Some(person) = found.first_mut() else {
        return Ok(Vec::new());
    };
    if person.biography {
        person.biography_library = library.to_string();
        person.biography_path = path.to_string();
    }
    if person.headshot {
        person.headshot_library = library.to_string();
        person.headshot_path = path.to_string();
    }

    if !(person.biography && person.headshot) {
        for (other, elsewhere) in entries(connection, library, path)? {
            if person.biography && person.headshot {
                break;
            }
            if other == library && elsewhere == path {
                continue;
            }
            let files = collect(
                connection,
                "SELECT biography, headshot FROM contributors WHERE library = ? AND path = ?",
                &[&other, &elsewhere],
                |row| Ok((row.get::<_, i64>(0)? != 0, row.get::<_, i64>(1)? != 0)),
            )?;
            let Some((biography, headshot)) = files.into_iter().next() else {
                continue;
            };
            if biography && !person.biography {
                person.biography = true;
                person.biography_library = other.clone();
                person.biography_path = elsewhere.clone();
            }
            if headshot && !person.headshot {
                person.headshot = true;
                person.headshot_library = other;
                person.headshot_path = elsewhere;
            }
        }
    }
    Ok(found)
}

/// Every title the person is credited in, across every library that holds
/// them, newest release first and a title with no release last. A title
/// the person holds more than one credit on is one row, with the parts
/// joined.
pub fn works(connection: &Connection, library: &str, path: &str) -> rusqlite::Result<Vec<Work>> {
    let kinds = kinds(connection)?;
    let mut works: Vec<Work> = Vec::new();
    let mut parts: Vec<Vec<(u8, String)>> = Vec::new();
    let mut placed: HashMap<(String, String), usize> = HashMap::new();

    for (other, elsewhere) in entries(connection, library, path)? {
        let Some(kind) = kinds.get(&other) else {
            continue;
        };
        let Some(table) = item_table(kind) else {
            continue;
        };
        // The library's kind names the item table, so a credit joins to
        // the one table that holds its titles.
        let sql = format!(
            "SELECT credits.item, credits.part, credits.role, \
                    {table}.title, {table}.released, {table}.art \
             FROM credits JOIN {table} ON {table}.library = credits.library \
             AND {table}.id = credits.item \
             WHERE credits.library = ? AND credits.contributor = ? \
             ORDER BY credits.billing"
        );
        let rows = collect(connection, &sql, &[&other, &elsewhere], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, String>(3)?,
                row.get::<_, String>(4)?,
                row.get::<_, String>(5)?,
            ))
        })?;

        for (id, part, role, title, released, art) in rows {
            let Some(named) = named(&part, &role) else {
                continue;
            };
            let slot = *placed
                .entry((other.clone(), id.clone()))
                .or_insert_with(|| {
                    works.push(Work {
                        library: other.clone(),
                        kind: kind.clone(),
                        id,
                        title,
                        released,
                        art,
                        parts: String::new(),
                    });
                    parts.push(Vec::new());
                    works.len() - 1
                });
            parts[slot].push(named);
        }
    }

    for (work, mut list) in works.iter_mut().zip(parts) {
        list.sort_by_key(|(rank, _)| *rank);
        let joined: Vec<String> = list.into_iter().map(|(_, named)| named).collect();
        work.parts = joined.join(", ");
    }
    works.sort_by(|one, other| {
        one.released
            .is_empty()
            .cmp(&other.released.is_empty())
            .then_with(|| other.released.cmp(&one.released))
            .then_with(|| one.title.cmp(&other.title))
    });
    Ok(works)
}

// What one credit reads as in a person's wall, and where it sorts among
// the parts of one title. A part the credits fact never writes reads as
// nothing at all.
fn named(part: &str, role: &str) -> Option<(u8, String)> {
    match part {
        "director" => Some((0, "Director".to_string())),
        "writer" => Some((1, "Writer".to_string())),
        "actor" if role.is_empty() => Some((2, "Actor".to_string())),
        "actor" => Some((2, format!("as {role}"))),
        _ => None,
    }
}

// Every entry of one person, the opening library's first and then every
// other library that holds an id this person's aliases carry.
fn entries(
    connection: &Connection,
    library: &str,
    path: &str,
) -> rusqlite::Result<Vec<(String, String)>> {
    let mut found = vec![(library.to_string(), path.to_string())];
    let ids = collect(
        connection,
        "SELECT scheme, id FROM contributor_aliases WHERE library = ? AND path = ? \
         ORDER BY scheme, id",
        &[&library, &path],
        |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
    )?;
    for (scheme, id) in ids {
        let elsewhere = collect(
            connection,
            "SELECT library, path FROM contributor_aliases \
             WHERE scheme = ? AND id = ? AND library <> ? ORDER BY library, path",
            &[&scheme, &id, &library],
            |row| Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?)),
        )?;
        for entry in elsewhere {
            if !found.contains(&entry) {
                found.push(entry);
            }
        }
    }
    Ok(found)
}

// Each library's kind, from the same union the first screen's list reads:
// the item table a library has rows in is its kind.
fn kinds(connection: &Connection) -> rusqlite::Result<HashMap<String, String>> {
    let sql = "SELECT library, 'movies' AS kind FROM movies GROUP BY library \
               UNION ALL \
               SELECT library, 'series' AS kind FROM series GROUP BY library";
    let rows = collect(connection, sql, &[], |row| {
        Ok((row.get::<_, String>(0)?, row.get::<_, String>(1)?))
    })?;
    let mut kinds = HashMap::new();
    for (library, kind) in rows {
        kinds.entry(library).or_insert(kind);
    }
    Ok(kinds)
}
