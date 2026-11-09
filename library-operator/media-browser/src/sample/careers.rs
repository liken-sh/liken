// The invented careers of the sample catalog: one person for each state
// of a person's page, so a run with no catalog draws every one. A long
// career with a running series draws the axis in dots, a busy one draws
// it in bars, a playwright filmed long after the death labels the faded
// end, a short life draws the death inside the span, a person with no
// birth date reads no ages, and a short career draws no axis. Every name
// here is synthesized.

use std::collections::HashMap;

use super::{FEATURES, MOVIES, SERIALS, SERIALS_LIBRARY, movie, serial_slot};
use crate::catalog::{Reach, Slot, Work};

const CONTRIBUTORS: &str = ".contributors/";

/// The people with an invented career, by the name a search finds them
/// by.
pub const LONG: &str = "A Long Career";
pub const BUSY: &str = "A Busy Voice";
pub const PLAYWRIGHT: &str = "A Late Playwright";
pub const SHORT_LIFE: &str = "A Short Life";
pub const UNDATED: &str = "An Undated Player";
pub const FEW: &str = "A Brief Career";

/// Every invented career, by name.
pub const ALL: [&str; 6] = [LONG, BUSY, PLAYWRIGHT, SHORT_LIFE, UNDATED, FEW];

// One credit of a career: the year a film came out, or nothing for a film
// whose release date the career's catalog does not hold, and the part.
type Credit = (Option<i64>, &'static str);

/// The dates of birth and death of a career.
pub fn lived(name: &str) -> (&'static str, &'static str) {
    match name {
        LONG => ("1970-06-01", ""),
        BUSY => ("1968-02-11", ""),
        PLAYWRIGHT => ("1564", "1616"),
        SHORT_LIFE => ("1908-03-02", "1964-09-30"),
        FEW => ("1988-04-04", ""),
        _ => ("", ""),
    }
}

// The busy voice's credits in each year from 1990, more than five in the
// busiest years, so the axis draws bars.
const BUSY_YEARS: [i64; 36] = [
    1, 2, 1, 3, 2, 4, 3, 5, 4, 6, 5, 7, 6, 8, 7, 9, 8, 10, 9, 11, 10, 9, 8, 9, 7, 8, 6, 7, 5, 6, 4,
    5, 6, 4, 3, 2,
];

const PARTS: [&str; 12] = [
    "as Clara",
    "as Sarah",
    "as Gloria",
    "as Jenny",
    "as Josie",
    "as Taryn",
    "as Ellie",
    "as Emma",
    "as Ruth",
    "as Marion",
    "as #7 (voice)",
    "as Helen",
];

const VOICES: [&str; 6] = [
    "as Sergeant Brask (voice)",
    "as Grumble (voice)",
    "Narrator",
    "as Ox (voice)",
    "as Elder Wing (voice)",
    "as Cosmo (voice)",
];

// The credits of one career, in no order.
fn credits(name: &str) -> Vec<Credit> {
    let years = |years: &[i64], parts: &[&'static str]| -> Vec<Credit> {
        years
            .iter()
            .enumerate()
            .map(|(index, year)| (Some(*year), parts[index % parts.len()]))
            .collect()
    };
    match name {
        LONG => years(
            &[
                1985, 1986, 1988, 1989, 1990, 1991, 1991, 1995, 1996, 1996, 1997, 1998, 2000, 2000,
                2000, 2001, 2003, 2003, 2005, 2006, 2006, 2008, 2008, 2009, 2009, 2014, 2014, 2016,
                2019, 2022, 2025,
            ],
            &PARTS,
        ),
        BUSY => BUSY_YEARS
            .iter()
            .enumerate()
            .flat_map(|(index, count)| {
                (0..*count).map(move |credit| {
                    (
                        Some(1990 + index as i64),
                        VOICES[credit as usize % VOICES.len()],
                    )
                })
            })
            .collect(),
        PLAYWRIGHT => years(
            &[1991, 1993, 1996, 1999, 2001, 2007, 2011, 2015, 2021],
            &["Writer"],
        ),
        SHORT_LIFE => years(
            &[
                1959, 1960, 1960, 1961, 1962, 1962, 1963, 1964, 1964, 1965, 1966, 1968, 1971, 1975,
                1982, 1990, 1995, 2003, 2010, 2016, 2020, 2024,
            ],
            &["Director", "Writer", "Director, Writer"],
        ),
        UNDATED => {
            let mut credits = years(
                &[
                    2004, 2005, 2007, 2008, 2010, 2011, 2012, 2014, 2015, 2016, 2017, 2018, 2019,
                    2020, 2022, 2023, 2024,
                ],
                &PARTS,
            );
            credits.extend([(None, "as Walk-on"), (None, "as Second Guard")]);
            credits
        }
        FEW => years(&[2015, 2018, 2022], &PARTS),
        _ => Vec::new(),
    }
}

// The number of the `nth` invented movie released in this year. The
// sample's movies take their years from their numbers, `1900 + 37n mod
// 126`, and 109 is the inverse of 37 modulo 126, so the numbers of one
// year are one residue apart by 126.
fn released_in(year: i64, nth: usize) -> i64 {
    let first = ((year - 1900).rem_euclid(126) * 109).rem_euclid(126);
    let number = first + 126 * (nth as i64 + i64::from(first == 0));
    number.min(MOVIES)
}

/// The career of one person entry, or nothing for an entry with none.
pub fn career(path: &str) -> Option<&'static str> {
    let name = path.strip_prefix(CONTRIBUTORS)?;
    ALL.into_iter().find(|career| *career == name)
}

