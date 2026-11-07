// The one read of the progress store: every play an audience's people are
// on, with the work each names and every person on it. It is one statement
// across the catalog file and the progress file attached beside it as the
// `progress` schema. The store holds no catalog id, so the read joins alias
// to alias. The screens' rules then run over the answer in
// `catalog/progress/seen.rs`.

use rusqlite::{Connection, Row, ToSql};

use super::collect;
use crate::catalog::progress::{Credits, Progress, Resume, Seen, finished};

// The bound parameter list of the audience's names, `?1` upward.
fn placeholders(people: &[String]) -> String {
    let names: Vec<String> = (1..=people.len()).map(|at| format!("?{at}")).collect();
    names.join(", ")
}

// The plays this audience is on: those that name some person of it, found
// through `play_people_person`, or, for an empty audience, those that name
// nobody.
fn audience(people: &[String]) -> String {
    match people.is_empty() {
        true => "NOT EXISTS (SELECT 1 FROM progress.play_people whom \
                             WHERE whom.play = plays.play)"
            .to_string(),
        false => format!(
            "plays.play IN (SELECT whom.play FROM progress.play_people whom \
                            WHERE whom.person IN ({}))",
            placeholders(people)
        ),
    }
}

// The audience as bound parameters, so no name from a flag or a bus message
// reaches the SQL text.
fn names(people: &[String]) -> Vec<&dyn ToSql> {
    people.iter().map(|person| person as &dyn ToSql).collect()
}

// The item a movie play names in `file_items`: the movie itself.
const MOVIE_ITEM: &str = "works.item";

// The item an episode play names in `file_items`: the episode row of the
// series at the play's season and episode.
const EPISODE_ITEM: &str = "(SELECT episodes.id FROM episodes \
                            WHERE episodes.library = works.library \
                              AND episodes.series = works.item \
                              AND episodes.season = watched.season \
                              AND episodes.episode = watched.episode)";

// The credits marks of the main file of one item, as one text column of
// `start:end` pairs in milliseconds joined by commas, with an empty side
// for a null edge, or null when the file has none. The finished rule reads
// the credits, so each read carries them in the same statement as the play.
// The main file is the one `play.rs` resolves a title to: the least path
// of the item's `primary` `video` files. `item` is one of the two
// constants above and never a caller's word, so no string from outside
// reaches the query text.
//
// The lookup names the item index. The read runs it once for each play, and
// SQLite, left to itself, answers MIN(path) by walking the library's files
// in path order until one belongs to the item, which is most of the library
// for each play. On a series library of 27,000 files, the episode read of a
// series with a play on each of 157 episodes takes 1.1 s on a workstation
// that way, and 5 ms through the index.
fn credits(item: &'static str) -> String {
    format!(
        "(SELECT group_concat(IFNULL(marks.start_ms, '') || ':' || IFNULL(marks.end_ms, ''), ',') \
          FROM marks \
          WHERE marks.library = works.library AND marks.kind = 'credits' \
            AND marks.path = (SELECT MIN(files.path) \
                              FROM file_items INDEXED BY file_items_library_item \
                              JOIN files ON files.library = file_items.library \
                                        AND files.path = file_items.path \
                              WHERE file_items.library = works.library \
                                AND file_items.item = {item} \
                                AND files.type = 'video' AND files.role = 'primary'))"
    )
}

// The credits column as the finished rule takes it, in seconds. A side that
// does not parse reads as absent, the way a null edge does.
fn parsed(column: Option<String>) -> Vec<Credits> {
    let seconds = |side: &str| side.parse::<i64>().ok().map(|ms| ms as f64 / 1_000.0);
    column
        .unwrap_or_default()
        .split(',')
        .filter_map(|pair| pair.split_once(':'))
        .map(|(start, end)| Credits {
            start: seconds(start),
            end: seconds(end),
        })
        .collect()
}

// The credits column of one play, read only for a play that reached the
// second half of its work. The finished rule never counts a play before
// the half as finished, because the credits line it draws is always in the
// second half, and the time rule's line is later still. So the lookup of
// the credits, the one costly part of the read, runs for the few plays
// that can be finished.
fn late_credits(item: &'static str) -> String {
    format!(
        "CASE WHEN watched.duration > 0 AND watched.position * 2 >= watched.duration \
         THEN {} END",
        credits(item)
    )
}

// The people of one play, as a JSON array, so a name holds any character.
const PEOPLE: &str = "(SELECT json_group_array(whom.person) FROM progress.play_people whom \
                      WHERE whom.play = watched.play)";

fn progress(row: &Row<'_>, at: usize) -> rusqlite::Result<Progress> {
    let position: i64 = row.get(at + 1)?;
    let duration: i64 = row.get(at + 2)?;
    let ended: i64 = row.get(at + 3)?;
    let credits = parsed(row.get(at + 7)?);

    Ok(Progress {
        play: row.get(at)?,
        position,
        duration,
        finished: finished(position, duration, &credits),
        running: ended == 0,
        recorded: row.get(at + 4)?,
        season: row.get(at + 5)?,
        episode: row.get(at + 6)?,
    })
}

/// Every play this audience is on, one row per play and work, across every
/// library. A play whose aliases name nothing the catalog holds is
/// skipped, because there is no slot to draw for it.
pub fn seen(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<Seen>> {
    collect(connection, &statement(people), &names(people), |row| {
        let people: String = row.get(14)?;
        Ok(Seen {
            resume: Resume {
                library: row.get(0)?,
                kind: row.get(1)?,
                id: row.get(2)?,
                title: row.get(3)?,
                released: row.get(4)?,
                art: row.get(5)?,
                progress: progress(row, 6)?,
                exact: false,
            },
            people: serde_json::from_str(&people).unwrap_or_default(),
        })
    })
}

/// The lines of SQLite's plan for the read, so a test checks how it finds
/// its rows.
#[cfg(test)]
pub fn plan(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<String>> {
    let sql = format!("EXPLAIN QUERY PLAN {}", statement(people));
    collect(connection, &sql, &names(people), |row| row.get(3))
}

fn statement(people: &[String]) -> String {
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
         ) \
         SELECT works.library, 'movie', movies.id, movies.title, movies.released, movies.art, \
                watched.play, watched.position, watched.duration, watched.ended, \
                watched.recorded, watched.season, watched.episode, \
                {movie_credits}, {PEOPLE} \
         FROM works \
         JOIN watched ON watched.play = works.play \
         JOIN movies ON movies.library = works.library AND movies.id = works.item \
         WHERE works.kind = 'movie' \
         UNION ALL \
         SELECT works.library, 'series', series.id, series.title, series.released, series.art, \
                watched.play, watched.position, watched.duration, watched.ended, \
                watched.recorded, watched.season, watched.episode, \
                {episode_credits}, {PEOPLE} \
         FROM works \
         JOIN watched ON watched.play = works.play \
         JOIN series ON series.library = works.library AND series.id = works.item \
         WHERE works.kind = 'series'",
        audience = audience(people),
        movie_credits = late_credits(MOVIE_ITEM),
        episode_credits = late_credits(EPISODE_ITEM),
    )
}
