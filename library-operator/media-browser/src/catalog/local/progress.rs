// The one read of the progress store: every play an audience's people are
// on, with the work each names and every person on it. It is one statement
// across the catalog file and the progress file attached beside it as the
// `progress` schema. The store holds no catalog id, so the read joins alias
// to alias. A play of a file of several episodes holds a position in the
// whole file, so the read splits it into one row for each episode it
// covers, with `catalog/progress/parts.rs`. The screens' rules then run
// over the answer in `catalog/progress/seen.rs`.

use rusqlite::{Connection, Row, ToSql};
use serde_json::Value;

use super::collect;
use crate::catalog::progress::parts::{File, split};
use crate::catalog::progress::{Credits, Origin, Progress, Resume, Seen};

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

// The credits marks of the main file that `main_file` finds, as one text
// column of `start:end` pairs in milliseconds joined by commas, with an
// empty side for a null edge, or null when the file has none. The finished
// rule reads the credits, so each read carries them in the same statement
// as the play.
const CREDITS: &str = "(SELECT group_concat(IFNULL(marks.start_ms, '') || ':' || IFNULL(marks.end_ms, ''), ',') \
                        FROM marks \
                        WHERE marks.library = main.library AND marks.kind = 'credits' \
                          AND marks.path = main.path)";

// The main file of one item is the one `play.rs` resolves a title to: the
// least path of the item's `primary` `video` files. `item` is one of the
// two constants above and never a caller's word, so no string from outside
// reaches the query text.
//
// The lookup names the item index. The read runs it once for each play, and
// SQLite, left to itself, answers MIN(path) by walking the library's files
// in path order until one belongs to the item, which is most of the library
// for each play. On a series library of 27,000 files, the episode read of a
// series with a play on each of 157 episodes takes 1.1 s on a workstation
// that way, and 5 ms through the index.
fn main_file(item: &'static str, column: &str) -> String {
    format!(
        "SELECT {column} \
         FROM file_items main INDEXED BY file_items_library_item \
         JOIN files ON files.library = main.library AND files.path = main.path \
         WHERE main.library = works.library AND main.item = {item} \
           AND files.type = 'video' AND files.role = 'primary' \
         ORDER BY main.path LIMIT 1"
    )
}

// The credits column of one film play, read only for a play that reached
// the second half of its work. The finished rule never counts a play
// before the half as finished, because the credits line it draws is always
// in the second half, and the time rule's line is later still. So the
// lookup of the credits, the one costly part of the read, runs for the few
// plays that can be finished.
fn late_credits(item: &'static str) -> String {
    format!(
        "CASE WHEN watched.duration > 0 AND watched.position * 2 >= watched.duration \
         THEN ({}) END",
        main_file(item, CREDITS)
    )
}

