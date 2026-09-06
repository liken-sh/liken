// The one read that builds an index off the replica. It walks the
// tables in rung order: titles and their plots, sets and franchises,
// then the episodes and aliases that fold onto them, then the people.
// Every row streams into the builder as it is read, because collecting
// 100,000 rows into Vecs first would double the build's peak memory.

use std::collections::HashMap;

use rusqlite::Connection;

use crate::catalog::Slot;
use crate::catalog::search::{Builder, Index, Item, Kind, Person, Place, Where};
use crate::catalog::sidecar::item;

// The place of each item, keyed by library and id, so an episode or an
// alias read later finds the item its strings fold onto.
type Places = HashMap<(String, String), Place>;

/// The index over one replica: movies, series, sets, franchises, their
/// episodes' titles and plots, their aliases, and every contributor.
pub fn index(connection: &Connection) -> rusqlite::Result<Index> {
    let mut builder = Builder::new();
    let mut places = Places::new();
    titles(connection, &mut builder, &mut places, "movies")?;
    titles(connection, &mut builder, &mut places, "series")?;
    collections(connection, &mut builder, &mut places, "sets")?;
    collections(connection, &mut builder, &mut places, "franchises")?;
    episodes(connection, &mut builder, &places)?;
    aliases(connection, &mut builder, &places)?;
    contributors(connection, &mut builder)?;
    Ok(builder.finish())
}

// The rows of one item table. The table name is formatted into the SQL,
// and it is a literal from `index`, never a value from outside.
fn titles(
    connection: &Connection,
    builder: &mut Builder,
    places: &mut Places,
    table: &'static str,
) -> rusqlite::Result<()> {
    let sql = format!(
        "SELECT library, id, title, sort_key, released, art, duration, \
                json_extract(body, '$.contentRating'), \
                json_extract(body, '$.tagline'), \
                json_extract(body, '$.plot'), {seasons} \
         FROM {table}",
        seasons = item::seasons(table),
    );
    let mut statement = connection.prepare(&sql)?;
    let mut rows = statement.query([])?;
    while let Some(row) = rows.next()? {
        let library: String = row.get(0)?;
        let id: String = row.get(1)?;
        let title: String = row.get(2)?;
        let slot = Slot {
            library: library.clone(),
            kind: table.to_string(),
            id: id.clone(),
            title: title.clone(),
            released: row.get(4)?,
            art: row.get(5)?,
            duration: row.get(6)?,
            rating: item::text(row, 7)?,
            tagline: item::text(row, 8)?,
            seasons: row.get(10)?,
            ..Slot::default()
        };
        let mut strings = vec![(Where::Title, title)];
        let plot = item::text(row, 9)?;
        if !plot.is_empty() {
            strings.push((Where::Plot, plot));
        }
        let place = builder.add(Item {
            slot,
            sort_key: row.get(3)?,
            kind: Kind::Title,
            strings,
        });
        places.insert((library, id), place);
    }
    Ok(())
}

// The sets and the franchises. The kind word is the one the wall opens
// each by. A franchise has no release, so its slot's date is empty and
// it ties after every dated title.
fn collections(
    connection: &Connection,
    builder: &mut Builder,
    places: &mut Places,
    table: &'static str,
) -> rusqlite::Result<()> {
    let word = match table {
        "sets" => "sets",
        _ => "franchise",
    };
    let sql = format!("SELECT library, id, title, sort_key, released, art FROM {table}");
    let mut statement = connection.prepare(&sql)?;
    let mut rows = statement.query([])?;
    while let Some(row) = rows.next()? {
        let library: String = row.get(0)?;
        let id: String = row.get(1)?;
        let title: String = row.get(2)?;
        let place = builder.add(Item {
            slot: Slot {
                library: library.clone(),
                kind: word.to_string(),
                id: id.clone(),
                title: title.clone(),
                released: row.get(4)?,
                art: row.get(5)?,
                ..Slot::default()
            },
            sort_key: row.get(3)?,
            kind: Kind::Collection,
            strings: vec![(Where::Title, title)],
        });
        places.insert((library, id), place);
    }
    Ok(())
}

// Every episode's title and plot, folded onto its series, because a hit
// on an episode opens the series' page. An episode whose series is not
// in the replica is skipped.
fn episodes(
    connection: &Connection,
    builder: &mut Builder,
    places: &Places,
) -> rusqlite::Result<()> {
    let sql = "SELECT library, series, title, json_extract(body, '$.plot') FROM episodes";
    let mut statement = connection.prepare(sql)?;
    let mut rows = statement.query([])?;
    while let Some(row) = rows.next()? {
        let key = (row.get(0)?, row.get(1)?);
        let Some(place) = places.get(&key) else {
            continue;
        };
        builder.fold(*place, Where::EpisodeTitle, &row.get::<_, String>(2)?);
        let plot = item::text(row, 3)?;
        if !plot.is_empty() {
            builder.fold(*place, Where::EpisodePlot, &plot);
        }
    }
    Ok(())
}

// Every alias, folded onto the item it names. An alias whose item is not
// in the replica is skipped.
fn aliases(
    connection: &Connection,
    builder: &mut Builder,
    places: &Places,
) -> rusqlite::Result<()> {
    let mut statement = connection.prepare("SELECT library, item, alias FROM aliases")?;
    let mut rows = statement.query([])?;
    while let Some(row) = rows.next()? {
        let key = (row.get(0)?, row.get(1)?);
        let Some(place) = places.get(&key) else {
            continue;
        };
        builder.fold(*place, Where::Alias, &row.get::<_, String>(2)?);
    }
    Ok(())
}

// Every contributor: the path their page reads by, the name a search
// lands on, and whether a headshot file exists under the path.
// One person is one entry across libraries, because their page already
// merges their work across libraries. The read orders by path so every
// library's row for one person arrives together. The first row names
// the person, and a headshot in any library counts.
fn contributors(connection: &Connection, builder: &mut Builder) -> rusqlite::Result<()> {
    let sql = "SELECT library, path, name, headshot FROM contributors \
               ORDER BY path, library";
    let mut statement = connection.prepare(sql)?;
    let mut rows = statement.query([])?;
    let mut held: Option<Person> = None;
    while let Some(row) = rows.next()? {
        let person = Person {
            library: row.get(0)?,
            path: row.get(1)?,
            name: row.get(2)?,
            headshot: row.get::<_, i64>(3)? != 0,
        };
        match held.as_mut() {
            Some(first) if first.path == person.path => first.headshot |= person.headshot,
            _ => {
                if let Some(first) = held.replace(person) {
                    builder.person(first);
                }
            }
        }
    }
    if let Some(first) = held {
        builder.person(first);
    }
    Ok(())
}
