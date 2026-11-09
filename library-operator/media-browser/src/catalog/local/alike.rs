// The read behind the More like this strip of a movie's page: the other
// films of the same library that share people with this one, each with
// its score. A film scores 3 for each director the two share and 2 for
// each actor billed in the first four of both, and 1 more where both lead
// with the same genre. A candidate must share a person, so it scores at
// least 2, and the genre alone never puts a film in the strip.
//
// The page reads this once when it opens, so it is one statement that
// walks indexes alone: this film's credits through the primary key, each
// of its people's other credits through `(library, contributor, item)`,
// and each candidate's row and leading genre through their primary keys.
// The hints hold that plan. Without them SQLite walks the whole library's
// credits for the film's own, and every movie of the library for the
// candidates' rows.

use rusqlite::Connection;

use super::{collect, item};
use crate::catalog::{Alike, Slot};

// The statement, with the library as ?1 and the film as ?2.
const SQL: &str = "\
WITH seeds AS (
  SELECT DISTINCT contributor, part
  FROM credits INDEXED BY sqlite_autoindex_credits_1
  WHERE library = ?1 AND item = ?2
    AND (part = 'director' OR (part = 'actor' AND billing < 4))
),
shared AS (
  SELECT item, SUM(points) AS points FROM (
    SELECT DISTINCT other.item AS item, seeds.contributor, seeds.part,
           CASE seeds.part WHEN 'director' THEN 3 ELSE 2 END AS points
    FROM seeds JOIN credits AS other INDEXED BY credits_library_contributor_item
      ON other.library = ?1 AND other.contributor = seeds.contributor
     AND other.part = seeds.part
    WHERE other.item != ?2 AND (other.part = 'director' OR other.billing < 4)
  ) GROUP BY item
)
SELECT {columns},
       shared.points + EXISTS (
         SELECT 1 FROM genres AS mine JOIN genres AS theirs
           ON theirs.library = mine.library AND theirs.item = shared.item
          AND theirs.rank = 0 AND theirs.genre = mine.genre
         WHERE mine.library = ?1 AND mine.item = ?2 AND mine.rank = 0
       ),
       IFNULL(json_extract(movies.body, '$.ratings.imdb'), 0.0)
FROM shared CROSS JOIN movies ON movies.library = ?1 AND movies.id = shared.item";

fn sql() -> String {
    SQL.replace("{columns}", item::COLUMNS)
}

/// Every film of the library that shares a person with this one, with
/// its score and its IMDb rating, in no order: the page orders them,
/// because the order reads who at the screen has seen each one.
pub fn films(connection: &Connection, library: &str, id: &str) -> rusqlite::Result<Vec<Alike>> {
    collect(connection, &sql(), &[&library, &id], |row| {
        Ok(Alike {
            slot: Slot::of(library, "movies", item::title(row)?),
            score: row.get(item::WIDTH)?,
            rating: row.get(item::WIDTH + 1)?,
        })
    })
}

/// The plan SQLite takes for the read, one line per step, which the
/// plan test reads.
#[cfg(test)]
pub fn plan(connection: &Connection) -> rusqlite::Result<Vec<String>> {
    collect(
        connection,
        &format!("EXPLAIN QUERY PLAN {}", sql()),
        &[&"test/films", &"movie:1"],
        |row| row.get(3),
    )
}
