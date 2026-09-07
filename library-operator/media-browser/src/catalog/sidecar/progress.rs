// The three progress reads. Each is one statement across the catalog file
// and the progress file attached beside it as the `progress` schema. The
// store holds no catalog id, so every read joins alias to alias.

use rusqlite::{Connection, Row, ToSql};

use super::collect;
use crate::catalog::progress::{Played, Progress, Resume, finished};

// The two common table expressions every read starts from: the plays this
// audience is on, and the catalog works those plays name. A play counts when
// every name in `people` has a row on it, so a person alone sees the plays
// they shared as well as their own. An empty audience takes the plays that
// name nobody. The names bind as `?1` upward, so a read that takes a library
// and an item numbers those after them.
fn works(people: &[String]) -> String {
    let audience = if people.is_empty() {
        "NOT EXISTS (SELECT 1 FROM progress.play_people whom \
                     WHERE whom.play = plays.play)"
            .to_string()
    } else {
        let names: Vec<String> = (1..=people.len()).map(|at| format!("?{at}")).collect();
        format!(
            "(SELECT COUNT(*) FROM progress.play_people whom \
               WHERE whom.play = plays.play AND whom.person IN ({names})) = {count}",
            names = names.join(", "),
            count = people.len(),
        )
    };

    format!(
        "WITH watched AS (\
           SELECT plays.play, plays.position, plays.duration, plays.ended, \
                  plays.recorded, plays.season, plays.episode \
           FROM progress.plays plays WHERE {audience}\
         ), works AS (\
           SELECT watched.play, aliases.library, aliases.item, 'movie' AS kind \
           FROM watched \
           JOIN progress.play_aliases named ON named.play = watched.play \
           JOIN aliases ON aliases.alias = 'movie:' || named.provider || ':' || named.id \
           UNION \
           SELECT watched.play, aliases.library, aliases.item, 'series' AS kind \
           FROM watched \
           JOIN progress.play_aliases named ON named.play = watched.play \
           JOIN aliases ON aliases.alias = 'series:' || named.provider || ':' || named.id\
         )"
    )
}

// The audience as bound parameters, so no name from a flag or a bus message
// reaches the SQL text.
fn names(people: &[String]) -> Vec<&dyn ToSql> {
    people.iter().map(|person| person as &dyn ToSql).collect()
}

// The seven progress columns every read selects, in this order, so one
// mapping serves all three reads.
const COLUMNS: &str = "play, position, duration, ended, recorded, season, episode";

fn progress(row: &Row<'_>, at: usize) -> rusqlite::Result<Progress> {
    let position: i64 = row.get(at + 1)?;
    let duration: i64 = row.get(at + 2)?;
    let ended: i64 = row.get(at + 3)?;

    Ok(Progress {
        play: row.get(at)?,
        position,
        duration,
        finished: finished(position, duration),
        running: ended == 0,
        recorded: row.get(at + 4)?,
        season: row.get(at + 5)?,
        episode: row.get(at + 6)?,
    })
}