// The main file of one episode play, as one JSON object: the named
// episode's part of the file, the file's parts, the probe's duration in
// milliseconds, the credits column, and, for a file of several episodes,
// each episode of it as its part, season, and episode. The split in
// `catalog/progress/parts.rs` reads it. The gate of `late_credits` cannot
// apply here, because a play in the first half of a double can still
// finish the first episode, so the read takes the file of every episode
// play. The other episodes of the file are found by the file's own key.
fn episode_file() -> String {
    let file = format!(
        "json_object('part', main.part, 'parts', main.parts, \
                     'duration', files.duration_ms, 'credits', {}, \
                     'episodes', CASE WHEN main.parts > 1 THEN json(\
                       (SELECT json_group_array(json_array(mate.part, mates.season, mates.episode)) \
                        FROM file_items mate \
                        JOIN episodes mates ON mates.library = mate.library AND mates.id = mate.item \
                        WHERE mate.library = main.library AND mate.path = main.path)) \
                     ELSE json('[]') END)",
        CREDITS
    );
    format!("({})", main_file(EPISODE_ITEM, &file))
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

// The file one row's work is in: the film's credits for a film play, and
// the episode file column for an episode play. A field that is absent or
// not a number reads as 0, so a row with no main file, such as an episode
// whose file is gone, reads as a file of one work with no credits.
fn file(credits: Option<String>, episode: Option<String>) -> File {
    let episode: Value = episode
        .and_then(|column| serde_json::from_str(&column).ok())
        .unwrap_or_default();
    let number = |field: &str| episode[field].as_i64().unwrap_or(0);
    let episodes = episode["episodes"]
        .as_array()
        .map(|list| {
            list.iter()
                .map(|mate| {
                    let at = |index: usize| mate[index].as_i64().unwrap_or(0);
                    (at(0), at(1), at(2))
                })
                .collect()
        })
        .unwrap_or_default();
    let episode_credits = episode["credits"].as_str().map(str::to_string);
    File {
        parts: number("parts"),
        part: number("part"),
        duration: number("duration") / 1_000,
        episodes,
        credits: parsed(credits.or(episode_credits)),
    }
}

// The people of one play, as a JSON array, so a name holds any character.
const PEOPLE: &str = "(SELECT json_group_array(whom.person) FROM progress.play_people whom \
                      WHERE whom.play = watched.play)";

// One row of the store as it is, before the split. `finished` is set by
// the split, which holds the credits of the right part.
fn progress(row: &Row<'_>, at: usize) -> rusqlite::Result<Progress> {
    let play: String = row.get(at)?;
    let library: String = row.get(at + 9)?;

    Ok(Progress {
        origin: Origin::of(&play, &library),
        play,
        position: row.get(at + 1)?,
        duration: row.get(at + 2)?,
        finished: false,
        running: row.get::<_, i64>(at + 3)? == 0,
        recorded: row.get(at + 4)?,
        season: row.get(at + 5)?,
        episode: row.get(at + 6)?,
    })
}

/// Every play this audience is on, one row per play and work, across every
/// library. A play whose aliases name nothing the catalog holds is
/// skipped, because there is no slot to draw for it. A play of an episode
/// in a file of several episodes answers one row for each episode it
/// covers, as `catalog/progress/parts.rs` splits it.
pub fn seen(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<Seen>> {
    let rows = collect(connection, &statement(people), &names(people), |row| {
        let people: String = row.get(14)?;
        let resume = Resume {
            library: row.get(0)?,
            kind: row.get(1)?,
            id: row.get(2)?,
            title: row.get(3)?,
            released: row.get(4)?,
            art: row.get(5)?,
            progress: progress(row, 6)?,
            exact: false,
        };
        let file = file(row.get(13)?, row.get(16)?);
        let people: Vec<String> = serde_json::from_str(&people).unwrap_or_default();
        Ok(split(resume.progress.clone(), &file)
            .into_iter()
            .map(|progress| Seen {
                resume: Resume {
                    progress,
                    ..resume.clone()
                },
                people: people.clone(),
            })
            .collect::<Vec<Seen>>())
    })?;
    Ok(rows.into_iter().flatten().collect())
}

/// The lines of SQLite's plan for the read, so a test checks how it finds
/// its rows.
#[cfg(test)]
pub fn plan(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<String>> {
    let sql = format!("EXPLAIN QUERY PLAN {}", statement(people));
    collect(connection, &sql, &names(people), |row| row.get(3))
}

// A play's aliases name a provider and an id but not a kind, and TMDb
// numbers films and series in separate ranges that overlap: id 253 is a
// film and a series. So each branch takes only the plays of its own kind,
// by the episode number that the store writes as 0 for a film and as the
// episode's number for an episode, or an episode's play would resume a
// film that shares its series' id.
fn statement(people: &[String]) -> String {
    format!(
        "WITH watched AS (\
           SELECT plays.play, plays.position, plays.duration, plays.ended, \
                  plays.recorded, plays.season, plays.episode, plays.library \
           FROM progress.plays plays WHERE {audience}\
         ), works AS (\
           SELECT watched.play, aliases.library, aliases.item, 'movie' AS kind \
           FROM watched \
           JOIN progress.play_aliases named ON named.play = watched.play \
           JOIN aliases ON aliases.alias = 'movie:' || named.provider || ':' || named.id \
           WHERE watched.episode = 0 \
           UNION \
           SELECT watched.play, aliases.library, aliases.item, 'series' AS kind \
           FROM watched \
           JOIN progress.play_aliases named ON named.play = watched.play \
           JOIN aliases ON aliases.alias = 'series:' || named.provider || ':' || named.id \
           WHERE watched.episode > 0\
         ) \
         SELECT works.library, 'movie', movies.id, movies.title, movies.released, movies.art, \
                watched.play, watched.position, watched.duration, watched.ended, \
                watched.recorded, watched.season, watched.episode, \
                {movie_credits}, {PEOPLE}, watched.library, NULL \
         FROM works \
         JOIN watched ON watched.play = works.play \
         JOIN movies ON movies.library = works.library AND movies.id = works.item \
         WHERE works.kind = 'movie' \
         UNION ALL \
         SELECT works.library, 'series', series.id, series.title, series.released, series.art, \
                watched.play, watched.position, watched.duration, watched.ended, \
                watched.recorded, watched.season, watched.episode, \
                NULL, {PEOPLE}, watched.library, {episode_file} \
         FROM works \
         JOIN watched ON watched.play = works.play \
         JOIN series ON series.library = works.library AND series.id = works.item \
         WHERE works.kind = 'series'",
        audience = audience(people),
        movie_credits = late_credits(MOVIE_ITEM),
        episode_file = episode_file(),
    )
}
