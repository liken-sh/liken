// The two reads behind a series' page. The first reads the series row,
// its body, and the count of its seasons in one statement. The second
// lists every episode in aired order, with the plot the header draws
// while that episode has focus.

use rusqlite::Connection;

use super::collect;
use super::item;
use crate::catalog::{Episode, SeriesDetails, art};

/// One series' details, as a list of one, or an empty list where the
/// library holds no series under that id.
pub fn series(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<SeriesDetails>> {
    let sql = "SELECT item.title, item.released, item.duration, \
                      json_extract(item.body, '$.contentRating'), \
                      json_extract(item.body, '$.tagline'), \
                      json_extract(item.body, '$.plot'), \
                      json_extract(item.body, '$.genres'), \
                      json_extract(item.body, '$.creators'), \
                      json_extract(item.body, '$.cast'), \
                      json_extract(item.body, '$.studios'), \
                      json_extract(item.body, '$.ratings'), \
                      (SELECT COUNT(DISTINCT episodes.season) FROM episodes \
                        WHERE episodes.library = item.library \
                        AND episodes.series = item.id) \
               FROM series item WHERE item.library = ? AND item.id = ?";
    let mut found = collect(connection, sql, &[&library, &id], |row| {
        Ok(SeriesDetails {
            title: row.get(0)?,
            released: row.get(1)?,
            duration: row.get(2)?,
            rating: item::text(row, 3)?,
            tagline: item::text(row, 4)?,
            plot: item::text(row, 5)?,
            genres: item::strings(&item::text(row, 6)?),
            creators: item::strings(&item::text(row, 7)?),
            cast: item::credits(&item::text(row, 8)?),
            studios: item::strings(&item::text(row, 9)?),
            ratings: item::ratings(&item::text(row, 10)?),
            seasons: row.get(11)?,
            backdrop: String::new(),
            logo: String::new(),
        })
    })?;

    // A series page draws no trailer button, so the two image roles are
    // all it keeps of the read by role.
    if let Some(details) = found.first_mut() {
        for (role, path) in item::art(connection, library, id)? {
            match role.as_str() {
                "backdrop" => details.backdrop = path,
                "logo" => details.logo = path,
                _ => {}
            }
        }
    }
    Ok(found)
}

/// Every episode of one series, in aired order, through the index on
/// (library, series, season, episode). The series' own art comes with
/// every row, because an episode the catalog holds no still for draws
/// its series' art in the still's place.
pub fn episodes(
    connection: &Connection,
    library: &str,
    id: &str,
) -> rusqlite::Result<Vec<Episode>> {
    let sql = "SELECT episodes.season, episodes.episode, episodes.title, \
                      episodes.released, episodes.duration, \
                      json_extract(episodes.body, '$.plot'), episodes.art, episodes.id, \
                      IFNULL(series.art, ''), IFNULL(series.arts, '[]') \
               FROM episodes \
               LEFT JOIN series ON series.library = episodes.library \
               AND series.id = episodes.series \
               WHERE episodes.library = ? AND episodes.series = ? \
               ORDER BY episodes.season, episodes.episode";
    collect(connection, sql, &[&library, &id], |row| {
        let still = art::still(
            &item::text(row, 6)?,
            &item::text(row, 8)?,
            &item::strings(&item::text(row, 9)?),
        );
        Ok(Episode {
            season: row.get(0)?,
            episode: row.get(1)?,
            title: row.get(2)?,
            released: row.get(3)?,
            duration: row.get(4)?,
            plot: item::text(row, 5)?,
            art: still,
            id: row.get(7)?,
        })
    })
}
