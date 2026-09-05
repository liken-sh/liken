// The three grouped reads behind the pool, each over an index the
// catalog already holds: `genres` by `(library, genre)`, `credits` by
// `(library, contributor, item)`, and `movies` by `(library, set_id)`.

use rusqlite::Connection;

use super::collect;
use crate::catalog::pool::Candidate;
use crate::catalog::recency::WORKS_FLOOR;
use crate::catalog::{Order, Query};

/// Every candidate: the genres, then the people, then the sets, each
/// with its weight. The order of the answer is fixed by name, so the draw
/// sees the same pool on every read.
pub fn candidates(connection: &Connection) -> rusqlite::Result<Vec<Candidate>> {
    let mut pool = genres(connection)?;
    pool.extend(people(connection)?);
    pool.extend(sets(connection)?);
    Ok(pool)
}

// Every genre with the count of titles that carry it, a title that
// leads with it counted twice, so a genre that leads is weightier than
// one that trails.
fn genres(connection: &Connection) -> rusqlite::Result<Vec<Candidate>> {
    let sql = "SELECT genre, COUNT(*) + SUM(rank = 0) FROM genres \
               WHERE genre != '' GROUP BY genre ORDER BY genre";
    collect(connection, sql, &[], |row| {
        let name: String = row.get(0)?;
        Ok(Candidate {
            query: Query::Genre {
                name: name.clone(),
                order: Order::Released,
            },
            name,
            weight: weight(row.get(1)?),
        })
    })
}

// Every person with an entry and more than `WORKS_FLOOR` distinct
// titles credited in one library, weighed by that count. A person's
// works read joins their entries across libraries; the pool counts one
// library's entry, so this read stays one group over the credits
// index.
fn people(connection: &Connection) -> rusqlite::Result<Vec<Candidate>> {
    let sql = "SELECT credited.library, credited.contributor, contributors.name, credited.works \
               FROM (\
                 SELECT library, contributor, COUNT(DISTINCT item) AS works \
                 FROM credits WHERE contributor != '' \
                 GROUP BY library, contributor HAVING works > ?1\
               ) AS credited \
               JOIN contributors ON contributors.library = credited.library \
               AND contributors.path = credited.contributor \
               ORDER BY contributors.name, credited.library";
    collect(connection, sql, &[&(WORKS_FLOOR as i64)], |row| {
        Ok(Candidate {
            query: Query::Person {
                library: row.get(0)?,
                path: row.get(1)?,
            },
            name: row.get(2)?,
            weight: weight(row.get(3)?),
        })
    })
}

// Every set with at least two members, weighed by its member count,
// because a set of one is its one film.
fn sets(connection: &Connection) -> rusqlite::Result<Vec<Candidate>> {
    let sql = "SELECT sets.library, sets.id, sets.title, COUNT(*) AS members \
               FROM sets JOIN movies ON movies.library = sets.library \
               AND movies.set_id = sets.id \
               GROUP BY sets.library, sets.id \
               HAVING members >= 2 \
               ORDER BY sets.title, sets.library, sets.id";
    collect(connection, sql, &[], |row| {
        Ok(Candidate {
            query: Query::Set {
                library: row.get(0)?,
                id: row.get(1)?,
            },
            name: row.get(2)?,
            weight: weight(row.get(3)?),
        })
    })
}

fn weight(count: i64) -> u64 {
    count.max(0) as u64
}
