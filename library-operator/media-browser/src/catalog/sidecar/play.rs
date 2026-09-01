// The reads that turn a choice into a play list. The main file of a
// title is its `files` row reached through `file_items`, typed `video`
// and in the `primary` role, and its trickplay path comes with it.
// Every read is parameterised, like every other read of this source.

use rusqlite::Connection;

use super::collect;
use crate::catalog::{PlayItem, Presentation};

// The join from an item to its main file. A title with a second
// encoding holds more than one primary video file, so MIN(path) picks
// one, and the bare trickplay column comes from that same row, which
// is SQLite's rule for a bare column beside a single min or max.
const MAIN_FILE: &str = "JOIN file_items ON file_items.library = item.library AND file_items.item = item.id \
     JOIN files ON files.library = file_items.library AND files.path = file_items.path \
     AND files.type = 'video' AND files.role = 'primary'";

/// One movie's play list: the one item it resolves to, or nothing when
/// the movie holds no main file.
pub fn movie(connection: &Connection, library: &str, id: &str) -> rusqlite::Result<Vec<PlayItem>> {
    let sql = format!(
        "SELECT item.title, item.released, item.art, MIN(files.path), files.trickplay \
         FROM movies item {MAIN_FILE} \
         WHERE item.library = ? AND item.id = ? GROUP BY item.id"
    );
    collect(connection, &sql, &[&library, &id], |row| {
        let released: String = row.get(1)?;
        Ok(PlayItem {
            path: row.get(3)?,
            presentation: Presentation {
                kind: "video".into(),
                hint: "movie".into(),
                title: row.get(0)?,
                year: year(&released),
                art: row.get(2)?,
                trickplay: row.get(4)?,
                ..Presentation::default()
            },
        })
    })
}

/// The chosen episode and every later episode of its season, in
/// episode order. An episode with no main file drops out of the join,
/// and a list that does not start with the chosen episode is no list
/// at all.
pub fn episodes(
    connection: &Connection,
    library: &str,
    series: &str,
    season: i64,
    chosen: i64,
) -> rusqlite::Result<Vec<PlayItem>> {
    let sql = format!(
        "SELECT item.episode, item.title, item.released, item.art, IFNULL(parent.title, ''), \
                MIN(files.path), files.trickplay \
         FROM episodes item {MAIN_FILE} \
         LEFT JOIN series parent ON parent.library = item.library AND parent.id = item.series \
         WHERE item.library = ? AND item.series = ? AND item.season = ? AND item.episode >= ? \
         GROUP BY item.id ORDER BY item.episode"
    );
    let found = collect(
        connection,
        &sql,
        &[&library, &series, &season, &chosen],
        |row| {
            let number: i64 = row.get(0)?;
            let released: String = row.get(2)?;
            let mut presentation = Presentation {
                kind: "video".into(),
                hint: "series".into(),
                series: row.get(4)?,
                season,
                episode: number,
                episode_title: row.get(1)?,
                art: row.get(3)?,
                trickplay: row.get(6)?,
                ..Presentation::default()
            };
            dated(&mut presentation, released);
            Ok((
                number,
                PlayItem {
                    path: row.get(5)?,
                    presentation,
                },
            ))
        },
    )?;
    if found.first().map(|(number, _)| *number) != Some(chosen) {
        return Ok(Vec::new());
    }
    Ok(found.into_iter().map(|(_, item)| item).collect())
}

// An episode carries the release the catalog holds: a full ISO date
// where the provider gave one, and the year alone otherwise. The film's
// display shows the date when it has one.
fn dated(presentation: &mut Presentation, released: String) {
    if is_date(&released) {
        presentation.date = released;
        return;
    }
    presentation.year = year(&released);
}

// The year, the first four digits of the released column. A column that
// holds neither a year nor a date answers zero, which the request leaves
// out.
fn year(released: &str) -> i64 {
    released
        .get(..4)
        .and_then(|digits| digits.parse().ok())
        .unwrap_or(0)
}

// Whether the released column holds a whole date, yyyy-mm-dd, and not a
// year alone.
fn is_date(released: &str) -> bool {
    let mut parts = released.split('-');
    matches!(
        (parts.next(), parts.next(), parts.next(), parts.next()),
        (Some(year), Some(month), Some(day), None)
            if year.len() == 4 && month.len() == 2 && day.len() == 2
    )
}
