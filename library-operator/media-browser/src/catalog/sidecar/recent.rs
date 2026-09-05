// The one read behind the two recency queries: movies and episodes off
// every library in one union, newest first by the query's own column.
// The bounded key selection runs before the payload reads, so titles,
// art, and JSON are read only for candidates that can enter the fold.

use rusqlite::{Connection, Row};

use super::{collect, item};
use crate::catalog::recency::{CANDIDATES, Candidate, PAGES};
use crate::catalog::{Order, Slot, Title};

/// The column an order names. The closed match is the whole of what may
/// reach the SQL text.
pub fn column(order: Order) -> &'static str {
    match order {
        Order::Released => "released",
        Order::Added => "added",
    }
}

/// At most `PAGES * CANDIDATES` candidates, newest first. The key union
/// joins episodes to their series before the limit, so an orphan cannot
/// displace a candidate that the payload read can return. `kind` keeps a
/// movie first when the order column, `library`, and `id` are equal.
pub fn candidates(connection: &Connection, order: Order) -> rusqlite::Result<Vec<Candidate>> {
    let sql = candidate_sql(order);
    collect(
        connection,
        &sql,
        &[&((PAGES * CANDIDATES) as i64)],
        candidate,
    )
}

fn candidate_sql(order: Order) -> String {
    format!(
        "WITH candidate_keys AS MATERIALIZED (\
           SELECT movies.library, movies.id, movies.released, movies.added, \
                  0 AS kind, '' AS series, 0 AS season, 0 AS episode \
           FROM movies \
           UNION ALL \
           SELECT episodes.library, episodes.id, episodes.released, episodes.added, \
                  1 AS kind, episodes.series, episodes.season, episodes.episode \
           FROM episodes JOIN series ON series.library = episodes.library \
           AND series.id = episodes.series \
           ORDER BY {key} DESC, library, id, kind LIMIT ?1\
         ) \
         SELECT * FROM (\
           SELECT {movie_columns}, keys.library, keys.added, 'movies' AS kind, \
                  '' AS series, 0 AS season, 0 AS episode, \
                  '' AS series_title, '' AS series_art, '' AS series_released, \
                  0 AS series_duration, '' AS series_rating, \
                  keys.{key} AS ordering, keys.kind AS tie_kind \
           FROM candidate_keys AS keys \
           JOIN movies ON movies.library = keys.library AND movies.id = keys.id \
           WHERE keys.kind = 0 \
           UNION ALL \
           SELECT {episode_columns}, keys.library, keys.added, 'episodes' AS kind, \
                  keys.series, keys.season, keys.episode, \
                  series.title, series.art, series.released, series.duration, \
                  json_extract(series.body, '$.contentRating'), \
                  keys.{key} AS ordering, keys.kind AS tie_kind \
           FROM candidate_keys AS keys \
           JOIN episodes ON episodes.library = keys.library AND episodes.id = keys.id \
           JOIN series ON series.library = keys.library AND series.id = keys.series \
           WHERE keys.kind = 1\
         ) ORDER BY ordering DESC, library, id, tie_kind",
        movie_columns = MOVIE_COLUMNS,
        episode_columns = EPISODE_COLUMNS,
        key = column(order),
    )
}

// These payload columns have the order `item::title` reads. An episode
// has no content rating or tagline of its own, so those positions are
// empty strings.
const MOVIE_COLUMNS: &str = "movies.id, movies.title, movies.released, movies.art, \
                             movies.duration, \
                             json_extract(movies.body, '$.contentRating'), \
                             json_extract(movies.body, '$.tagline')";
const EPISODE_COLUMNS: &str = "episodes.id, episodes.title, episodes.released, episodes.art, \
                               episodes.duration, '', ''";

fn candidate(row: &Row<'_>) -> rusqlite::Result<Candidate> {
    let title = item::title(row)?;
    // The columns the union selects after the ones every list reads.
    let at = |column: usize| item::WIDTH + column;
    let library: String = row.get(at(0))?;
    let kind: String = row.get(at(2))?;
    if kind == "movies" {
        return Ok(Candidate::Movie {
            slot: Slot::of(&library, "movies", title),
        });
    }
    Ok(Candidate::Episode {
        library,
        episode: title,
        added: row.get(at(1))?,
        season: row.get(at(4))?,
        number: row.get(at(5))?,
        series: Title {
            id: row.get(at(3))?,
            title: row.get(at(6))?,
            art: row.get(at(7))?,
            released: row.get(at(8))?,
            duration: row.get(at(9))?,
            rating: item::text(row, at(10))?,
            tagline: String::new(),
        },
    })
}
