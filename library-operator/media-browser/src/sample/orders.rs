// The invented franchises of the sample catalog, so the strip and the
// franchise page draw before any git repository is scanned. The saga holds a
// calendar, nested eras, three universes, an entry in two of them, one serial
// cut into two runs, and two gaps. The cycle holds no calendar and no
// universes, which is the page with no rail and no time. Every name here is
// invented, as everything else in the sample is.

use crate::catalog::FranchiseEntry;
use crate::catalog::franchise::{Calendar, Entry, Era, Franchise, Held, MOVIE, Membership, SERIES};

use super::{FEATURES, SERIALS_LIBRARY, movie, serial};

/// The `Library` of kind franchises the sample invents.
pub const ORDERS: &str = "sample/orders";

/// The two franchises the sample holds.
pub const SAGA: &str = "franchise:name:the-specimen-saga";
pub const CYCLE: &str = "franchise:name:the-marsh-cycle";

// The three universes of the saga: its own, and the two an entry names.
const COPPICE: &str = "The Coppice";
const FEN: &str = "The Fen";
const MARSH: &str = "The Marsh";

// The release years the two gaps of the saga carry. One is far enough
// ahead of any run to draw as coming, and the other is behind every run
// and draws as missing.
const AHEAD: i64 = 2099;
const BEHIND: i64 = 1961;

/// The two franchises, in the order a wall of them draws.
pub fn franchises() -> Vec<Franchise> {
    vec![saga(), cycle()]
}

/// The two invented franchises as the home page's strip draws them. The saga
/// carries art and the cycle carries none, so the strip draws one poster and
/// one tile of words.
pub fn entries() -> Vec<FranchiseEntry> {
    franchises()
        .into_iter()
        .map(|franchise| {
            // The saga carries its own art, and the cycle carries none,
            // so the cycle draws the poster of its first held member the
            // way the catalog's own read answers one.
            let first = franchise
                .entries
                .iter()
                .find_map(|entry| entry.held.clone())
                .unwrap_or_default();
            let (art, art_library) = match franchise.art.is_empty() {
                true => (first.art, first.library),
                false => (franchise.art, franchise.library.clone()),
            };
            FranchiseEntry {
                library: franchise.library,
                id: franchise.id,
                title: franchise.title,
                art,
                art_library,
                slug: String::new(),
            }
        })
        .collect()
}

/// One invented franchise by its id, and nothing where the sample invented
/// none.
pub fn franchise(library: &str, id: &str) -> Option<Franchise> {
    if library != ORDERS {
        return None;
    }
    franchises().into_iter().find(|held| held.id == id)
}

/// Every franchise one invented title belongs to, with the members the
/// sample's own libraries hold.
pub fn memberships(id: &str) -> Vec<Membership> {
    franchises()
        .into_iter()
        .filter(|franchise| {
            franchise
                .entries
                .iter()
                .any(|entry| entry.held.as_ref().is_some_and(|held| held.id == id))
        })
        .map(|franchise| Membership {
            movies: counted(&franchise.entries, MOVIE),
            series: counted(&franchise.entries, SERIES),
            library: franchise.library,
            id: franchise.id,
            title: franchise.title,
            members: franchise
                .entries
                .into_iter()
                .filter(|entry| entry.held.is_some())
                .collect(),
        })
        .collect()
}

// How many entries of one order are of this kind, which is the scope a
// strip's heading carries.
fn counted(entries: &[Entry], kind: &str) -> i64 {
    entries.iter().filter(|entry| entry.kind == kind).count() as i64
}

