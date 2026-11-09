// One credit of a person's page: the work's poster and card, the year it
// came out, and what the people at the screen have seen of it. The row of
// posters draws the credits in release order, and the career axis draws
// one mark for each dated credit.

use super::life::{Life, day};
use crate::catalog::{Query, Slot, Source};
use crate::screens::Item;

// The kind word a series' slot carries.
const SERIES: &str = "series";

// The years before now in which a credit or an episode still counts as
// current. A career with a credit this recent reaches to now, and a
// series with an episode this recent is still running.
pub const RECENT: i64 = 2;

/// Whether a year is recent enough, at the year `now`, to count as now.
pub fn current(year: i64, now: i64) -> bool {
    year >= now - RECENT
}

/// What the people at the screen have seen of one credit.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum Seen {
    /// Nobody at the screen has seen it, and every credit while nobody
    /// is at the screen.
    #[default]
    Nobody,
    /// Some of the people at the screen have seen it, and not all.
    Some,
    /// Everyone at the screen has seen it.
    Everyone,
}

impl Seen {
    /// The state of a credit that `seen` of `people` people have seen.
    pub fn of(seen: usize, people: usize) -> Self {
        match seen {
            0 => Self::Nobody,
            seen if seen >= people => Self::Everyone,
            _ => Self::Some,
        }
    }
}

/// One credit on a person's page.
#[derive(Debug, Clone, PartialEq)]
pub struct Credit {
    /// The work's poster and the two lines of its card.
    pub item: Item,
    /// The year the work came out, and nothing for a work with no
    /// release date.
    pub year: Option<i64>,
    /// What the people at the screen have seen of the work.
    pub seen: Seen,
    /// How many episodes of a series the library holds, which a person
    /// must all finish to have seen the series. Zero for a film.
    pub episodes: usize,
    /// The first year of a series that is still running, whose run the
    /// axis draws to now. Nothing for a film and for a series that ended.
    pub run: Option<i64>,
}

impl Credit {
    /// One slot of the person's works as a credit. The card's first line
    /// is the part the person took, because the poster shows the title,
    /// and the second is the year with the age at the release. A series
    /// reads its episodes, which say whether it still runs and how many
    /// episodes a person has to finish.
    pub fn of(query: &Query, slot: Slot, life: &Life, now: i64, source: &mut dyn Source) -> Self {
        let year = day(&slot.released).map(|day| day.year);
        let (episodes, run) = match slot.kind == SERIES {
            true => running(source, &slot, year, now),
            false => (0, None),
        };
        let caption = part(&slot.parts);
        let under = life.line(&slot.released);
        let mut item = Item::of(query, slot);
        item.caption = caption;
        item.under = under;
        item.tagline = false;
        Self {
            item,
            year,
            seen: Seen::Nobody,
            episodes,
            run,
        }
    }

    /// Whether this credit is the work that a library and an id name.
    pub fn is(&self, work: &(String, String)) -> bool {
        self.item.library == work.0 && self.item.id == work.1
    }

    /// The library and the id of the work, which a later read finds the
    /// credit again by.
    pub fn work(&self) -> (String, String) {
        (self.item.library.clone(), self.item.id.clone())
    }
}

// The episodes a series holds, and its first year where its newest
// episode aired recently enough that the series still runs. The catalog
// holds no status for a series, so the newest episode is what says it.
fn running(
    source: &mut dyn Source,
    slot: &Slot,
    year: Option<i64>,
    now: i64,
) -> (usize, Option<i64>) {
    let episodes = source.episodes(&slot.library, &slot.id);
    let newest = episodes
        .iter()
        .filter_map(|episode| day(&episode.released))
        .map(|day| day.year)
        .max();
    let run = newest
        .filter(|newest| current(*newest, now))
        .and(year.or(newest));
    (episodes.len(), run)
}

/// The first line of a credit's card: each part the person took, with
/// the character in place of an `as <character>` run. A person who acted
/// and directed reads "Director, Sarah".
pub fn part(parts: &str) -> String {
    parts
        .split(", ")
        .filter(|part| !part.is_empty())
        .map(|part| part.strip_prefix("as ").unwrap_or(part))
        .collect::<Vec<&str>>()
        .join(", ")
}

/// The person's works in release order, oldest first, and the works with
/// no release date after every dated one. Works of one date keep their
/// title order.
pub fn ordered(mut slots: Vec<Slot>) -> Vec<Slot> {
    slots.sort_by(|one, other| {
        let (one_day, other_day) = (day(&one.released), day(&other.released));
        one_day
            .is_none()
            .cmp(&other_day.is_none())
            .then_with(|| one.released.cmp(&other.released))
            .then_with(|| one.title.cmp(&other.title))
    });
    slots
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_first_line_names_the_job_or_the_character() {
        let cases = [
            ("as Sarah", "Sarah"),
            ("as #7 (voice)", "#7 (voice)"),
            ("Director", "Director"),
            ("Director, Writer", "Director, Writer"),
            ("Director, as Sarah", "Director, Sarah"),
            ("Actor", "Actor"),
            ("", ""),
        ];
        for (parts, line) in cases {
            assert_eq!(part(parts), line, "{parts}");
        }
    }

    fn released(title: &str, released: &str) -> Slot {
        Slot {
            title: title.into(),
            released: released.into(),
            ..Slot::default()
        }
    }

    #[test]
    fn the_works_run_oldest_first_with_the_undated_last() {
        let works = ordered(vec![
            released("Newest", "2021-01-01"),
            released("Undated", ""),
            released("Oldest", "1985"),
            released("Same year, B", "1990-05-02"),
            released("Same year, A", "1990-05-02"),
        ]);
        let titles: Vec<&str> = works.iter().map(|work| work.title.as_str()).collect();
        assert_eq!(
            titles,
            [
                "Oldest",
                "Same year, A",
                "Same year, B",
                "Newest",
                "Undated"
            ]
        );
    }

    #[test]
    fn a_credit_is_seen_by_nobody_by_some_or_by_everyone() {
        let cases = [
            (0, 0, Seen::Nobody),
            (0, 2, Seen::Nobody),
            (1, 2, Seen::Some),
            (2, 2, Seen::Everyone),
            (1, 1, Seen::Everyone),
        ];
        for (seen, people, state) in cases {
            assert_eq!(Seen::of(seen, people), state, "{seen} of {people}");
        }
    }

    #[test]
    fn a_year_two_years_back_still_counts_as_now() {
        assert!(current(2026, 2026));
        assert!(current(2024, 2026));
        assert!(!current(2023, 2026));
    }
}
