// The two reads a media browser makes of a franchise, as catalog.sql writes
// them beside the tables. The strip read is the INNER JOIN: the whole order
// with only the members some library holds. The page read is the LEFT JOIN:
// every entry, held or not, with the episodes the catalog holds for a series
// run. Both resolve a member through the aliases table across every library of
// the namespace, and the first library by name wins where two hold one member.

use rusqlite::{Connection, Row};
use serde_json::Value;

use super::collect;
use super::item;
use crate::catalog::FranchiseEntry;
use crate::catalog::franchise::{Calendar, Entry, Era, Franchise, Held, Membership};

// The two item tables as one list of members, which both reads join
// through. The kind column names the table a row came from, so a press
// on a slot opens the page of its own kind.
const ITEMS: &str = "SELECT library, id, title, art, arts, released, slug, body, duration, \
                            'movies' AS kind \
                     FROM movies \
                     UNION ALL \
                     SELECT library, id, title, art, arts, released, slug, body, duration, \
                            'series' AS kind \
                     FROM series";

// The member columns both reads answer with, in the order [`entry`]
// takes them. The last of them is the held item, which is NULL for
// every gap of the page read.
const COLUMNS: &str = "m.position, m.kind, m.alias, m.title, m.release_year, m.timed, \
                       m.time_from, m.time_to, m.universes, MIN(i.library), i.id, i.title, \
                       i.art, i.arts, i.released, i.slug, i.kind, m.released, i.duration";

/// Every franchise row of every library, in sort order. The read is one scan
/// of the franchises table, which holds one row per franchise of the namespace
/// and is the smallest table of the catalog, so it needs no index of its own.
pub fn all(connection: &Connection) -> rusqlite::Result<Vec<FranchiseEntry>> {
    // Where the franchises row carries no art of its own, the page falls
    // back to the poster of the first member some library holds. The
    // member comes off the same alias join the other two reads make, in
    // story order, and the first one with a poster wins. The poster
    // resolves against that member's own library, which is the second
    // subquery.
    let member = |column: &str| {
        format!(
            "(SELECT {column} FROM franchise_members m \
              JOIN aliases a ON a.alias = m.alias \
              JOIN items i ON i.library = a.library AND i.id = a.item \
              WHERE m.library = f.library AND m.franchise = f.id AND i.art != '' \
              ORDER BY m.position, i.library LIMIT 1)"
        )
    };
    let sql = format!(
        "WITH items AS ({ITEMS}) \
         SELECT f.library, f.id, f.title, f.art, f.slug, {art}, {holder} \
         FROM franchises f ORDER BY f.sort_key, f.library, f.id",
        art = member("i.art"),
        holder = member("i.library"),
    );
    collect(connection, &sql, &[], |row| {
        let own: String = item::text(row, 3)?;
        let (art, art_library) = match own.is_empty() {
            true => (item::text(row, 5)?, item::text(row, 6)?),
            false => (own, row.get(0)?),
        };
        Ok(FranchiseEntry {
            library: row.get(0)?,
            id: row.get(1)?,
            title: row.get(2)?,
            art,
            art_library,
            slug: item::text(row, 4)?,
        })
    })
}

