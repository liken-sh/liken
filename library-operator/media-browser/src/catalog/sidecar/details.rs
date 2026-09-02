// The reads behind a movie's page. The item's own columns come off the
// movies row. The fields the sidecar wrote come out of the body column
// with SQLite's json_extract. The backdrop, the logo, and the trailer
// come off the files table through file_items, by role.

use rusqlite::{Connection, Row};
use serde_json::Value;

use super::collect;
use crate::catalog::{Credit, MovieDetails, MovieSet, Title};

/// The columns every list of titles reads, in the order [`title`] takes
/// them.
pub const COLUMNS: &str =
    "id, title, released, art, duration, json_extract(body, '$.contentRating')";

/// One title from those columns.
pub fn title(row: &Row<'_>) -> rusqlite::Result<Title> {
    Ok(Title {
        id: row.get(0)?,
        title: row.get(1)?,
        released: row.get(2)?,
        art: row.get(3)?,
        duration: row.get(4)?,
        rating: text(row, 5)?,
    })
}

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
                      json_extract(body, '$.cast') \
               FROM movies WHERE library = ? AND id = ?";
    let mut found = collect(connection, sql, &[&library, &id], |row| {
        Ok(MovieDetails {
            title: row.get(0)?,
            released: row.get(1)?,
            duration: row.get(2)?,
            set_id: row.get(3)?,
            rating: text(row, 4)?,
            tagline: text(row, 5)?,
            plot: text(row, 6)?,
            genres: strings(&text(row, 7)?),
            directors: strings(&text(row, 8)?),
            writers: strings(&text(row, 9)?),
            cast: credits(&text(row, 10)?),
            backdrop: String::new(),
            logo: String::new(),
            trailer: String::new(),
        })
    })?;

    if let Some(details) = found.first_mut() {
        for (role, path) in art(connection, library, id)? {
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
    let members = collect(connection, &sql, &[&library, &id], self::title)?;
    Ok(vec![MovieSet { title, members }])
}

// The three files a page reads by role. A title with a second file in
// one role holds more than one row, so MIN(path) picks one, the way the
// main file's read does.
fn art(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<(String, String)>> {
    let sql = "SELECT files.role, MIN(files.path) FROM files \
               JOIN file_items ON file_items.library = files.library \
               AND file_items.path = files.path \
               WHERE file_items.library = ? AND file_items.item = ? \
               AND ((files.type = 'image' AND files.role IN ('backdrop', 'logo')) \
                    OR (files.type = 'video' AND files.role = 'trailer')) \
               GROUP BY files.role";
    collect(connection, sql, &[&library, &id], |row| {
        Ok((row.get(0)?, row.get(1)?))
    })
}

// One text column. A body that names no such field answers NULL, and
// this reads it as an empty string instead of a row that fails to map.
fn text(row: &Row<'_>, index: usize) -> rusqlite::Result<String> {
    Ok(row.get::<_, Option<String>>(index)?.unwrap_or_default())
}

// One array of the body. json_extract answers it as JSON text.
fn strings(json: &str) -> Vec<String> {
    serde_json::from_str(json).unwrap_or_default()
}

// The body's cast. A member with no name is left out, because a role
// with no person in front of it reads as damage.
fn credits(json: &str) -> Vec<Credit> {
    let Ok(Value::Array(members)) = serde_json::from_str::<Value>(json) else {
        return Vec::new();
    };
    members
        .iter()
        .map(|member| Credit {
            name: field(member, "name"),
            role: field(member, "role"),
        })
        .filter(|credit| !credit.name.is_empty())
        .collect()
}

fn field(member: &Value, name: &str) -> String {
    member
        .get(name)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_array_the_body_does_not_hold_reads_as_nothing() {
        assert!(strings("").is_empty());
        assert!(strings("null").is_empty());
        assert_eq!(strings(r#"["Drama","Mystery"]"#), ["Drama", "Mystery"]);
    }

    #[test]
    fn a_cast_carries_its_names_and_parts() {
        let cast = credits(r#"[{"name":"A Player","role":"The Part"},{"name":"Another"}]"#);
        assert_eq!(
            cast,
            [
                Credit {
                    name: "A Player".into(),
                    role: "The Part".into(),
                },
                Credit {
                    name: "Another".into(),
                    role: String::new(),
                },
            ]
        );
    }

    #[test]
    fn a_cast_the_body_does_not_hold_reads_as_nothing() {
        assert!(credits("").is_empty());
        assert!(credits(r#"{"name":"A Player"}"#).is_empty());
        assert!(credits(r#"[{"role":"The Part"}]"#).is_empty());
    }
}
