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

/// One page of candidates, newest first, `CANDIDATES` rows from the
/// page's offset. A movie row pads the series columns
/// with nothing, and an episode row carries its series' title, art,
/// release, duration, and rating from the join. The library and the id
/// break ties, so the order is the same on every read.
pub fn candidates(
    connection: &Connection,
    order: Order,
    page: usize,
) -> rusqlite::Result<Vec<Candidate>> {
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
         ) ORDER BY {key} DESC, library, id LIMIT ?1 OFFSET ?2",
        columns = item::COLUMNS,
        episode_columns = EPISODE_COLUMNS,
        key = column(order),
    );
    let offset = (page * CANDIDATES) as i64;
    collect(
        connection,
        &sql,
        &[&(CANDIDATES as i64), &offset],
        candidate,
    )
}

// The episode half of the union, in the order item::COLUMNS takes them.
// An episode row holds neither a content rating nor a tagline of its
// own, so the union's two halves carry the same columns.
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
