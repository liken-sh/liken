// Where one episode is in its main file. The scanner records each
// episode's part of a file of several episodes on its `file_items` link,
// so a play of a later episode starts at that episode's share of the
// file, and the offer after a play follows the file's last episode.

use rusqlite::Connection;

use super::collect;
use crate::catalog::Place;

// The main file is the one `play.rs` resolves an episode to: the least path
// of its `primary` `video` files, found through the item index.
const MAIN: &str = "SELECT main.part, main.parts, files.duration_ms, main.path \
                    FROM episodes item \
                    JOIN file_items main INDEXED BY file_items_library_item \
                      ON main.library = item.library AND main.item = item.id \
                    JOIN files ON files.library = main.library AND files.path = main.path \
                     AND files.type = 'video' AND files.role = 'primary' \
                    WHERE item.library = ? AND item.series = ? \
                      AND item.season = ? AND item.episode = ? \
                    ORDER BY main.path LIMIT 1";

// The numbers of the file's last episode, by its part. A part that holds no
// numbered episode has no link, so the last link is the last episode the
// catalog holds of the file.
const LAST: &str = "SELECT mates.season, mates.episode \
                    FROM file_items mate \
                    JOIN episodes mates ON mates.library = mate.library AND mates.id = mate.item \
                    WHERE mate.library = ? AND mate.path = ? \
                    ORDER BY mate.part DESC LIMIT 1";

/// The place of one episode, or the place of an episode that is the whole
/// of its file where the catalog holds no main file for it.
pub fn place(
    connection: &Connection,
    library: &str,
    series: &str,
    numbers: (i64, i64),
) -> rusqlite::Result<Place> {
    let (season, episode) = numbers;
    let found = collect(
        connection,
        MAIN,
        &[&library, &series, &season, &episode],
        |row| {
            Ok((
                Place {
                    part: row.get(0)?,
                    parts: row.get(1)?,
                    duration: row.get::<_, i64>(2)? / 1_000,
                    last: numbers,
                },
                row.get::<_, String>(3)?,
            ))
        },
    )?;
    let Some((mut place, path)) = found.into_iter().next() else {
        return Ok(Place::whole(numbers));
    };
    if place.parts > 1 {
        let last = collect(connection, LAST, &[&library, &path], |row| {
            Ok((row.get(0)?, row.get(1)?))
        })?;
        place.last = last.into_iter().next().unwrap_or(numbers);
    }
    Ok(place)
}
