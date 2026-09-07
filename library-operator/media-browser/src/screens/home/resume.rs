// The continue-watching row: the slots the progress store's resumes become.
// It is the one row of the home page whose slots come from the progress
// store and not from a wall read. A press on one of its slots opens the
// work's page, as a press on every other row does.

mod next;

use std::collections::HashSet;

use crate::catalog::recency::SHOWN;
use crate::catalog::{Episode, InSeries, Played, Resume, Slot, Source};

/// The heading over the row.
pub const HEADING: &str = "Continue watching";

// The kind word a resume of a series carries, as the progress read writes
// it.
const SERIES: &str = "series";

// The kind words the slots carry. They are the catalog's own item tables,
// so a slot of this row reads as every other slot of that kind.
const MOVIES: &str = "movies";
const EPISODES: &str = "episodes";

/// What this audience is in the middle of, as slots, newest first: the
/// movie they stopped in, the episode they stopped in, or the episode after
/// the one they finished. A work they finished yields the works that follow
/// it instead, and nothing where none follows it.
pub fn slots(source: &mut dyn Source, people: &[String]) -> Vec<Slot> {
    let runs: Vec<Run> = source
        .continue_watching(people)
        .into_iter()
        .map(|resume| run(source, resume))
        .collect();
    ordered(runs).into_iter().take(SHOWN).collect()
}

// The slots one resume yields: the work itself while the audience is in
// the middle of it, and the works that follow it once they finished it.
#[derive(Default)]
struct Run {
    current: Option<Slot>,
    after: Vec<Slot>,
}

// The runs as one row, in the order the resumes came in, so a work's
// successors sit together where the resume that named them stood. A work
// the row already draws is never drawn a second time, and a successor gives
// way to the card of a work the audience is in the middle of.
fn ordered(runs: Vec<Run>) -> Vec<Slot> {
    let held: HashSet<(String, String)> = runs
        .iter()
        .filter_map(|run| run.current.as_ref())
        .map(key)
        .collect();
    let mut drawn: HashSet<(String, String)> = HashSet::new();
    let mut slots = Vec::new();
    for run in runs {
        if let Some(slot) = run.current
            && drawn.insert(key(&slot))
        {
            slots.push(slot);
        }
        for slot in run.after {
            let at = key(&slot);
            if held.contains(&at) || !drawn.insert(at) {
                continue;
            }
            slots.push(slot);
        }
    }
    slots
}

// The library and the id that name one work, which is what tells two cards
// of the row apart.
fn key(slot: &Slot) -> (String, String) {
    (slot.library.clone(), slot.id.clone())
}

// One resume as the slots the row draws, by the kind of work it names.
fn run(source: &mut dyn Source, resume: Resume) -> Run {
    match resume.kind.as_str() {
        SERIES => series(source, resume),
        _ => movie(source, resume),
    }
}

// The movie the audience stopped in, with the bar under it. A movie they
// finished yields the works that follow it, and one the catalog no longer
// holds yields nothing.
fn movie(source: &mut dyn Source, resume: Resume) -> Run {
    let Some(details) = source.movie(&resume.library, &resume.id) else {
        return Run::default();
    };
    if resume.progress.finished {
        return Run {
            current: None,
            after: next::after(source, &resume.library, &resume.id, &details.set_id),
        };
    }
    Run {
        current: Some(Slot {
            library: resume.library,
            kind: MOVIES.to_string(),
            id: resume.id,
            title: resume.title,
            released: resume.released,
            art: resume.art,
            duration: details.duration,
            rating: details.rating,
            tagline: details.tagline,
            progress: Some(resume.progress.played()),
            ..Slot::default()
        }),
        after: Vec::new(),
    }
}

// The episode of the show the row draws: the one the audience stopped in,
// with the bar under it, or the next one in aired order after the one they
// finished, with no bar, because they have not started it. A show whose
// last episode they finished yields the works that follow the show.
fn series(source: &mut dyn Source, resume: Resume) -> Run {
    let episodes = source.episodes(&resume.library, &resume.id);
    let at = (resume.progress.season, resume.progress.episode);
    if !resume.progress.finished {
        let played = resume.progress.played();
        return Run {
            current: reached(episodes, at).map(|episode| still(&resume, episode, Some(played))),
            after: Vec::new(),
        };
    }
    match following(episodes, at) {
        Some(episode) => Run {
            current: Some(still(&resume, episode, None)),
            after: Vec::new(),
        },
        None => Run {
            current: None,
            after: next::after(source, &resume.library, &resume.id, ""),
        },
    }
}