/// Every work this audience has a play of, one row per work across every
/// library, newest first. The row is the latest play of the work, finished
/// or not; the caller decides what a finished row means. A play whose
/// aliases name nothing the catalog holds is skipped, because there is no
/// slot to draw for it.
pub fn resumes(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<Resume>> {
    let sql = format!(
        "{works}, latest AS (\
           SELECT works.library, works.item, works.kind, watched.play, watched.position, \
                  watched.duration, watched.ended, watched.recorded, watched.season, \
                  watched.episode, \
                  ROW_NUMBER() OVER (PARTITION BY works.library, works.item \
                                     ORDER BY watched.recorded DESC, watched.play) AS newest \
           FROM works JOIN watched ON watched.play = works.play\
         ) \
         SELECT * FROM (\
           SELECT latest.library AS library, 'movie' AS kind, movies.id AS id, \
                  movies.title, movies.released, movies.art, \
                  latest.play, latest.position, latest.duration, latest.ended, \
                  latest.recorded AS recorded, latest.season, latest.episode \
           FROM latest \
           JOIN movies ON movies.library = latest.library AND movies.id = latest.item \
           WHERE latest.newest = 1 AND latest.kind = 'movie' \
           UNION ALL \
           SELECT latest.library, 'series', series.id, \
                  series.title, series.released, series.art, \
                  latest.play, latest.position, latest.duration, latest.ended, \
                  latest.recorded, latest.season, latest.episode \
           FROM latest \
           JOIN series ON series.library = latest.library AND series.id = latest.item \
           WHERE latest.newest = 1 AND latest.kind = 'series'\
         ) ORDER BY recorded DESC, library, id",
        works = works(people),
    );

    collect(connection, &sql, &names(people), |row| {
        Ok(Resume {
            library: row.get(0)?,
            kind: row.get(1)?,
            id: row.get(2)?,
            title: row.get(3)?,
            released: row.get(4)?,
            art: row.get(5)?,
            progress: progress(row, 6)?,
        })
    })
}

/// Where this audience last reached in one work, as a list of one, or an
/// empty list where no play of theirs names it. A series answers its latest
/// episode row, because that is where the audience left the show.
pub fn of(
    connection: &Connection,
    library: &str,
    id: &str,
    people: &[String],
) -> rusqlite::Result<Vec<Progress>> {
    let at = people.len();
    let sql = format!(
        "{works} \
         SELECT watched.play, watched.position, watched.duration, watched.ended, \
                watched.recorded, watched.season, watched.episode \
         FROM works JOIN watched ON watched.play = works.play \
         WHERE works.library = ?{library_at} AND works.item = ?{item_at} \
         ORDER BY watched.recorded DESC, watched.play LIMIT 1",
        works = works(people),
        library_at = at + 1,
        item_at = at + 2,
    );

    let mut params = names(people);
    params.push(&library);
    params.push(&id);
    collect(connection, &sql, &params, |row| progress(row, 0))
}

/// Every movie of one library this audience has a play of, as the item's
/// id and the latest play's position and duration. The read takes the
/// movie half of the works alone, because a wall draws a bar under a film
/// and not under a show. It keeps a finished play, because a watched film
/// draws a whole bar.
pub fn by_item(
    connection: &Connection,
    library: &str,
    people: &[String],
) -> rusqlite::Result<Vec<(String, Played)>> {
    let at = people.len();
    let sql = format!(
        "{works}, latest AS (\
           SELECT works.item AS item, watched.position AS position, \
                  watched.duration AS duration, \
                  ROW_NUMBER() OVER (PARTITION BY works.item \
                                     ORDER BY watched.recorded DESC, watched.play) AS newest \
           FROM works JOIN watched ON watched.play = works.play \
           WHERE works.kind = 'movie' AND works.library = ?{library_at}\
         ) \
         SELECT item, position, duration FROM latest WHERE newest = 1",
        works = works(people),
        library_at = at + 1,
    );

    let mut params = names(people);
    params.push(&library);
    collect(connection, &sql, &params, |row| {
        Ok((
            row.get(0)?,
            Played {
                position: row.get(1)?,
                duration: row.get(2)?,
            },
        ))
    })
}

/// Where this audience reached in each episode of one series: the latest
/// play per season and episode, in aired order.
pub fn episodes(
    connection: &Connection,
    library: &str,
    series: &str,
    people: &[String],
) -> rusqlite::Result<Vec<Progress>> {
    let at = people.len();
    let sql = format!(
        "{works}, latest AS (\
           SELECT watched.play, watched.position, watched.duration, watched.ended, \
                  watched.recorded, watched.season, watched.episode, \
                  ROW_NUMBER() OVER (PARTITION BY watched.season, watched.episode \
                                     ORDER BY watched.recorded DESC, watched.play) AS newest \
           FROM works JOIN watched ON watched.play = works.play \
           WHERE works.library = ?{library_at} AND works.item = ?{item_at}\
         ) \
         SELECT {COLUMNS} FROM latest WHERE newest = 1 ORDER BY season, episode",
        works = works(people),
        library_at = at + 1,
        item_at = at + 2,
    );

    let mut params = names(people);
    params.push(&library);
    params.push(&series);
    collect(connection, &sql, &params, |row| progress(row, 0))
}
