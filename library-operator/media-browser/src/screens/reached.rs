// How far the people watching got in each film a screen draws a poster of.
// Every poster of a film draws it the same way, on a wall, in a strip, and
// on a franchise page, so the people choosing a film see who has seen it
// wherever they are. The bar under the poster is how far all of them got,
// and while more than one person watches, a face for each person with a play
// of the film draws along the foot of the poster.

use std::collections::HashMap;

use crate::audience::Viewer;
use crate::catalog::{Reach, Source, Work};
use crate::screens::Item;

// The kind word a film's slot carries, the one kind of slot that draws a
// reach. A series, an episode, a set, a franchise, and a person carry their
// own words and are left as they are.
const MOVIES: &str = "movies";

/// How far the people watching got in one film, as its poster draws it.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Reached {
    /// The share of the film every person watching reached, and nothing
    /// where one of them has no play of it.
    pub share: Option<f32>,
    /// The people watching who have a play of the film, in the audience's
    /// order. Empty while one person or nobody watches, because then the
    /// bar says it all.
    pub faces: Vec<Viewer>,
}

impl Reached {
    /// The reach of one film for these people, from the store's answer.
    /// `viewers` are the same people as circles draw them, in the same
    /// order.
    pub fn of(reach: Option<&Reach>, people: &[String], viewers: &[Viewer]) -> Self {
        let Some(reach) = reach else {
            return Self::default();
        };
        let names = reach.names();
        Self {
            share: reach.shared(people),
            faces: match people.len() > 1 {
                true => viewers
                    .iter()
                    .filter(|viewer| names.contains(&viewer.name))
                    .cloned()
                    .collect(),
                false => Vec::new(),
            },
        }
    }
}

/// Read how far these people got in every film among these slots, and
/// clear it on every film slot no play of theirs names, so a re-read after
/// the audience changes takes the old bars and faces away.
pub fn read(items: &mut [Item], source: &mut dyn Source, people: &[String], viewers: &[Viewer]) {
    if !items.iter().any(filmed) {
        return;
    }
    let reach = source.film_reach(people);
    fill(items, &reach, people, viewers);
}

/// Hang this answer on the film slots, for a screen that reads the answer
/// once and fills several lists of slots from it.
pub fn fill(
    items: &mut [Item],
    reach: &HashMap<Work, Reach>,
    people: &[String],
    viewers: &[Viewer],
) {
    for item in items.iter_mut().filter(|item| filmed(item)) {
        let work = (item.library.clone(), item.id.clone());
        item.reached = Reached::of(reach.get(&work), people, viewers);
    }
}

/// Whether these slots draw the reach of this film.
pub fn draws(items: &[Item], library: &str, id: &str) -> bool {
    items
        .iter()
        .any(|item| filmed(item) && item.library == library && item.id == id)
}

// Whether a slot draws a film.
fn filmed(item: &Item) -> bool {
    item.kind == MOVIES && item.episode.is_none()
}