// The saga: the franchise that exercises every part of the page.
fn saga() -> Franchise {
    Franchise {
        library: ORDERS.into(),
        id: SAGA.into(),
        title: "The Specimen Saga".into(),
        art: "art/the-specimen-saga.jpg".into(),
        universe: COPPICE.into(),
        calendar: Some(Calendar {
            unit: "years".into(),
            zero: "the Survey".into(),
            before: "BS".into(),
            after: "AS".into(),
        }),
        // The wider era holds the narrower one, so the rail draws two
        // lanes and the phase nests inside the saga.
        eras: vec![
            Era {
                name: "The Long Survey".into(),
                from: -40.0,
                to: 40.0,
            },
            Era {
                name: "The Coppice Years".into(),
                from: -5.0,
                to: 5.0,
            },
        ],
        entries: vec![
            film(1, 1, (-32.0, -32.0), &[]),
            // These two spans overlap in different universes, so the
            // page packs them onto one row.
            film(2, 2, (-30.0, -28.0), &[FEN]),
            film(3, 3, (-29.0, -27.0), &[MARSH]),
            // One serial the story cuts into two runs: the same member
            // at two positions, each with its own span and its own
            // count of episodes.
            run(4, 1, (-22.0, -20.0), 9),
            run(5, 1, (-19.0, -18.0), 10),
            gap(6, MOVIE, "Specimen 9001", AHEAD, Some((0.0, 0.0))),
            gap(7, MOVIE, "Specimen 9002", BEHIND, Some((2.0, 2.0))),
            // One entry in three universes, which draws as a banner
            // across their columns.
            film(8, 4, (10.0, 12.0), &[COPPICE, FEN, MARSH]),
            // An entry with no time joins no era on the rail.
            gap(9, SERIES, "Serial 99", 0, None),
        ],
    }
}

// The cycle: a franchise with no clock and one universe, which is the
// page with no time and no rail.
fn cycle() -> Franchise {
    Franchise {
        library: ORDERS.into(),
        id: CYCLE.into(),
        title: "The Marsh Cycle".into(),
        art: String::new(),
        universe: String::new(),
        calendar: None,
        eras: Vec::new(),
        entries: (5..=7)
            .map(|number| Entry {
                timed: false,
                ..film(number - 4, number, (0.0, 0.0), &[])
            })
            .collect(),
    }
}

// One entry the features library holds, by the movie's own number.
fn film(position: i64, number: i64, span: (f64, f64), universes: &[&str]) -> Entry {
    let title = movie(number);
    Entry {
        position,
        kind: MOVIE.into(),
        alias: format!("movie:sample:{number}"),
        title: title.title.clone(),
        released: title.released.clone(),
        release_year: title.released.parse().unwrap_or_default(),
        timed: true,
        from: span.0,
        to: span.1,
        universes: universes.iter().map(|name| name.to_string()).collect(),
        held: Some(Held {
            library: FEATURES.into(),
            id: title.id,
            kind: "movies".into(),
            title: title.title,
            arts: vec![
                title.art.clone(),
                format!("backdrops/specimen-{number:04}.jpg"),
            ],
            art: title.art,
            released: title.released,
            slug: format!("specimen-{number:04}"),
            tagline: format!("The {number:04}th of its kind."),
            plot: super::PLOT.repeat(2),
            duration: title.duration,
        }),
        episodes: 0,
    }
}

// One run of a serial the serials library holds, with the episodes the
// run counts.
fn run(position: i64, number: i64, span: (f64, f64), episodes: i64) -> Entry {
    let title = serial(number);
    Entry {
        position,
        kind: SERIES.into(),
        alias: format!("series:sample:{number}"),
        title: title.title.clone(),
        released: title.released.clone(),
        release_year: title.released.parse().unwrap_or_default(),
        timed: true,
        from: span.0,
        to: span.1,
        universes: Vec::new(),
        held: Some(Held {
            library: SERIALS_LIBRARY.into(),
            id: title.id,
            kind: "series".into(),
            title: title.title,
            arts: vec![
                title.art.clone(),
                format!("backdrops/serial-{number:02}.jpg"),
            ],
            art: title.art,
            released: title.released,
            slug: format!("serial-{number:02}"),
            tagline: format!("Serial {number:02}, in its own seasons."),
            plot: super::PLOT.repeat(2),
            duration: 0,
        }),
        episodes,
    }
}

