// The plays the progress stream named, resolved to the works and the
// people they name. The stream carries each changed row's primary key and
// nothing else, so the browser reads the play's aliases and people from
// the progress file attached beside the catalog. Both reads go by the
// play, which leads the primary key of each table, so each is one index
// lookup, and a screen that draws none of the works pays nothing more.

use std::collections::{BTreeMap, BTreeSet};

use rusqlite::Connection;

use crate::catalog::Touched;

// The catalog works one play names: its aliases, as the progress reads
// resolve them in `progress.rs`, the film by its movie alias and an
// episode by its series' alias.
const WORKS: &str = "SELECT aliases.library, aliases.item \
                     FROM progress.play_aliases named \
                     JOIN aliases ON aliases.alias = 'movie:' || named.provider || ':' || named.id \
                     WHERE named.play = ?1 \
                     UNION \
                     SELECT aliases.library, aliases.item \
                     FROM progress.play_aliases named \
                     JOIN aliases ON aliases.alias = 'series:' || named.provider || ':' || named.id \
                     WHERE named.play = ?1 \
                     ORDER BY 1, 2";

const PEOPLE: &str = "SELECT person FROM progress.play_people WHERE play = ?1 ORDER BY person";

/// Resolve each play to the works and the people it names. `plays` maps
/// each play to the people a change took off it, which the answer names
/// beside the people it holds now. A play whose aliases name no work of
/// the catalog touches no screen, and the answer leaves it out.
pub fn resolve(
    connection: &Connection,
    plays: &BTreeMap<String, BTreeSet<String>>,
) -> rusqlite::Result<Vec<Touched>> {
    let mut works = connection.prepare_cached(WORKS)?;
    let mut people = connection.prepare_cached(PEOPLE)?;
    let mut touched = Vec::new();
    for (play, removed) in plays {
        let named: Vec<(String, String)> = works
            .query_map([play], |row| Ok((row.get(0)?, row.get(1)?)))?
            .collect::<rusqlite::Result<_>>()?;
        if named.is_empty() {
            continue;
        }
        let mut on: BTreeSet<String> = people
            .query_map([play], |row| row.get(0))?
            .collect::<rusqlite::Result<_>>()?;
        on.extend(removed.iter().cloned());
        touched.push(Touched {
            works: named,
            people: on.into_iter().collect(),
        });
    }
    Ok(touched)
}
