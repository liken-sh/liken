// Pick up here on an episode's row: which episodes a press marks, and
// whether the row offers the button at all.
//
// A press marks every earlier episode watched and clears every later one,
// so that the series reads from the picked episode on as not started. The
// row offers the button only where a press changes at least one episode.
// That test runs on every frame and every press while a row is open, so
// the page reads the two bounds it needs once, when it reads the progress,
// and the test compares the picked episode's numbers against them.

use super::{Series, Still};
use crate::bus::mark::{self, TitleMark};
use crate::screens::movie::{marked_duration, row};

/// Where a press of Pick up here changes anything, read once for each read
/// of the page's progress. Each bound is an episode's numbers, season then
/// episode, which is series order.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Reach {
    /// The first episode a press would mark watched: one the audience has
    /// not finished, which the catalog holds a duration for.
    first_unmarked: Option<(i64, i64)>,
    /// The last episode a press would clear: one the audience started or
    /// finished.
    last_started: Option<(i64, i64)>,
}

impl Reach {
    /// The bounds over one wall of stills. Specials, season 0, stand
    /// outside series order, so neither bound names one.
    pub fn of(stills: &[Still]) -> Self {
        let mut ordered = stills.iter().filter(|still| still.season > 0);
        Self {
            first_unmarked: ordered.clone().find(|still| markable(still)).map(numbers),
            last_started: ordered
                .rfind(|still| row::started(still.progress.as_ref()))
                .map(numbers),
        }
    }

    /// Whether a press on this episode changes any episode: an earlier one
    /// is left to mark watched, or a later one is left to clear. A special
    /// is in no order, so a press on one would change nothing.
    pub fn changes(&self, picked: &Still) -> bool {
        let at = numbers(picked);
        picked.season > 0
            && (self.first_unmarked.is_some_and(|first| first < at)
                || self.last_started.is_some_and(|last| last > at))
    }
}

impl Series {
    /// The episodes Pick up here on this episode marks, in series order.
    /// Every episode before it, across seasons, that the audience has not
    /// finished, a partly watched one included, takes a watched mark. Every
    /// episode after it that the audience started or finished takes a
    /// cleared mark, the one Clear progress on its row would send. Specials,
    /// season 0, stand outside that order, so a press marks none of them and
    /// a special's row marks nothing.
    ///
    /// An earlier episode with no duration is left out, because the store
    /// reads a position as finished only against a duration, the rule a
    /// single watched mark follows. A later episode the audience never
    /// started, or already cleared, holds nothing to clear.
    pub fn picked_up(&self, index: usize) -> Vec<mark::Episode> {
        let picked = &self.stills[index];
        if picked.season == 0 {
            return Vec::new();
        }
        let at = numbers(picked);
        self.stills
            .iter()
            .filter(|still| still.season > 0)
            .filter_map(|still| {
                let mark = match numbers(still) {
                    earlier if earlier < at && markable(still) => TitleMark::Watched,
                    later if later > at && row::started(still.progress.as_ref()) => {
                        TitleMark::Cleared
                    }
                    _ => return None,
                };
                Some(mark::Episode {
                    season: still.season,
                    episode: still.episode,
                    duration: marked_duration(still.progress.as_ref(), still.duration),
                    mark,
                })
            })
            .collect()
    }
}

// An episode's numbers, in the order that sorts episodes in series order.
fn numbers(still: &Still) -> (i64, i64) {
    (still.season, still.episode)
}

// Whether a watched mark changes this episode: the audience has not
// finished it, and the mark can state a duration for it.
fn markable(still: &Still) -> bool {
    !still
        .progress
        .as_ref()
        .is_some_and(|played| played.finished)
        && marked_duration(still.progress.as_ref(), still.duration) > 0
}
