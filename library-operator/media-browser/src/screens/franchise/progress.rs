// How far the room and each person in it reached in one franchise: the
// bar under every held member, the row each thread stands on, and the
// row the page opens on. The thread rule is the one every container
// uses, so the row the room stands on here is the row the home page's
// continue-watching card names.

use std::collections::HashMap;

use super::{Franchise, leaves};
use crate::catalog::franchise::Entry;
use crate::catalog::progress::thread;
use crate::catalog::{Progress, Resume, Source};
use crate::screens::home::resume::containers::{Episodes, Leaf, Work, on_leaves};

// The kind word a held series carries.
const SERIES: &str = "series";

/// What the read hangs on the page: one bar share per row, the row the
/// room's thread stands on with the letters of everyone present, and one
/// circle per person whose own thread stands on another row.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Marks {
    /// The share of each row's member the room reached, in the wall's
    /// own order, and none for a row no play of theirs names.
    pub bars: Vec<Option<f32>>,
    /// The row the room's thread stands on, and none where no thread of
    /// theirs stands, or the room finished the story.
    pub room: Option<usize>,
    /// The letters of everyone in the room, which draw as the stack on
    /// that row, and none where the room has no row.
    pub letters: Vec<String>,
    /// One person's letter and the row their own thread stands on, for
    /// every person whose own thread stands on a row other than the
    /// room's.
    pub solo: Vec<(String, usize)>,
}

impl Marks {
    /// The bar one row draws, and none for a row the read did not reach.
    pub fn bar(&self, row: usize) -> Option<f32> {
        self.bars.get(row).copied().flatten()
    }

    /// Whether any marker stands. The circles take their column only
    /// where one does.
    pub fn marked(&self) -> bool {
        self.room.is_some() || !self.solo.is_empty()
    }
}

/// Read how far these people reached in every held member of the order,
/// and hang the bars and the markers on the page. On the first read after
/// an open, focus lands on the room's row. The browser calls it at every
/// open and at every re-read, because only the browser holds the
/// audience.
pub fn read(page: &mut Franchise, source: &mut dyn Source, people: &[String], letters: &[String]) {
    let mut episodes = Episodes::default();
    let leaves = leaves::of(source, &mut episodes, &page.entries);
    let rows = rows(&page.entries, &leaves);
    let played = plays(source, &page.entries, people);
    let room = stands(&leaves, &rows, &played);
    let bars = bars(source, &page.entries, &leaves, &played, people);
    let solo = solo(source, &page.entries, people, letters, &leaves, &rows, room);
    page.marks = Marks {
        bars,
        letters: match room {
            Some(_) => letters.to_vec(),
            None => Vec::new(),
        },
        room,
        solo,
    };
    if !page.placed {
        if let Some(row) = room {
            page.focus = row;
        }
        page.placed = true;
    }
}

// The row every leaf stands on, which is the row of the member at the
// leaf's own story position.
fn rows(entries: &[Entry], leaves: &[Leaf]) -> Vec<Option<usize>> {
    leaves
        .iter()
        .map(|leaf| {
            entries
                .iter()
                .position(|entry| entry.position == leaf.member)
        })
        .collect()
}

// The row one audience's thread stands on, and nothing where the walk
// over the whole order offers nothing.
fn stands(
    leaves: &[Leaf],
    rows: &[Option<usize>],
    played: &HashMap<Work, Vec<Resume>>,
) -> Option<usize> {
    let offer = thread::walk(leaves.len(), &on_leaves(leaves, played))?;
    rows.get(offer.leaf).copied().flatten()
}

// The circle of every person whose own thread stands on a row other than
// the room's. A room of one person draws none, because that person's own
// walk is the room's walk.
fn solo(
    source: &mut dyn Source,
    entries: &[Entry],
    people: &[String],
    letters: &[String],
    leaves: &[Leaf],
    rows: &[Option<usize>],
    room: Option<usize>,
) -> Vec<(String, usize)> {
    if people.len() < 2 {
        return Vec::new();
    }
    people
        .iter()
        .zip(letters)
        .filter_map(|(person, letter)| {
            let alone = plays(source, entries, std::slice::from_ref(person));
            let row = stands(leaves, rows, &alone)?;
            (Some(row) != room).then(|| (letter.clone(), row))
        })
        .collect()
}

// Every play of this audience on every work the order holds, keyed by
// the work, which is the shape the walk reads them in. A series two
// members hold is read once. The catalog answers only the plays every
// named person is on and marks the ones that name exactly these people,
// so each audience needs a read of its own.
fn plays(
    source: &mut dyn Source,
    entries: &[Entry],
    people: &[String],
) -> HashMap<Work, Vec<Resume>> {
    let mut played: HashMap<Work, Vec<Resume>> = HashMap::new();
    for held in entries.iter().filter_map(|entry| entry.held.as_ref()) {
        let work = (held.library.clone(), held.id.clone());
        played
            .entry(work)
            .or_insert_with(|| source.plays_of(&held.library, &held.id, people));
    }
    played
}

// The bar under every row: the share of a film the room reached, the
// share of a series member's own run they reached, and nothing under a
// row no library holds.
fn bars(
    source: &mut dyn Source,
    entries: &[Entry],
    leaves: &[Leaf],
    played: &HashMap<Work, Vec<Resume>>,
    people: &[String],
) -> Vec<Option<f32>> {
    entries
        .iter()
        .map(|entry| {
            let held = entry.held.as_ref()?;
            if held.kind != SERIES {
                return film(played.get(&(held.library.clone(), held.id.clone()))?);
            }
            let covered = leaves
                .iter()
                .filter(|leaf| leaf.member == entry.position)
                .count();
            let inside: Vec<Progress> = source
                .episode_progress(&held.library, &held.id, people)
                .into_iter()
                .filter(|row| entry.covers(row.season, row.episode))
                .collect();
            run(&inside, covered)
        })
        .collect()
}

/// The share of a film the room reached: the share of its running time
/// their latest play stopped at, which is the share the film walls draw.
/// A film no play of theirs names draws no bar.
pub fn film(plays: &[Resume]) -> Option<f32> {
    plays
        .iter()
        .max_by_key(|play| play.progress.recorded)
        .map(|play| play.progress.played().fraction())
}

/// The share of one series member's run the room reached: an episode
/// they finished counts whole, the episode they are in the middle of
/// counts its own share, and the total is the episodes of the run the
/// catalog holds. A count stands in for a share of the running time
/// because the catalog holds no duration for many episodes. A run no
/// play of theirs names draws no bar.
pub fn run(inside: &[Progress], covered: usize) -> Option<f32> {
    if covered == 0 {
        return None;
    }
    let reached: f32 = inside
        .iter()
        .map(|row| match row.finished {
            true => 1.0,
            false => row.played().fraction(),
        })
        .sum();
    (reached > 0.0).then(|| reached / covered as f32)
}

#[cfg(test)]
mod tests;