// One episode of the show the resume names, as the slot the row draws.
fn still(resume: &Resume, episode: Episode, progress: Option<Played>) -> Slot {
    Slot {
        library: resume.library.clone(),
        kind: EPISODES.to_string(),
        id: episode.id,
        title: episode.title,
        released: episode.released,
        art: episode.art,
        duration: episode.duration,
        episode: Some(InSeries {
            series: resume.id.clone(),
            name: resume.title.clone(),
            season: episode.season,
            episode: episode.episode,
        }),
        progress,
        ..Slot::default()
    }
}

// The episode the play reached, by its aired numbers, or nothing where the
// catalog holds no episode under them.
fn reached(episodes: Vec<Episode>, at: (i64, i64)) -> Option<Episode> {
    episodes
        .into_iter()
        .find(|episode| (episode.season, episode.episode) == at)
}

// The first episode after this one in aired order, which is the order the
// source answers episodes in.
fn following(episodes: Vec<Episode>, at: (i64, i64)) -> Option<Episode> {
    episodes
        .into_iter()
        .find(|episode| (episode.season, episode.episode) > at)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn episodes() -> Vec<Episode> {
        [(1, 1), (1, 2), (2, 1)]
            .into_iter()
            .map(|(season, episode)| Episode {
                id: format!("episode:{season}:{episode}"),
                season,
                episode,
                ..Episode::default()
            })
            .collect()
    }

    fn numbers(episode: Option<Episode>) -> Option<(i64, i64)> {
        episode.map(|episode| (episode.season, episode.episode))
    }

    #[test]
    fn the_episode_a_play_reached_is_the_one_its_numbers_name() {
        assert_eq!(numbers(reached(episodes(), (1, 2))), Some((1, 2)));
        assert_eq!(numbers(reached(episodes(), (3, 1))), None);
    }

    #[test]
    fn the_next_episode_crosses_into_the_season_after_it() {
        assert_eq!(numbers(following(episodes(), (1, 1))), Some((1, 2)));
        assert_eq!(numbers(following(episodes(), (1, 2))), Some((2, 1)));
        assert_eq!(numbers(following(episodes(), (2, 1))), None);
    }

    fn card(library: &str, id: &str) -> Slot {
        Slot {
            library: library.to_string(),
            kind: MOVIES.to_string(),
            id: id.to_string(),
            ..Slot::default()
        }
    }

    fn drawn(runs: Vec<Run>) -> Vec<String> {
        ordered(runs)
            .into_iter()
            .map(|slot| format!("{}/{}", slot.library, slot.id))
            .collect()
    }

    #[test]
    fn a_works_successors_sit_together_after_the_work_the_resume_named() {
        let runs = vec![
            Run {
                current: None,
                after: vec![card("films", "one"), card("films", "two")],
            },
            Run {
                current: Some(card("films", "three")),
                after: Vec::new(),
            },
        ];

        assert_eq!(drawn(runs), ["films/one", "films/two", "films/three"]);
    }

    #[test]
    fn two_successors_that_name_one_work_leave_one_card() {
        let runs = vec![Run {
            current: None,
            after: vec![card("films", "one"), card("films", "one")],
        }];

        assert_eq!(drawn(runs), ["films/one"]);
    }

    #[test]
    fn a_successor_the_audience_is_in_the_middle_of_gives_way_to_their_own_card() {
        let runs = vec![
            Run {
                current: None,
                after: vec![card("films", "one")],
            },
            Run {
                current: Some(card("films", "one")),
                after: Vec::new(),
            },
        ];

        assert_eq!(drawn(runs), ["films/one"]);
    }

    #[test]
    fn one_id_in_two_libraries_names_two_works() {
        let runs = vec![Run {
            current: None,
            after: vec![card("films", "one"), card("shorts", "one")],
        }];

        assert_eq!(drawn(runs), ["films/one", "shorts/one"]);
    }
}
