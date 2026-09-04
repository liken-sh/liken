// The day's draw. The local civil date is the seed, so the page is the
// same all day and different tomorrow, and the draw is a pure function
// of the date and the pool, so a wrong page reproduces from those two
// alone. There is no random-number crate: splitmix64 is a few lines, and
// the seed is the date.

use std::collections::HashSet;

use super::pool::{Candidate, Kind};
use super::recency::DRAWN;

/// A civil date in the process's own zone: the year, the month from one,
/// and the day of the month.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Date {
    pub year: i64,
    pub month: u8,
    pub day: u8,
}

impl Date {
    /// Today's date in the process's zone. The standard library has no
    /// zones, and glibc's `localtime_r` reads `TZ` and `/etc/localtime`, which
    /// the operator sets on the pod, so this is one libc call and not a date
    /// crate.
    pub fn today() -> Self {
        let now = unsafe { libc::time(std::ptr::null_mut()) };
        let mut local: libc::tm = unsafe { std::mem::zeroed() };
        unsafe { libc::localtime_r(&now, &mut local) };
        Self {
            year: i64::from(local.tm_year) + 1900,
            month: (local.tm_mon + 1) as u8,
            day: local.tm_mday as u8,
        }
    }

    // The seed: the date as one number, yyyymmdd, so two dates never share
    // one.
    fn seed(self) -> u64 {
        (self.year.unsigned_abs() * 10_000) + u64::from(self.month) * 100 + u64::from(self.day)
    }
}

// splitmix64, seeded once from the date. The same date walks the same
// sequence.
struct Generator(u64);

impl Generator {
    fn next(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        z ^ (z >> 31)
    }
}