// One entry no sample library holds, which draws as a gap in the order.
// A gap with a release year of 0 is one the file gives no year.
fn gap(
    position: i64,
    kind: &str,
    title: &str,
    release_year: i64,
    span: Option<(f64, f64)>,
) -> Entry {
    let (timed, from, to) = match span {
        Some((from, to)) => (true, from, to),
        None => (false, 0.0, 0.0),
    };
    Entry {
        position,
        kind: kind.into(),
        alias: format!("{kind}:sample:{position:02}"),
        title: title.into(),
        released: match release_year > 0 {
            true => release_year.to_string(),
            false => String::new(),
        },
        release_year,
        timed,
        from,
        to,
        universes: Vec::new(),
        held: None,
        episodes: 0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::franchise::Standing;

    #[test]
    fn the_saga_holds_a_calendar_nested_eras_and_three_universes() {
        let saga = franchise(ORDERS, SAGA).expect("the sample invents the saga");
        assert!(saga.calendar.is_some());
        assert_eq!(saga.universe, COPPICE);
        assert_eq!(saga.eras.len(), 2);
        assert!(saga.eras[0].holds(&saga.eras[1]));
        let named: Vec<&str> = saga
            .entries
            .iter()
            .flat_map(|entry| entry.universes.iter().map(String::as_str))
            .collect();
        assert!(named.contains(&FEN));
        assert!(named.contains(&MARSH));
    }

    #[test]
    fn the_saga_cuts_one_serial_into_two_runs() {
        let saga = franchise(ORDERS, SAGA).expect("the sample invents the saga");
        let runs: Vec<&Entry> = saga
            .entries
            .iter()
            .filter(|entry| entry.alias == "series:sample:1")
            .collect();
        assert_eq!(runs.len(), 2);
        assert_eq!((runs[0].position, runs[1].position), (4, 5));
        assert_eq!((runs[0].episodes, runs[1].episodes), (9, 10));
    }

    #[test]
    fn the_saga_holds_a_gap_ahead_of_today_and_one_behind() {
        let saga = franchise(ORDERS, SAGA).expect("the sample invents the saga");
        let gaps: Vec<Standing> = saga
            .entries
            .iter()
            .filter(|entry| entry.held.is_none())
            .map(|entry| entry.standing("2026-06-15"))
            .collect();
        assert_eq!(
            gaps,
            [Standing::Coming, Standing::Missing, Standing::Missing]
        );
    }

    #[test]
    fn one_entry_of_the_saga_names_three_universes_and_one_names_none() {
        let saga = franchise(ORDERS, SAGA).expect("the sample invents the saga");
        assert_eq!(saga.entries[7].universes, [COPPICE, FEN, MARSH]);
        assert!(saga.entries[0].universes.is_empty());
    }

    #[test]
    fn the_cycle_holds_no_calendar_no_universes_and_no_time() {
        let cycle = franchise(ORDERS, CYCLE).expect("the sample invents the cycle");
        assert_eq!(cycle.calendar, None);
        assert!(cycle.eras.is_empty());
        assert!(cycle.universe.is_empty());
        assert!(cycle.entries.iter().all(|entry| !entry.timed));
        assert!(
            cycle
                .entries
                .iter()
                .all(|entry| entry.universes.is_empty() && entry.held.is_some())
        );
    }

    #[test]
    fn the_home_page_reads_both_invented_franchises() {
        let entries = entries();
        let named: Vec<(&str, &str)> = entries
            .iter()
            .map(|entry| (entry.id.as_str(), entry.title.as_str()))
            .collect();
        assert_eq!(
            named,
            [(SAGA, "The Specimen Saga"), (CYCLE, "The Marsh Cycle")]
        );
        assert!(entries.iter().all(|entry| entry.library == ORDERS));
        // The saga carries its own art, and the cycle draws the poster
        // of the first film its libraries hold.
        assert_eq!(entries[0].art, "art/the-specimen-saga.jpg");
        assert_eq!(entries[0].art_library, ORDERS);
        assert_eq!(entries[1].art, "posters/specimen-0005.jpg");
        assert_eq!(entries[1].art_library, "sample/features");
    }

    #[test]
    fn a_film_belongs_to_the_franchise_that_names_it() {
        let held = memberships("movie:sample:0001");
        assert_eq!(held.len(), 1);
        assert_eq!(held[0].id, SAGA);
        assert_eq!(held[0].title, "The Specimen Saga");
        assert_eq!(memberships("movie:sample:0005")[0].id, CYCLE);
        assert!(memberships("movie:sample:0099").is_empty());
    }

    #[test]
    fn a_strip_holds_only_the_members_the_libraries_hold() {
        let strip = memberships("movie:sample:0001").remove(0);
        assert!(strip.members.iter().all(|entry| entry.held.is_some()));
        assert_eq!(strip.members.len(), 6);
    }

    #[test]
    fn a_franchise_the_sample_never_invented_has_no_page() {
        assert_eq!(franchise(ORDERS, "franchise:name:none"), None);
        assert_eq!(franchise("sample/features", SAGA), None);
    }
}
