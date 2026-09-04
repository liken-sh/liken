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

/// How many candidate rows a read takes, newest first, before the fold.
/// The bound keeps the read small on a catalog of thousands.
pub const CANDIDATES: usize = 120;

/// How many slots a strip shows of what the fold answered. The wall
/// behind "see all" shows the rest.
pub const SHOWN: usize = 20;

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

/// The fold, over candidates in the query's order, newest first. A
/// movie is a slot. An episode that stands alone is a slot with its
/// still. Every other episode folds to its series, and a series appears
/// once, at the newest date among its folded episodes.
pub fn fold(candidates: Vec<Candidate>, fold: Fold) -> Vec<Slot> {
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

// Whether an episode stands alone under this fold: never under
// `Titles`, always under `Episodes`, and under `Airing` when its release
// date and its arrival fall inside the window. An episode with no full
// date folds, because the gap cannot be measured.
fn stands_alone(fold: Fold, released: &str, added: i64) -> bool {
    match fold {
        Fold::Titles => false,
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
        for rule in [Fold::Titles, Fold::Episodes, Fold::Airing] {
            let slots = fold(vec![movie("movie:1", "1999")], rule);
            assert_eq!(ids(&slots), ["movie:1"]);
            assert_eq!(slots[0].kind, "movies");
        }
    }

    #[test]
    fn the_strip_shows_fewer_than_the_read_takes() {
        const { assert!(SHOWN < CANDIDATES) };
        const { assert!(WINDOW_DAYS > 0) };
    }
}
