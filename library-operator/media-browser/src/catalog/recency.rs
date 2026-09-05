// The fold behind the two recency queries. The sidecar and the sample
// answer the same query, so one rule decides what a strip shows. This
// module holds the constants the recency queries are bounded by, the
// candidate a read answers with before the fold, and the fold that turns
// candidates into slots.

use super::{InSeries, Slot, Title};
use crate::catalog::Fold;

/// The window, in days, inside which an episode's release and its
/// arrival count as airing. The number is a guess to live with, beside
/// the other numbers here.
pub const WINDOW_DAYS: i64 = 14;

/// The window, in days, inside which a release date counts as current.
/// The released strip shows what is new in the world and nothing older
/// than this, and the wall behind it shows everything.
pub const CURRENT_DAYS: i64 = 30;

/// How many candidate rows a read takes, newest first, before the fold.
/// The bound keeps the read small on a catalog of thousands.
pub const CANDIDATES: usize = 120;

/// How many slots a strip shows of what the fold answered. The wall
/// behind "see all" shows the rest.
pub const SHOWN: usize = 24;

/// How many folded slots a recency read collects before it stops
/// paging: twice what a strip shows, so the added strip still fills
/// after it drops what the released strip shows. A season drop of a
/// hundred episodes is one slot, so a read that counted rows would
/// stop far short of a strip.
pub const FILL: usize = SHOWN * 2;

/// The most pages of `CANDIDATES` rows a recency read walks before it
/// stops, whatever the fold made of them. It bounds the read on a
/// catalog whose newest arrivals are all one series.
pub const PAGES: usize = 8;

/// A person enters the pool with more works than this. A person
/// credited in one or two titles makes a strip of one or two slots.
pub const WORKS_FLOOR: u64 = 3;

/// How many candidates the day draws from the pool. Four is enough for
/// one of each of the three kinds and one more, and a guess to live with
/// beside the other numbers here.
pub const DRAWN: usize = 4;

const DAY: i64 = 86_400;

/// One row a recency read answers with before the fold: a movie as its
/// slot, or an episode with its series row read beside it, because a
/// folded episode becomes a slot for the series with the series'
/// poster.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Candidate {
    Movie {
        slot: Slot,
    },
    Episode {
        library: String,
        episode: Title,
        added: i64,
        season: i64,
        number: i64,
        series: Title,
    },
}

/// The fold behind a recency strip. The read answers at most `PAGES`
/// pages in one vector, and the fold consumes the same `CANDIDATES`-row
/// prefixes as separate page reads. It stops when a page is short, the
/// prefix makes `FILL` slots, or it consumes `PAGES` pages.
pub fn filled(fold: Fold, candidates: Vec<Candidate>) -> Vec<Slot> {
    let mut read = Vec::new();
    for page in candidates.chunks(CANDIDATES).take(PAGES) {
        let short = page.len() < CANDIDATES;
        read.extend_from_slice(page);
        if short || self::fold(read.clone(), fold).len() >= FILL {
            break;
        }
    }
    self::fold(read, fold)
}

/// The fold, over candidates in the query's order, newest first. A
/// movie is a slot. An episode that stands alone is a slot with its
/// still. Every other episode folds to its series, and a series appears
/// once, at the newest date among its folded episodes.
pub fn fold(candidates: Vec<Candidate>, fold: Fold) -> Vec<Slot> {
    // The Shows fold has a pass of its own, because it answers one slot
    // per series and not one per candidate that stands alone.
    if let Fold::Shows { today } = fold {
        return shows(candidates, today);
    }
    let mut slots: Vec<Slot> = Vec::new();
    for candidate in candidates {
        match candidate {
            Candidate::Movie { slot } => slots.push(slot),
            Candidate::Episode {
                library,
                episode,
                added,
                season,
                number,
                series,
            } => {
                if stands_alone(fold, &episode.released, added) {
                    slots.push(episode_slot(&library, episode, season, number, &series));
                    continue;
                }
                let folded = slots.iter().any(|slot| {
                    slot.kind == "series" && slot.library == library && slot.id == series.id
                });
                if !folded {
                    let mut slot = Slot::of(&library, "series", series);
                    slot.released = episode.released;
                    slots.push(slot);
                }
            }
        }
    }
    slots
}