/// The draw: `DRAWN` candidates, weighted, without replacement, and no
/// two of one kind until every kind that has candidates has been drawn
/// once, so a page shows a mix and not four genres. A candidate with no
/// weight is never drawn.
pub fn draw(date: Date, pool: &[Candidate]) -> Vec<Candidate> {
    let mut generator = Generator(date.seed());
    let mut remaining: Vec<&Candidate> = pool.iter().filter(|c| c.weight > 0).collect();
    let mut kinds: HashSet<Kind> = HashSet::new();
    let mut drawn = Vec::new();
    while drawn.len() < DRAWN && !remaining.is_empty() {
        let fresh: Vec<usize> = (0..remaining.len())
            .filter(|index| !kinds.contains(&remaining[*index].kind()))
            .collect();
        let eligible: Vec<usize> = match fresh.is_empty() {
            true => (0..remaining.len()).collect(),
            false => fresh,
        };
        let total: u64 = eligible.iter().map(|index| remaining[*index].weight).sum();
        let mut point = generator.next() % total;
        let mut chosen = eligible[0];
        for index in eligible {
            let weight = remaining[index].weight;
            if point < weight {
                chosen = index;
                break;
            }
            point -= weight;
        }
        let candidate = remaining.remove(chosen);
        kinds.insert(candidate.kind());
        drawn.push(candidate.clone());
    }
    drawn
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Order, Query};

    fn genre(name: &str, weight: u64) -> Candidate {
        Candidate {
            query: Query::Genre {
                name: name.into(),
                order: Order::Released,
            },
            name: name.into(),
            weight,
        }
    }

    fn person(name: &str, weight: u64) -> Candidate {
        Candidate {
            query: Query::Person {
                library: "screening/features".into(),
                path: format!(".contributors/{name}"),
            },
            name: name.into(),
            weight,
        }
    }

    fn set(name: &str, weight: u64) -> Candidate {
        Candidate {
            query: Query::Set {
                library: "screening/features".into(),
                id: format!("set:{name}"),
            },
            name: name.into(),
            weight,
        }
    }

    fn date(year: i64, month: u8, day: u8) -> Date {
        Date { year, month, day }
    }

    fn names(drawn: &[Candidate]) -> Vec<&str> {
        drawn
            .iter()
            .map(|candidate| candidate.name.as_str())
            .collect()
    }

    fn kinds(drawn: &[Candidate]) -> Vec<Kind> {
        drawn.iter().map(Candidate::kind).collect()
    }

    // A pool with two of every kind, so every draw shows one of each and
    // then a repeat.
    fn mixed() -> Vec<Candidate> {
        vec![
            genre("Western", 200),
            genre("Drama", 40),
            person("A Player", 15),
            person("A Director", 4),
            set("The Cycle", 3),
            set("The Sequels", 2),
        ]
    }

    #[test]
    fn the_first_three_draws_are_one_of_each_kind_and_the_fourth_repeats_one() {
        for day in 1..=28 {
            let drawn = draw(date(2026, 9, day), &mixed());
            assert_eq!(drawn.len(), DRAWN);
            let first: HashSet<Kind> = kinds(&drawn[..3]).into_iter().collect();
            assert_eq!(first.len(), 3, "day {day}: {:?}", names(&drawn));
        }
    }

    #[test]
    fn the_draw_is_fixed_by_the_date_and_the_pool() {
        let cases = [
            (
                date(2026, 9, 3),
                mixed(),
                vec!["Western", "A Player", "The Cycle", "Drama"],
            ),
            (
                date(2026, 9, 4),
                mixed(),
                vec!["Western", "A Player", "The Sequels", "Drama"],
            ),
            (
                date(2026, 9, 5),
                mixed(),
                vec!["Western", "The Cycle", "A Player", "Drama"],
            ),
            (
                date(2026, 9, 3),
                vec![genre("Western", 1), genre("Drama", 1)],
                vec!["Western", "Drama"],
            ),
        ];
        for (date, pool, expected) in cases {
            assert_eq!(names(&draw(date, &pool)), expected, "{date:?}");
        }
    }

    #[test]
    fn two_dates_draw_differently_where_the_pool_allows_it() {
        let pool: Vec<Candidate> = (1..=12)
            .map(|number| genre(&format!("Genre {number}"), 10))
            .collect();
        assert_ne!(
            names(&draw(date(2026, 9, 3), &pool)),
            names(&draw(date(2026, 9, 4), &pool))
        );
        assert_ne!(
            names(&draw(date(2026, 9, 3), &pool)),
            names(&draw(date(2025, 9, 3), &pool))
        );
    }

    #[test]
    fn a_draw_never_repeats_a_candidate_and_never_takes_one_with_no_weight() {
        let pool = vec![
            genre("Western", 5),
            genre("Silent", 0),
            person("A Player", 5),
        ];
        for day in 1..=28 {
            let drawn = draw(date(2026, 9, day), &pool);
            assert_eq!(drawn.len(), 2);
            assert!(!names(&drawn).contains(&"Silent"));
            let distinct: HashSet<&str> = names(&drawn).into_iter().collect();
            assert_eq!(distinct.len(), 2);
        }
    }

    #[test]
    fn a_pool_of_one_kind_draws_up_to_the_count() {
        let pool: Vec<Candidate> = (1..=6)
            .map(|number| genre(&format!("Genre {number}"), number))
            .collect();
        assert_eq!(draw(date(2026, 9, 3), &pool).len(), DRAWN);
        assert!(draw(date(2026, 9, 3), &[]).is_empty());
    }

    #[test]
    fn a_heavier_candidate_draws_more_often() {
        let pool = vec![genre("Western", 90), genre("Drama", 10)];
        let westerns = (1..=30)
            .filter(|day| draw(date(2026, 9, *day), &pool)[0].name == "Western")
            .count();
        assert!(westerns > 20, "{westerns} of 30");
    }

    #[test]
    fn two_dates_never_share_a_seed() {
        assert_ne!(date(2026, 9, 3).seed(), date(2026, 9, 4).seed());
        assert_ne!(date(2026, 9, 3).seed(), date(2026, 10, 3).seed());
        assert_eq!(date(2026, 9, 3).seed(), 20_260_903);
    }

    #[test]
    fn today_is_a_civil_date() {
        let today = Date::today();
        assert!(today.year >= 2026);
        assert!((1..=12).contains(&today.month));
        assert!((1..=31).contains(&today.day));
    }
}
