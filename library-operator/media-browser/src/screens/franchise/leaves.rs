// The leaves of a franchise's order: every held film and every covered
// episode, in story order. The home page's continue-watching row and the
// franchise page's progress read both walk them, so a card and the page
// agree on where the room stands.

use crate::catalog::Source;
use crate::catalog::franchise::{self, Entry};
use crate::screens::home::resume::containers::{Episodes, Leaf, leaf, still};

// The kind word a held series carries.
const SERIES: &str = "series";

/// Every held film and every covered episode, in story order. Each leaf
/// carries the position of the member it came from, which is how a leaf
/// finds its row.
pub fn of(source: &mut dyn Source, episodes: &mut Episodes, entries: &[Entry]) -> Vec<Leaf> {
    entries
        .iter()
        .flat_map(|entry| member(source, episodes, entry))
        .collect()
}

// The leaves of one franchise member: the film, or the episodes of the
// series inside its runs.
fn member(source: &mut dyn Source, episodes: &mut Episodes, entry: &Entry) -> Vec<Leaf> {
    let Some(held) = &entry.held else {
        return Vec::new();
    };
    if held.kind != SERIES {
        return vec![leaf(franchise::slot(held.clone()), entry.position)];
    }
    episodes
        .of(source, &held.library, &held.id)
        .into_iter()
        .filter(|episode| entry.covers(episode.season, episode.episode))
        .map(|episode| {
            leaf(
                still(&held.library, &held.id, &held.title, episode),
                entry.position,
            )
        })
        .collect()
}

#[cfg(test)]
mod tests;
