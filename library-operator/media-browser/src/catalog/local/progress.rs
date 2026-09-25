// The four progress reads. Each is one statement across the catalog file
// and the progress file attached beside it as the `progress` schema. The
// store holds no catalog id, so every read joins alias to alias.

use rusqlite::{Connection, Row, ToSql};

use super::collect;
use crate::catalog::progress::{Played, Progress, Resume, finished};

// Which plays a read fetches for the audience. `Every` takes the plays
// every name is on, which is the thread rule's fetch, so a play with fewer
// people never comes back. `Any` takes the plays some name is on, which is
// the marks' fetch, and the marks then keep a leaf only when every name
// has a play of it. Both take the plays that name nobody for an empty
// audience.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Rule {
    Every,
    Any,
}

// The bound parameter list of the audience's names, `?1` upward, so a
// read that takes a library and an item numbers those after them.
fn placeholders(people: &[String]) -> String {
    let names: Vec<String> = (1..=people.len()).map(|at| format!("?{at}")).collect();
    names.join(", ")
}

// The two common table expressions every read starts from: the plays this
// audience is on, and the catalog works those plays name.
// `watched` carries `exact`: whether the play names exactly the audience
// and nobody more.
fn works(people: &[String], rule: Rule) -> String {
    let (audience, exact) = if people.is_empty() {
        (
            "NOT EXISTS (SELECT 1 FROM progress.play_people whom \
                         WHERE whom.play = plays.play)"
                .to_string(),
            "1".to_string(),
        )
    } else {
        let names = placeholders(people);
        let count = people.len();
        let on = format!(
            "(SELECT COUNT(*) FROM progress.play_people whom \
               WHERE whom.play = plays.play AND whom.person IN ({names}))"
        );
        let audience = match rule {
            Rule::Every => format!("{on} = {count}"),
            Rule::Any => format!("{on} > 0"),
        };
        let exact = format!(
            "({on} = {count} AND (SELECT COUNT(*) FROM progress.play_people whom \
                                  WHERE whom.play = plays.play) = {count})"
        );
        (audience, exact)
    };

    format!(
        "WITH watched AS (\
           SELECT plays.play, plays.position, plays.duration, plays.ended, \
                  plays.recorded, plays.season, plays.episode, {exact} AS exact \
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
// mapping serves every read.
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

// The statement behind the two reads that answer whole plays: every play
// the audience is on of every work, or of the one work `work` names. The
// library and the item bind after the names.
fn every_play(
    connection: &Connection,
    people: &[String],
    work: Option<(&str, &str)>,
) -> rusqlite::Result<Vec<Resume>> {
    let at = people.len();
    let only = match work {
        Some(_) => format!(
            "AND works.library = ?{} AND works.item = ?{}",
            at + 1,
            at + 2
        ),
        None => String::new(),
    };
    let sql = format!(
        "{works} \
         SELECT * FROM (\
           SELECT works.library AS library, 'movie' AS kind, movies.id AS id, \
                  movies.title, movies.released, movies.art, \
                  watched.play, watched.position, watched.duration, watched.ended, \
                  watched.recorded AS recorded, watched.season, watched.episode, \
                  watched.exact \
           FROM works \
           JOIN watched ON watched.play = works.play \
           JOIN movies ON movies.library = works.library AND movies.id = works.item \
           WHERE works.kind = 'movie' {only} \
           UNION ALL \
           SELECT works.library, 'series', series.id, \
                  series.title, series.released, series.art, \
                  watched.play, watched.position, watched.duration, watched.ended, \
                  watched.recorded, watched.season, watched.episode, watched.exact \
           FROM works \
           JOIN watched ON watched.play = works.play \
           JOIN series ON series.library = works.library AND series.id = works.item \
           WHERE works.kind = 'series' {only}\
         ) ORDER BY recorded DESC, library, id, play",
        works = works(people, Rule::Every),
    );

    let mut params = names(people);
    if let Some((library, id)) = &work {
        params.push(library);
        params.push(id);
    }
    collect(connection, &sql, &params, |row| {
        Ok(Resume {
            library: row.get(0)?,
            kind: row.get(1)?,
            id: row.get(2)?,
            title: row.get(3)?,
            released: row.get(4)?,
            art: row.get(5)?,
            progress: progress(row, 6)?,
            exact: row.get(13)?,
        })
    })
}

/// Every play this audience is on, one row per play and work across every
/// library, newest first. A play whose aliases name nothing the catalog
/// holds is skipped, because there is no slot to draw for it.
pub fn resumes(connection: &Connection, people: &[String]) -> rusqlite::Result<Vec<Resume>> {
    every_play(connection, people, None)
}

// Every play this audience is on of one work, newest first.
pub fn plays(
    connection: &Connection,
    library: &str,
    id: &str,
    people: &[String],
) -> rusqlite::Result<Vec<Resume>> {
    every_play(connection, people, Some((library, id)))
}

// The marks' filter, as the clause that keeps a leaf only when every
// person of the audience has a play of it in any group. `keys` are the
// columns that name a leaf in the `latest` rows, and `marked` selects the
// same columns from the `marked` expression. An empty audience keeps every
// leaf, because its plays already name nobody.
fn marked(people: &[String], keys: &str, marked: &str) -> String {
    match people.is_empty() {
        true => String::new(),
        false => format!("AND ({keys}) IN (SELECT {marked} FROM marked)"),
    }
}

/// Every movie of one library this audience has a play of, as the item's
/// id and the latest play's position and duration.
// A movie is in the answer when every person of the audience has a play
// of it, in any group.
pub fn by_item(
    connection: &Connection,
    library: &str,
    people: &[String],
) -> rusqlite::Result<Vec<(String, Played)>> {
    let at = people.len();
    let sql = format!(
        "{works}, marked AS (\
           SELECT works.item AS item \
           FROM works JOIN progress.play_people whom ON whom.play = works.play \
           WHERE works.kind = 'movie' AND works.library = ?{library_at} \
             AND whom.person IN ({names}) \
           GROUP BY works.item HAVING COUNT(DISTINCT whom.person) = {count}\
         ), latest AS (\
           SELECT works.item AS item, watched.position AS position, \
                  watched.duration AS duration, \
                  ROW_NUMBER() OVER (PARTITION BY works.item \
                                     ORDER BY watched.recorded DESC, watched.play) AS newest \
           FROM works JOIN watched ON watched.play = works.play \
           WHERE works.kind = 'movie' AND works.library = ?{library_at} {only}\
         ) \
         SELECT item, position, duration FROM latest WHERE newest = 1",
        works = works(people, Rule::Any),
        names = placeholders(people),
        count = people.len(),
        only = marked(people, "works.item", "item"),
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
// An episode is in the answer when every person of the audience has a
// play of it, in any group.
pub fn episodes(
    connection: &Connection,
    library: &str,
    series: &str,
    people: &[String],
) -> rusqlite::Result<Vec<Progress>> {
    let at = people.len();
    let sql = format!(
        "{works}, marked AS (\
           SELECT watched.season AS season, watched.episode AS episode \
           FROM works \
           JOIN watched ON watched.play = works.play \
           JOIN progress.play_people whom ON whom.play = works.play \
           WHERE works.library = ?{library_at} AND works.item = ?{item_at} \
             AND whom.person IN ({names}) \
           GROUP BY watched.season, watched.episode \
           HAVING COUNT(DISTINCT whom.person) = {count}\
         ), latest AS (\
           SELECT watched.play, watched.position, watched.duration, watched.ended, \
                  watched.recorded, watched.season, watched.episode, \
                  ROW_NUMBER() OVER (PARTITION BY watched.season, watched.episode \
                                     ORDER BY watched.recorded DESC, watched.play) AS newest \
           FROM works JOIN watched ON watched.play = works.play \
           WHERE works.library = ?{library_at} AND works.item = ?{item_at} {only}\
         ) \
         SELECT {COLUMNS} FROM latest WHERE newest = 1 ORDER BY season, episode",
        works = works(people, Rule::Any),
        names = placeholders(people),
        count = people.len(),
        only = marked(people, "watched.season, watched.episode", "season, episode"),
        library_at = at + 1,
        item_at = at + 2,
    );

    let mut params = names(people);
    params.push(&library);
    params.push(&series);
    collect(connection, &sql, &params, |row| progress(row, 0))
}