/// Every franchise this item belongs to, with its held members in position
/// order. The franchises come from the item's own aliases, and the join to the
/// item tables keeps the members some library holds. The franchise's title
/// comes off the franchises row, because the strip's heading is that title.
/// The read also counts the entries of the order by kind, held or not, on
/// the primary key of `franchise_members`, so the heading says the scope of
/// the order and not only the members the namespace holds.
pub fn strips(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<Membership>> {
    let sql = format!(
        "WITH mine AS (\
           SELECT alias FROM aliases WHERE library = ?1 AND item = ?2\
         ), found AS (\
           SELECT DISTINCT library, franchise FROM franchise_members \
           WHERE alias IN (SELECT alias FROM mine)\
         ), items AS ({ITEMS}) \
         SELECT m.library, m.franchise, \
           (SELECT title FROM franchises named \
            WHERE named.library = m.library AND named.id = m.franchise), \
           (SELECT count(*) FROM franchise_members every \
            WHERE every.library = m.library AND every.franchise = m.franchise \
              AND every.kind = 'movie'), \
           (SELECT count(*) FROM franchise_members every \
            WHERE every.library = m.library AND every.franchise = m.franchise \
              AND every.kind = 'series'), \
           {COLUMNS} \
         FROM franchise_members m \
         JOIN found f ON f.library = m.library AND f.franchise = m.franchise \
         JOIN aliases a ON a.alias = m.alias \
         JOIN items i ON i.library = a.library AND i.id = a.item \
         GROUP BY m.library, m.franchise, m.position \
         ORDER BY m.library, m.franchise, m.position"
    );
    let members = collect(connection, &sql, &[&library, &id], |row| {
        Ok((
            Membership {
                library: row.get(0)?,
                id: row.get(1)?,
                title: item::text(row, 2)?,
                movies: row.get(3)?,
                series: row.get(4)?,
                members: Vec::new(),
            },
            entry(row, 5)?,
        ))
    })?;

    // The read answers one row per member, ordered by the franchise, so
    // the members fold into the franchise the row before them opened.
    let mut strips: Vec<Membership> = Vec::new();
    for (franchise, member) in members {
        match strips.last_mut() {
            Some(last) if last.library == franchise.library && last.id == franchise.id => {}
            _ => strips.push(franchise),
        }
        if let Some(last) = strips.last_mut() {
            last.members.push(member);
        }
    }
    Ok(strips)
}

/// One franchise as a list of one, and an empty list where that `Library`
/// holds no franchise under that id. The header comes off the franchises row,
/// and the body carries the universe, the calendar, and the eras as the file
/// wrote them.
pub fn franchise(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<Franchise>> {
    let sql = "SELECT title, art, json_extract(body, '$.universe'), \
                      json_extract(body, '$.calendar'), json_extract(body, '$.eras') \
               FROM franchises WHERE library = ? AND id = ?";
    let mut found = collect(connection, sql, &[&library, &id], |row| {
        Ok(Franchise {
            library: library.to_string(),
            id: id.to_string(),
            title: row.get(0)?,
            art: item::text(row, 1)?,
            universe: item::text(row, 2)?,
            calendar: calendar(&item::text(row, 3)?),
            eras: eras(&item::text(row, 4)?),
            entries: Vec::new(),
        })
    })?;

    if let Some(page) = found.first_mut() {
        page.entries = entries(connection, library, id)?;
    }
    Ok(found)
}

// Every entry of one franchise in story order, held or not. The
// episodes column counts what the catalog holds for a series run: every
// episode of the show where the run names no season, and the episodes
// the runs name where it does. The read also takes the tagline and the
// plot out of the held item's body, because the wall's card draws them
// beside the art; the strip read carries neither.
fn entries(connection: &Connection, library: &str, id: &str) -> rusqlite::Result<Vec<Entry>> {
    let sql = format!(
        "WITH items AS ({ITEMS}) \
         SELECT {COLUMNS}, \
           (SELECT count(*) FROM episodes e \
            JOIN aliases sa ON sa.library = e.library AND sa.item = e.series \
            WHERE sa.alias = m.alias \
              AND (NOT EXISTS (SELECT 1 FROM franchise_runs r \
                               WHERE r.library = m.library AND r.franchise = m.franchise \
                                 AND r.position = m.position) \
                   OR EXISTS (SELECT 1 FROM franchise_runs r \
                              WHERE r.library = m.library AND r.franchise = m.franchise \
                                AND r.position = m.position AND r.season = e.season \
                                AND r.episode IN (0, e.episode)))) AS held_episodes, \
           json_extract(i.body, '$.tagline'), json_extract(i.body, '$.plot') \
         FROM franchise_members m \
         LEFT JOIN aliases a ON a.alias = m.alias \
         LEFT JOIN items i ON i.library = a.library AND i.id = a.item \
         WHERE m.library = ?1 AND m.franchise = ?2 \
         GROUP BY m.position \
         ORDER BY m.position"
    );
    collect(connection, &sql, &[&library, &id], |row| {
        let mut member = entry(row, 0)?;
        member.episodes = row.get(19)?;
        if let Some(held) = member.held.as_mut() {
            held.tagline = item::text(row, 20)?;
            held.plot = item::text(row, 21)?;
        }
        Ok(member)
    })
}

// One member row as an entry, from the columns [`COLUMNS`] names,
// starting at `at`. A held item's own library is the MIN over the
// libraries that hold it, and a NULL there is a gap.
fn entry(row: &Row<'_>, at: usize) -> rusqlite::Result<Entry> {
    let library: Option<String> = row.get(at + 9)?;
    let held = match library {
        Some(library) => Some(Held {
            library,
            id: item::text(row, at + 10)?,
            kind: item::text(row, at + 16)?,
            title: item::text(row, at + 11)?,
            art: item::text(row, at + 12)?,
            arts: item::strings(&item::text(row, at + 13)?),
            released: item::text(row, at + 14)?,
            slug: item::text(row, at + 15)?,
            tagline: String::new(),
            plot: String::new(),
            duration: row.get::<_, Option<i64>>(at + 18)?.unwrap_or_default(),
        }),
        None => None,
    };
    Ok(Entry {
        position: row.get(at)?,
        kind: row.get(at + 1)?,
        alias: row.get(at + 2)?,
        title: row.get(at + 3)?,
        released: item::text(row, at + 17)?,
        release_year: row.get(at + 4)?,
        timed: row.get::<_, i64>(at + 5)? != 0,
        from: row.get(at + 6)?,
        to: row.get(at + 7)?,
        universes: item::strings(&item::text(row, at + 8)?),
        held,
        episodes: 0,
    })
}

// The body's calendar, and nothing where the file names none. A
// calendar needs a unit, so a block without one is no calendar.
fn calendar(json: &str) -> Option<Calendar> {
    let Ok(Value::Object(block)) = serde_json::from_str::<Value>(json) else {
        return None;
    };
    let unit = word(&block, "unit");
    match unit.is_empty() {
        true => None,
        false => Some(Calendar {
            unit,
            zero: word(&block, "zero"),
            before: word(&block, "before"),
            after: word(&block, "after"),
        }),
    }
}

// The body's eras, in the order the file names them. An era with no
// name is left out, because a bar with no words on it says nothing.
fn eras(json: &str) -> Vec<Era> {
    let Ok(Value::Array(named)) = serde_json::from_str::<Value>(json) else {
        return Vec::new();
    };
    named
        .iter()
        .filter_map(|era| {
            let block = era.as_object()?;
            let name = word(block, "name");
            match name.is_empty() {
                true => None,
                false => Some(Era {
                    name,
                    from: era.get("from").and_then(Value::as_f64).unwrap_or_default(),
                    to: era.get("to").and_then(Value::as_f64).unwrap_or_default(),
                }),
            }
        })
        .collect()
}

fn word(block: &serde_json::Map<String, Value>, name: &str) -> String {
    block
        .get(name)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_body_with_no_calendar_answers_none() {
        assert_eq!(calendar(""), None);
        assert_eq!(calendar("null"), None);
        assert_eq!(calendar("{}"), None);
        assert_eq!(calendar(r#"{"zero":"Yavin"}"#), None);
    }

    #[test]
    fn a_calendar_carries_its_unit_and_its_two_words() {
        assert_eq!(
            calendar(r#"{"unit":"years","zero":"Yavin","before":"BBY","after":"ABY"}"#),
            Some(Calendar {
                unit: "years".into(),
                zero: "Yavin".into(),
                before: "BBY".into(),
                after: "ABY".into(),
            })
        );
    }

    #[test]
    fn the_eras_come_in_the_order_the_file_names_them() {
        let eras = eras(r#"[{"name":"Late","from":-5,"to":5},{"name":"Early","from":-500}]"#);
        assert_eq!(
            eras,
            [
                Era {
                    name: "Late".into(),
                    from: -5.0,
                    to: 5.0
                },
                Era {
                    name: "Early".into(),
                    from: -500.0,
                    to: 0.0
                },
            ]
        );
    }

    #[test]
    fn an_era_with_no_name_and_a_body_with_no_eras_leave_nothing_behind() {
        assert!(eras("").is_empty());
        assert!(eras("null").is_empty());
        assert!(eras(r#"{"name":"Late"}"#).is_empty());
        assert!(eras(r#"[{"from":-5,"to":5},"Late"]"#).is_empty());
    }
}
