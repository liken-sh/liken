// The bars a wall draws under its films. One progress read per library
// the wall's slots span, because a search wall and a genre wall hold slots
// of several libraries at once.

use crate::catalog::Source;
use crate::screens::Item;

// The kind word a film's slot carries, the one kind of slot this read
// draws a bar under. A series, an episode, a set, a franchise, and a person
// carry their own words and take no bar here.
const MOVIES: &str = "movies";

/// Attach the plays these people have of the films among these slots, and
/// clear the field on every film slot no play of theirs names, so a re-read
/// after a lapse takes the stale bars away. A slot of any other kind is left
/// as it is.
pub fn read(items: &mut [Item], source: &mut dyn Source, people: &[String]) {
    let mut libraries: Vec<String> = items
        .iter()
        .filter(|item| filmed(item))
        .map(|item| item.library.clone())
        .collect();
    libraries.sort();
    libraries.dedup();

    for item in items.iter_mut().filter(|item| filmed(item)) {
        item.progress = None;
    }
    for library in libraries {
        let played = source.progress_by_item(&library, people);
        for item in items.iter_mut().filter(|item| filmed(item)) {
            if item.library == library {
                item.progress = played.get(&item.id).copied();
            }
        }
    }
}

// Whether a slot draws a film, the one kind the map keys.
fn filmed(item: &Item) -> bool {
    item.kind == MOVIES && item.episode.is_none()
}
