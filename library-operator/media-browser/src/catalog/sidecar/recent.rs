// The one read behind the two recency queries: movies and episodes off
// every library in one union, newest first by the query's own column,
// bounded to the candidate count. The series row is joined to each
// episode because a folded episode becomes a slot for its series.

use rusqlite::{Connection, Row};

use super::{collect, item};
use crate::catalog::recency::{CANDIDATES, Candidate};
use crate::catalog::{Order, Slot, Title};

/// The column an order names. The closed match is the whole of what may
/// reach the SQL text.
pub fn column(order: Order) -> &'static str {
    match order {
        Order::Released => "released",
        Order::Added => "added",
    }
}

/// The candidates, newest first. A movie row pads the series columns
/// with nothing, and an episode row carries its series' title, art,
/// release, duration, and rating from the join. The library and the id
/// break ties, so the order is the same on every read.
pub fn candidates(connection: &Connection, order: Order) -> rusqlite::Result<Vec<Candidate>> {
    let sql = format!(
        "SELECT * FROM (\
           SELECT {columns}, library, added, 'movies' AS kind, \
                  '' AS series, 0 AS season, 0 AS episode, \
                  '' AS series_title, '' AS series_art, '' AS series_released, \
                  0 AS series_duration, '' AS series_rating \
           FROM movies \
           UNION ALL \
           SELECT {episode_columns}, episodes.library, episodes.added, 'episodes' AS kind, \
                  episodes.series, episodes.season, episodes.episode, \
                  series.title, series.art, series.released, series.duration, \
                  json_extract(series.body, '$.contentRating') \
           FROM episodes JOIN series ON series.library = episodes.library \
           AND series.id = episodes.series\
         ) ORDER BY {key} DESC, library, id LIMIT ?1",
        columns = item::COLUMNS,
        episode_columns = EPISODE_COLUMNS,
        key = column(order),
    );
    collect(connection, &sql, &[&(CANDIDATES as i64)], candidate)
}

// The episode half of the union, in the order item::COLUMNS takes them.
const EPISODE_COLUMNS: &str = "episodes.id, episodes.title, episodes.released, episodes.art, \
                               episodes.duration, ''";

fn candidate(row: &Row<'_>) -> rusqlite::Result<Candidate> {
    let title = item::title(row)?;
    let library: String = row.get(6)?;
    let kind: String = row.get(8)?;
    if kind == "movies" {
        return Ok(Candidate::Movie {
            slot: Slot::of(&library, "movies", title),
        });
    }
    Ok(Candidate::Episode {
        library,
        episode: title,
        added: row.get(7)?,
        season: row.get(10)?,
        number: row.get(11)?,
        series: Title {
            id: row.get(9)?,
            title: row.get(12)?,
            art: row.get(13)?,
            released: row.get(14)?,
            duration: row.get(15)?,
            rating: item::text(row, 16)?,
        },
    })
}