// The Shows fold: a movie is its own slot, and every episode of a series
// folds to one slot for the series, at the place the series first came
// in.
fn shows(candidates: Vec<Candidate>, today: i64) -> Vec<Slot> {
    let mut slots: Vec<Slot> = Vec::new();
    let mut shows: Vec<(usize, Show)> = Vec::new();
    for candidate in candidates {
        match candidate {
            Candidate::Movie { slot } => slots.push(slot),
            Candidate::Episode {
                library,
                episode,
                season,
                number,
                series,
                ..
            } => {
                let new = usize::from(current(&episode.released, today));
                match shows
                    .iter()
                    .position(|(_, show)| show.holds(&library, &series.id))
                {
                    Some(at) => shows[at].1.add(episode, season, number, new),
                    None => {
                        shows.push((
                            slots.len(),
                            Show {
                                library,
                                series,
                                episode,
                                season,
                                number,
                                new,
                            },
                        ));
                        slots.push(Slot::default());
                    }
                }
            }
        }
    }
    // Each show takes the place its first episode held, so the slots keep
    // the order the candidates came in.
    for (at, show) in shows {
        slots[at] = show.slot();
    }
    slots
}

// The episodes of one series folded together: the series, the newest
// episode among them, and how many of them are current.
struct Show {
    library: String,
    series: Title,
    episode: Title,
    season: i64,
    number: i64,
    new: usize,
}

impl Show {
    // Whether this is the show of that library and series.
    fn holds(&self, library: &str, series: &str) -> bool {
        self.library == library && self.series.id == series
    }

    // One more episode folded in. It counts toward `new` where it is
    // current, and it takes the still where it is the newest.
    fn add(&mut self, episode: Title, season: i64, number: i64, new: usize) {
        self.new += new;
        if (&episode.released, season, number) <= (&self.episode.released, self.season, self.number)
        {
            return;
        }
        self.episode = episode;
        self.season = season;
        self.number = number;
    }

    // The slot: the newest episode's still under the series' id, so a
    // select opens the series on that episode, as an episode slot does.
    fn slot(self) -> Slot {
        let mut slot = episode_slot(
            &self.library,
            self.episode,
            self.season,
            self.number,
            &self.series,
        );
        slot.id = self.series.id;
        slot.new = self.new;
        slot
    }
}

// Whether an episode stands alone under this fold: never under
// `Titles`, always under `Episodes`, and under `Airing` when its release
// date and its arrival fall inside the window. An episode with no full
// date folds, because the gap cannot be measured.
fn stands_alone(fold: Fold, released: &str, added: i64) -> bool {
    match fold {
        Fold::Titles | Fold::Shows { .. } => false,
        Fold::Episodes => true,
        Fold::Airing => match date_seconds(released) {
            Some(aired) => (added - aired).abs() <= WINDOW_DAYS * DAY,
            None => false,
        },
    }
}

fn episode_slot(library: &str, episode: Title, season: i64, number: i64, series: &Title) -> Slot {
    let mut slot = Slot::of(library, "episodes", episode);
    slot.episode = Some(InSeries {
        series: series.id.clone(),
        name: series.title.clone(),
        season,
        episode: number,
    });
    slot
}

/// Whether a release date falls inside the window of today, in seconds.
/// The window is measured both ways, because a date a day ahead by zone
/// is still current. A title with no full date is never current.
pub fn current(released: &str, today: i64) -> bool {
    date_seconds(released).is_some_and(|aired| (today - aired).abs() <= CURRENT_DAYS * DAY)
}

/// A `released` column as Unix seconds at midnight UTC, or nothing
/// where it holds less than a full date. The civil-to-days arithmetic is
/// Howard Hinnant's, so no date crate is pulled in for one
/// subtraction.
pub fn date_seconds(released: &str) -> Option<i64> {
    let mut parts = released.splitn(3, '-').map(|part| part.parse::<i64>().ok());
    let (year, month, day) = (parts.next()??, parts.next()??, parts.next()??);
    if !(1..=12).contains(&month) || !(1..=31).contains(&day) {
        return None;
    }
    let year = if month <= 2 { year - 1 } else { year };
    let era = year.div_euclid(400);
    let year_of_era = year - era * 400;
    let month_index = (month + 9) % 12;
    let day_of_year = (153 * month_index + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    let days = era * 146_097 + day_of_era - 719_468;
    Some(days * DAY)
}

#[cfg(test)]
mod tests;
