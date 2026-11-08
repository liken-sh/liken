// The career axis: a line across the page from the start of a person's
// career to its end, with a mark for each credit at its release year. It
// shows where a career started, where it was busy, where it stopped, and
// which part of it the people at the screen have seen, which a row of
// posters cannot show. This module decides what the axis holds: its span,
// its years, whether it draws dots or bars, and the marks of the birth
// and the death. `place` says where each of those goes on the screen, and
// `draw` draws them.

pub mod draw;
pub mod place;

use std::collections::BTreeMap;

use super::credit::{Credit, Seen, current};
use super::life::Life;

/// The most credits one year stacks as dots. A year with more turns the
/// whole axis into bars, because a taller stack would reach into the head.
pub const STACK: usize = 5;

// The fewest dated credits, and the fewest years between the first and
// the last, that make an axis worth drawing. A shorter career is a few
// posters, and the row shows all of it.
const FEWEST: usize = 5;
const FEWEST_YEARS: i64 = 5;

// The most years a birth may come before the first credit for the axis to
// start at it. A later first credit would leave most of the line empty.
const BIRTH_REACH: i64 = 25;

// The fewest years the axis spans, so a short career does not stretch a
// few credits across the whole page.
const SHORTEST: i64 = 10;

// The years before the first credit where an axis that does not start at
// a birth starts. The line fades in over them, because nothing on the
// axis says when the career began.
pub const LEAD: i64 = 3;

/// How the axis draws its credits.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    /// One dot per credit, stacked by year.
    Dots,
    /// One bar per year, on one scale for the whole axis.
    Bars,
}

/// One year with credits.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Year {
    pub year: i64,
    /// The credits of the year, as indices into the page's credits, in
    /// the row's order.
    pub credits: Vec<usize>,
    /// How many of them everyone at the screen has seen.
    pub everyone: usize,
    /// How many of them some, and not all, of the people at the screen
    /// have seen.
    pub some: usize,
}

/// What the left end of the line says.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Start {
    /// The axis starts at the birth, and the end is labeled "born".
    Born,
    /// The axis starts later than the birth, or the entry holds no birth,
    /// so the line fades in up to the first credit. A person who died
    /// before the axis starts has the faded end labeled with the year of
    /// the death, with no age, because the works came out long after it.
    Faded { died: Option<String> },
}

/// The axis of one career.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Axis {
    /// The first year the axis spans.
    pub start: i64,
    /// The last year the axis spans, inclusive.
    pub end: i64,
    /// The year of the first credit, where a faded line reaches its full
    /// strength.
    pub first: i64,
    /// Every year with credits, oldest first.
    pub years: Vec<Year>,
    pub mode: Mode,
    /// The most credits of any one year, which the bars are scaled to.
    pub tallest: usize,
    pub left: Start,
    /// The year of a death inside the span and the words of its mark.
    pub death: Option<(i64, String)>,
    /// The first year of each running series, whose run the axis draws
    /// to its end.
    pub runs: Vec<i64>,
}

impl Axis {
    /// The axis of these credits, and nothing for a career too short to
    /// draw one. `now` is the current year.
    pub fn of(credits: &[Credit], life: &Life, now: i64) -> Option<Self> {
        let mut by_year: BTreeMap<i64, Vec<usize>> = BTreeMap::new();
        for (index, credit) in credits.iter().enumerate() {
            if let Some(year) = credit.year {
                by_year.entry(year).or_default().push(index);
            }
        }
        let dated: usize = by_year.values().map(Vec::len).sum();
        let (&first, &last) = (by_year.keys().next()?, by_year.keys().next_back()?);
        if dated < FEWEST || last - first < FEWEST_YEARS {
            return None;
        }
        let end = match current(last, now) {
            true => now.max(last),
            false => last,
        };
        let birth = life
            .born
            .map(|born| born.year)
            .filter(|born| *born <= first && first - born <= BIRTH_REACH);
        let (start, end) = span(first, end, birth);
        let years: Vec<Year> = by_year
            .into_iter()
            .map(|(year, indices)| Year::of(year, indices, credits))
            .collect();
        let tallest = years.iter().map(|year| year.credits.len()).max()?;
        let died = life.died.map(|died| died.year);
        Some(Self {
            start,
            end,
            first,
            mode: match tallest > STACK {
                true => Mode::Bars,
                false => Mode::Dots,
            },
            tallest,
            years,
            left: match birth {
                Some(_) => Start::Born,
                None => Start::Faded {
                    died: died
                        .filter(|died| *died < start)
                        .map(|died| format!("died {died}")),
                },
            },
            death: died
                .filter(|died| (start..=end).contains(died))
                .zip(life.death()),
            runs: credits.iter().filter_map(|credit| credit.run).collect(),
        })
    }

    /// The year one credit is in, as an index into `years`, and nothing
    /// for an undated credit.
    pub fn year_of(&self, credit: usize) -> Option<usize> {
        self.years
            .iter()
            .position(|year| year.credits.contains(&credit))
    }

    /// The years of the decade ticks, oldest first.
    pub fn ticks(&self) -> impl Iterator<Item = i64> {
        let first = self.start.div_euclid(10) * 10;
        (first..=self.end)
            .step_by(10)
            .filter(|year| *year >= self.start)
    }

    /// The words over the focused year: the year, its credits, and how
    /// many of them everyone at the screen has seen, where any.
    pub fn label(&self, index: usize) -> String {
        let Some(year) = self.years.get(index) else {
            return String::new();
        };
        let credits = match year.credits.len() {
            1 => "1 credit".to_string(),
            count => format!("{count} credits"),
        };
        match year.everyone {
            0 => format!("{} · {credits}", year.year),
            seen => format!("{} · {credits} · {seen} seen", year.year),
        }
    }
}

impl Year {
    fn of(year: i64, credits: Vec<usize>, all: &[Credit]) -> Self {
        let count = |state| {
            credits
                .iter()
                .filter(|index| all[**index].seen == state)
                .count()
        };
        Self {
            year,
            everyone: count(Seen::Everyone),
            some: count(Seen::Some),
            credits,
        }
    }
}

/// The years the axis spans, both inclusive, for credits from `first` to
/// `end`. The axis starts at the birth where one applies, and otherwise
/// `LEAD` years before the first credit, so the line has room to fade in.
/// A short span widens at its end to `SHORTEST` years.
pub fn span(first: i64, end: i64, birth: Option<i64>) -> (i64, i64) {
    let start = birth.unwrap_or(first - LEAD);
    (start, end.max(start + SHORTEST - 1))
}

#[cfg(test)]
mod tests;
