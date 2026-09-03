// The reads behind a movie's page. The item's own columns come off the
// movies row. The fields the sidecar wrote come out of the body column
// with SQLite's json_extract. The backdrop, the logo, and the trailer
// come off the files table through file_items, by role.

use rusqlite::Connection;

use super::collect;
use super::item::{self, COLUMNS};
use crate::catalog::{MovieDetails, MovieSet};

/// One movie's details, as a list of one, or an empty list where the
/// library holds no movie under that id.
pub fn movie(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<MovieDetails>> {
    let sql = "SELECT title, released, duration, set_id, \
                      json_extract(body, '$.contentRating'), \
                      json_extract(body, '$.tagline'), \
                      json_extract(body, '$.plot'), \
                      json_extract(body, '$.genres'), \
                      json_extract(body, '$.directors'), \
                      json_extract(body, '$.writers'), \
                      json_extract(body, '$.cast'), \
                      json_extract(body, '$.studios'), \
                      json_extract(body, '$.ratings') \
               FROM movies WHERE library = ? AND id = ?";
    let mut found = collect(connection, sql, &[&library, &id], |row| {
        Ok(MovieDetails {
            title: row.get(0)?,
            released: row.get(1)?,
            duration: row.get(2)?,
            set_id: row.get(3)?,
            rating: item::text(row, 4)?,
            tagline: item::text(row, 5)?,
            plot: item::text(row, 6)?,
            genres: item::strings(&item::text(row, 7)?),
            directors: item::strings(&item::text(row, 8)?),
            writers: item::strings(&item::text(row, 9)?),
            cast: item::credits(&item::text(row, 10)?),
            studios: item::strings(&item::text(row, 11)?),
            ratings: item::ratings(&item::text(row, 12)?),
            backdrop: String::new(),
            logo: String::new(),
            trailer: String::new(),
        })
    })?;

    if let Some(details) = found.first_mut() {
        for (role, path) in item::art(connection, library, id)? {
            match role.as_str() {
                "backdrop" => details.backdrop = path,
                "logo" => details.logo = path,
                _ => details.trailer = path,
            }
        }
    }
    Ok(found)
}

/// One set and its members in release order, as a list of one, or an
/// empty list where the library holds no set under that id.
pub fn set(connection: &Connection, library: &str, id: &str) -> rusqlite::Result<Vec<MovieSet>> {
    let named = collect(
        connection,
        "SELECT title FROM sets WHERE library = ? AND id = ?",
        &[&library, &id],
        |row| row.get::<_, String>(0),
    )?;
    let Some(title) = named.into_iter().next() else {
        return Ok(Vec::new());
    };

    // The members come off the index on (library, set_id), so the strip
    // is one indexed read and not a scan of the body column.
    let sql = format!(
        "SELECT {COLUMNS} FROM movies WHERE library = ? AND set_id = ? \
         ORDER BY released, sort_key"
    );
    let members = collect(connection, &sql, &[&library, &id], item::title)?;
    Ok(vec![MovieSet { title, members }])
}