/// The works of one career as slots of the features library, and the
/// long career's running serial in the serials library, newest first as
/// the catalog answers a person's works. A credit the career holds no
/// date for is a film with its release date cleared.
pub fn works(name: &str) -> Vec<Slot> {
    let mut nth: HashMap<i64, usize> = HashMap::new();
    let mut works: Vec<Slot> = credits(name)
        .into_iter()
        .enumerate()
        .map(|(index, (year, part))| {
            let number = match year {
                Some(year) => {
                    let count = nth.entry(year).or_default();
                    *count += 1;
                    released_in(year, *count - 1)
                }
                None => MOVIES - index as i64,
            };
            let mut slot = Slot::of(FEATURES, "movies", movie(number));
            if year.is_none() {
                slot.released = String::new();
            }
            slot.parts = part.to_string();
            slot
        })
        .collect();
    if name == LONG {
        works.push(Slot {
            parts: "as The Host".into(),
            ..serial_slot(SERIALS_LIBRARY, SERIALS)
        });
    }
    works.sort_by(|one, other| other.released.cmp(&one.released));
    works
}

/// How far each person of an audience got in the films of the careers.
/// The sample holds no plays, so it states who finished what: the `i`th
/// person of the audience finished every film whose key leaves a
/// remainder under `2 - i` by 3, so with two people some films are seen
/// by both, some by the first alone, and the rest by nobody. The key adds
/// the film's number to its count of 126s, because the films of one year
/// are 126 apart and would otherwise all leave one remainder.
pub fn reach(people: &[String]) -> HashMap<Work, Reach> {
    let mut reach: HashMap<Work, Reach> = HashMap::new();
    for name in ALL {
        for slot in works(name).into_iter().filter(|slot| slot.kind == "movies") {
            let number: i64 = slot
                .id
                .rsplit(':')
                .next()
                .and_then(|n| n.parse().ok())
                .unwrap_or(0);
            let finished: Vec<(String, f32)> = people
                .iter()
                .enumerate()
                .filter(|(index, _)| (number + number / 126) % 3 < 2 - (*index as i64).min(2))
                .map(|(_, person)| (person.clone(), 1.0))
                .collect();
            if !finished.is_empty() {
                reach.insert(
                    (slot.library, slot.id),
                    Reach {
                        people: finished,
                        ..Reach::default()
                    },
                );
            }
        }
    }
    reach
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::screens::facts;

    #[test]
    fn a_movie_released_in_a_year_carries_that_year() {
        for (year, nth) in [(1985, 0), (2009, 10), (1959, 3), (2025, 0), (1900, 0)] {
            let number = released_in(year, nth);
            assert!((1..=MOVIES).contains(&number), "{year} {nth}");
            assert_eq!(movie(number).released, year.to_string(), "{year} {nth}");
        }
    }

    #[test]
    fn every_career_holds_its_credits_and_a_person_entry() {
        let counts: Vec<usize> = ALL.iter().map(|name| works(name).len()).collect();
        assert_eq!(counts, [32, 210, 9, 22, 19, 3]);
        assert_eq!(career(".contributors/A Long Career"), Some(LONG));
        assert_eq!(career(".contributors/Somebody"), None);
        assert_eq!(career("A Long Career"), None);
    }

    #[test]
    fn the_busy_voice_holds_eleven_credits_in_its_busiest_year() {
        let in_2009 = works(BUSY)
            .into_iter()
            .filter(|slot| facts::year(&slot.released) == "2009")
            .count();
        assert_eq!(in_2009, 11);
    }

    #[test]
    fn an_undated_credit_holds_no_release_date() {
        let undated = works(UNDATED)
            .into_iter()
            .filter(|slot| slot.released.is_empty())
            .count();
        assert_eq!(undated, 2);
    }

    #[test]
    fn two_people_have_seen_some_films_together_and_some_alone() {
        let people = ["first".to_string(), "second".to_string()];
        let reach = reach(&people);
        let both = reach
            .values()
            .filter(|reach| reach.people.len() == 2)
            .count();
        let alone = reach
            .values()
            .filter(|reach| reach.people.len() == 1)
            .count();
        assert!(both > 0 && alone > 0, "{both} {alone}");
        assert!(super::reach(&[]).is_empty());
    }
}
