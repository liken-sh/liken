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

/// The read behind a recency strip: pages of candidates, newest first,
/// folded together until `FILL` slots came out, a page came up short,
/// or `PAGES` pages were read. The fold runs over every candidate read
/// so far, because a series folds to one slot however its episodes
/// spread across pages.
pub fn filled(fold: Fold, mut page: impl FnMut(usize) -> Vec<Candidate>) -> Vec<Slot> {
    let mut candidates: Vec<Candidate> = Vec::new();
    for number in 0..PAGES {
        let read = page(number);
        let short = read.len() < CANDIDATES;
        candidates.extend(read);
        if short || self::fold(candidates.clone(), fold).len() >= FILL {
            break;
        }
    }
    self::fold(candidates, fold)
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
mod tests {
    use super::*;

    const LIBRARY: &str = "screening/serials";

    fn movie(id: &str, released: &str) -> Candidate {
        Candidate::Movie {
            slot: Slot::of(
                "screening/films",
                "movies",
                Title {
                    id: id.into(),
                    title: id.into(),
                    released: released.into(),
                    art: format!("{id}.jpg"),
                    ..Title::default()
                },
            ),
        }
    }

    fn serial() -> Title {
        Title {
            id: "series:1".into(),
            title: "The Serial".into(),
            released: "2004".into(),
            art: "serial.jpg".into(),
            rating: "TV-14".into(),
            ..Title::default()
        }
    }

    // One episode of the serial, released on the date and arrived this
    // many days after it.
    fn episode(number: i64, released: &str, days_after: i64) -> Candidate {
        Candidate::Episode {
            library: LIBRARY.into(),
            episode: Title {
                id: format!("episode:{number}"),
                title: format!("Segment {number}"),
                released: released.into(),
                art: format!("still-{number}.jpg"),
                duration: 2_760,
                ..Title::default()
            },
            added: date_seconds(released).unwrap_or(0) + days_after * DAY,
            season: 3,
            number,
            series: serial(),
        }
    }

    fn ids(slots: &[Slot]) -> Vec<&str> {
        slots.iter().map(|slot| slot.id.as_str()).collect()
    }

    #[test]
    fn a_date_inside_the_window_of_today_is_current_and_a_year_alone_never_is() {
        let today = date_seconds("2026-09-03").expect("a full date");
        for (released, expected) in [
            ("2026-09-03", true),
            ("2026-08-04", true),
            ("2026-08-03", false),
            ("2026-09-10", true),
            ("2026-10-04", false),
            ("2026", false),
            ("", false),
        ] {
            assert_eq!(current(released, today), expected, "{released}");
        }
    }

    #[test]
    fn a_full_date_reads_as_seconds_and_a_year_alone_reads_as_nothing() {
        assert_eq!(date_seconds("1970-01-01"), Some(0));
        assert_eq!(date_seconds("2000-03-01"), Some(951_868_800));
        assert_eq!(date_seconds("2024-02-29"), Some(1_709_164_800));
        assert_eq!(date_seconds("2004"), None);
        assert_eq!(date_seconds(""), None);
        assert_eq!(date_seconds("2004-13-01"), None);
        assert_eq!(date_seconds("soon-01-01"), None);
    }

    #[test]
    fn the_titles_fold_makes_one_series_slot_of_a_season_drop() {
        let slots = fold(
            vec![
                episode(3, "2026-09-03", 1),
                episode(2, "2026-08-27", 1),
                movie("movie:1", "2026-08-20"),
                episode(1, "2026-08-20", 1),
            ],
            Fold::Titles,
        );
        assert_eq!(ids(&slots), ["series:1", "movie:1"]);
        assert_eq!(slots[0].kind, "series");
        assert_eq!(slots[0].library, LIBRARY);
        assert_eq!(slots[0].title, "The Serial");
        assert_eq!(slots[0].released, "2026-09-03");
        assert_eq!(slots[0].art, "serial.jpg");
        assert_eq!(slots[0].rating, "TV-14");
        assert!(!slots[0].still());
    }

    #[test]
    fn the_episodes_fold_keeps_every_episode_as_a_still() {
        let slots = fold(
            vec![episode(2, "2026-08-27", 400), episode(1, "2026-08-20", 400)],
            Fold::Episodes,
        );
        assert_eq!(ids(&slots), ["episode:2", "episode:1"]);
        assert!(slots.iter().all(Slot::still));
        assert_eq!(slots[0].kind, "episodes");
        assert_eq!(slots[0].title, "Segment 2");
        assert_eq!(slots[0].art, "still-2.jpg");
        assert_eq!(slots[0].duration, 2_760);
        assert_eq!(
            slots[0].episode,
            Some(InSeries {
                series: "series:1".into(),
                name: "The Serial".into(),
                season: 3,
                episode: 2,
            })
        );
    }

    #[test]
    fn the_airing_fold_keeps_an_episode_inside_the_window_and_folds_the_rest() {
        let slots = fold(
            vec![
                episode(4, "2026-09-03", WINDOW_DAYS),
                episode(3, "2026-08-27", -2),
                episode(2, "2026-08-20", WINDOW_DAYS + 1),
                episode(1, "2026-08-13", 400),
            ],
            Fold::Airing,
        );
        assert_eq!(ids(&slots), ["episode:4", "episode:3", "series:1"]);
        assert_eq!(slots[2].released, "2026-08-20");
    }

    fn today() -> i64 {
        date_seconds("2026-09-03").expect("a full date")
    }

    fn shown(candidates: Vec<Candidate>) -> Vec<Slot> {
        fold(candidates, Fold::Shows { today: today() })
    }

    #[test]
    fn the_shows_fold_makes_one_still_of_the_newest_episode_of_a_series() {
        let slots = shown(vec![
            episode(8, "2026-09-03", 1),
            episode(7, "2026-08-27", 1),
            episode(1, "2026-03-01", 400),
        ]);
        assert_eq!(ids(&slots), ["series:1"]);
        assert_eq!(slots[0].kind, "episodes");
        assert_eq!(slots[0].library, LIBRARY);
        assert_eq!(slots[0].title, "Segment 8");
        assert_eq!(slots[0].art, "still-8.jpg");
        assert_eq!(slots[0].released, "2026-09-03");
        assert_eq!(slots[0].duration, 2_760);
        assert_eq!(slots[0].new, 2);
        assert!(slots[0].still());
        assert!(slots[0].folded());
        assert_eq!(
            slots[0].episode,
            Some(InSeries {
                series: "series:1".into(),
                name: "The Serial".into(),
                season: 3,
                episode: 8,
            })
        );
    }

    #[test]
    fn the_newest_episode_takes_the_slot_whatever_order_they_came_in() {
        let slots = shown(vec![
            episode(7, "2026-08-27", 1),
            episode(8, "2026-09-03", 1),
        ]);
        assert_eq!(slots[0].art, "still-8.jpg");
        assert_eq!(slots[0].released, "2026-09-03");
        assert_eq!(slots[0].new, 2);
    }

    #[test]
    fn two_episodes_of_one_date_break_the_tie_by_their_numbers() {
        let slots = shown(vec![
            episode(7, "2026-09-03", 1),
            episode(8, "2026-09-03", 1),
        ]);
        assert_eq!(slots[0].art, "still-8.jpg");
    }

    #[test]
    fn a_series_with_one_current_episode_is_one_new() {
        let slots = shown(vec![
            episode(8, "2026-09-03", 1),
            episode(1, "2026-03-01", 400),
        ]);
        assert_eq!(slots[0].new, 1);
        assert_eq!(slots[0].art, "still-8.jpg");
    }

    #[test]
    fn a_series_of_old_episodes_alone_is_none_new_and_still_draws_as_a_still() {
        let slots = shown(vec![
            episode(2, "2026-03-01", 400),
            episode(1, "2026-02-01", 400),
        ]);
        assert_eq!(ids(&slots), ["series:1"]);
        assert_eq!(slots[0].new, 0);
        assert_eq!(slots[0].art, "still-2.jpg");
        assert!(slots[0].still());
    }

    // One episode of a second serial, released on the date and arrived
    // the same day.
    fn other(number: i64, released: &str) -> Candidate {
        Candidate::Episode {
            library: LIBRARY.into(),
            episode: Title {
                id: format!("part:{number}"),
                title: format!("Part {number}"),
                released: released.into(),
                art: format!("part-{number}.jpg"),
                duration: 1_800,
                ..Title::default()
            },
            added: date_seconds(released).unwrap_or(0),
            season: 1,
            number,
            series: Title {
                id: "series:2".into(),
                title: "The Other".into(),
                released: "2019".into(),
                art: "other.jpg".into(),
                ..Title::default()
            },
        }
    }

    #[test]
    fn two_series_interleaved_keep_their_order_by_their_newest_episode() {
        let slots = shown(vec![
            episode(8, "2026-09-03", 1),
            other(4, "2026-09-01"),
            movie("movie:1", "2026-08-30"),
            episode(7, "2026-08-27", 1),
            other(3, "2026-08-20"),
        ]);
        assert_eq!(ids(&slots), ["series:1", "series:2", "movie:1"]);
        assert_eq!(slots[0].released, "2026-09-03");
        assert_eq!(slots[1].released, "2026-09-01");
        assert_eq!(slots[1].title, "Part 4");
        assert_eq!(slots[1].new, 2);
        assert_eq!(slots[2].kind, "movies");
    }

    #[test]
    fn an_episode_with_no_full_date_folds_under_airing() {
        let slots = fold(vec![episode(1, "2004", 0)], Fold::Airing);
        assert_eq!(ids(&slots), ["series:1"]);
        assert_eq!(slots[0].released, "2004");
    }

    #[test]
    fn a_series_appears_once_however_its_episodes_interleave() {
        let slots = fold(
            vec![
                episode(3, "2026-09-03", 400),
                movie("movie:1", "2026-09-01"),
                episode(2, "2026-08-27", 400),
            ],
            Fold::Titles,
        );
        assert_eq!(ids(&slots), ["series:1", "movie:1"]);
    }

    #[test]
    fn a_movie_is_its_own_slot_under_every_fold() {
        for rule in [
            Fold::Titles,
            Fold::Episodes,
            Fold::Airing,
            Fold::Shows { today: 0 },
        ] {
            let slots = fold(vec![movie("movie:1", "1999")], rule);
            assert_eq!(ids(&slots), ["movie:1"]);
            assert_eq!(slots[0].kind, "movies");
        }
    }

    // A season of one serial, as one page of candidates, every episode
    // older than the window so the fold makes one slot of the page.
    fn a_season(page: usize) -> Vec<Candidate> {
        (0..CANDIDATES)
            .map(|index| {
                let number = (page * CANDIDATES + index) as i64;
                episode(number, "2004-03-01", 400)
            })
            .collect()
    }

    #[test]
    fn a_read_pages_past_a_season_drop_until_it_has_its_fill() {
        let mut pages = Vec::new();
        let slots = filled(Fold::Titles, |page| {
            pages.push(page);
            let mut candidates = a_season(page);
            for index in 0..FILL / 4 {
                candidates.push(movie(&format!("movie:{page}:{index}"), "2001"));
            }
            candidates
        });
        assert_eq!(pages, [0, 1, 2, 3]);
        assert!(slots.len() >= FILL, "{} slots", slots.len());
        assert_eq!(slots.iter().filter(|slot| slot.kind == "series").count(), 1);
    }

    #[test]
    fn a_short_page_ends_the_read() {
        let slots = filled(Fold::Titles, |page| match page {
            0 => vec![movie("movie:1", "2001")],
            _ => panic!("a second page after a short one"),
        });
        assert_eq!(ids(&slots), ["movie:1"]);
    }

    #[test]
    fn the_page_bound_ends_a_read_that_never_fills() {
        let mut pages = 0;
        let slots = filled(Fold::Titles, |page| {
            pages += 1;
            a_season(page)
        });
        assert_eq!(pages, PAGES);
        assert_eq!(ids(&slots), ["series:1"]);
    }

    #[test]
    fn the_strip_shows_fewer_than_the_read_takes() {
        const { assert!(SHOWN < CANDIDATES) };
        const { assert!(WINDOW_DAYS > 0) };
    }
}
