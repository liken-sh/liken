// The reads every item table shares: the header columns a list draws,
// the fields of the body column, and the files a page reads by role
// through file_items.

use rusqlite::{Connection, Row};
use serde_json::Value;

use super::collect;
use crate::catalog::{Credit, Title};

/// The columns every list of titles reads, in the order [`title`] takes
/// them. The last is the tagline a film's card leads with.
pub const COLUMNS: &str = "id, title, released, art, duration, \
                           json_extract(body, '$.contentRating'), \
                           json_extract(body, '$.tagline')";

/// How many columns [`COLUMNS`] names, so a read that selects more after
/// them counts its own from there.
pub const WIDTH: usize = 7;

// The seasons column of a slot read: a correlated count over the
// covering index (library, series, season, episode) for the series
// table, and the literal zero for every other item table, so the two
// halves of a union carry the same columns.
pub fn seasons(table: &str) -> String {
    if table != "series" {
        return "0".to_string();
    }
    format!(
        "(SELECT COUNT(DISTINCT episodes.season) FROM episodes \
          WHERE episodes.library = {table}.library AND episodes.series = {table}.id)"
    )
}

/// One title from those columns.
pub fn title(row: &Row<'_>) -> rusqlite::Result<Title> {
    Ok(Title {
        id: row.get(0)?,
        title: row.get(1)?,
        released: row.get(2)?,
        art: row.get(3)?,
        duration: row.get(4)?,
        rating: text(row, 5)?,
        tagline: text(row, 6)?,
    })
}

/// An item's files by role, as pairs of the role and the path: the three
/// roles a page reads. A title with a second file in one role holds more
/// than one row, so MIN(path) picks one, the way the main file's read
/// does.
pub fn art(
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

/// One text column of a row. A body that names no such field answers
/// NULL, and this reads it as an empty string instead of a row that fails
/// to map.
pub fn text(row: &Row<'_>, index: usize) -> rusqlite::Result<String> {
    Ok(row.get::<_, Option<String>>(index)?.unwrap_or_default())
}

/// One array of the body. json_extract answers it as JSON text.
pub fn strings(json: &str) -> Vec<String> {
    serde_json::from_str(json).unwrap_or_default()
}

/// The body's ratings, as pairs of the sidecar's own name for the site and
/// the score on that site's scale. A score that is not a number is left
/// out.
pub fn ratings(json: &str) -> Vec<(String, f64)> {
    let Ok(Value::Object(sites)) = serde_json::from_str::<Value>(json) else {
        return Vec::new();
    };
    sites
        .iter()
        .filter_map(|(name, score)| Some((name.clone(), score.as_f64()?)))
        .collect()
}

/// The body's cast. A member with no name is left out, because a role
/// with no person in front of it reads as damage.
pub fn credits(json: &str) -> Vec<Credit> {
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
    fn a_ratings_block_reads_as_one_pair_for_every_score() {
        assert_eq!(
            ratings(r#"{"imdb":6.5,"metacritic":80}"#),
            [("imdb".to_string(), 6.5), ("metacritic".to_string(), 80.0)]
        );
    }

    #[test]
    fn a_ratings_block_the_body_does_not_hold_reads_as_nothing() {
        assert!(ratings("").is_empty());
        assert!(ratings("null").is_empty());
        assert!(ratings(r#"{"imdb":"6.5"}"#).is_empty());
    }

    #[test]
    fn a_cast_the_body_does_not_hold_reads_as_nothing() {
        assert!(credits("").is_empty());
        assert!(credits(r#"{"name":"A Player"}"#).is_empty());
        assert!(credits(r#"[{"role":"The Part"}]"#).is_empty());
    }
}
