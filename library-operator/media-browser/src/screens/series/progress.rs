// How far the audience reached in a series, as the episode wall draws it:
// a bar under every still they played, and focus on the episode they watch
// next.

use super::{Focus, Series, Still};
use crate::catalog::progress::thread;
use crate::catalog::{Progress, Resume, Source};

/// Read how far these people reached in each episode of the series, and
/// hang each row on the still it names. Focus lands on the episode to watch
/// next, unless the way into the page already named an episode. The browser
/// calls it at every open and at every re-read, because only the browser
/// holds the audience.
pub fn read(page: &mut Series, source: &mut dyn Source, people: &[String]) {
    let rows = source.episode_progress(&page.library, &page.id, people);
    for still in &mut page.stills {
        still.progress = None;
    }
    for row in rows {
        let found = page
            .stills
            .iter_mut()
            .find(|still| still.season == row.season && still.episode == row.episode);
        if let Some(still) = found {
            still.progress = Some(row);
        }
    }
    if !page.placed {
        let plays = source.plays_of(&page.library, &page.id, people);
        page.focus = Focus::Still(next_episode(&page.stills, &plays));
        page.refoot(source);
    }
}

/// The share of one episode its bar draws: the whole work for an episode
/// the audience finished, and the share they reached for one they are in
/// the middle of. Nothing for an episode no play of theirs names.
pub fn share(progress: Option<&Progress>) -> Option<f32> {
    progress.map(|progress| match progress.finished {
        true => 1.0,
        false => progress.played().fraction(),
    })
}

/// The second a press on one still starts the play at: where the audience
/// stopped in an episode they are in the middle of, and the beginning of
/// every other episode.
pub fn start(still: &Still) -> Option<i64> {
    still
        .progress
        .as_ref()
        .filter(|progress| !progress.finished)
        .map(|progress| progress.position)
}

// The episode the wall opens on: the leaf the thread rule offers over
// the stills in aired order and these plays of the series, which is the
// episode the continue-watching row's card names. The first episode where
// the walk offers nothing.
pub fn next_episode(stills: &[Still], plays: &[Resume]) -> usize {
    let on: Vec<thread::Play> = plays
        .iter()
        .filter_map(|play| {
            let numbers = (play.progress.season, play.progress.episode);
            let leaf = stills
                .iter()
                .position(|still| (still.season, still.episode) == numbers)?;
            Some(thread::Play {
                leaf,
                progress: play.progress.clone(),
                exact: play.exact,
            })
        })
        .collect();
    thread::walk(stills.len(), &on)
        .map(|offer| offer.leaf)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    // One play of one episode of a wall of four: the second it was recorded,
    // and whether it finished.
    fn play(index: usize, recorded: i64, finished: bool) -> Progress {
        Progress {
            play: format!("play-{recorded}"),
            position: match finished {
                true => 2_700,
                false => 900,
            },
            duration: 2_760,
            finished,
            recorded,
            season: 1,
            episode: index as i64 + 1,
            ..Progress::default()
        }
    }

    // A wall of four episodes, with a play on each one the case names.
    fn stills(played: &[(usize, i64, bool)]) -> Vec<Still> {
        let mut stills: Vec<Still> = (1..=4)
            .map(|episode| Still {
                season: 1,
                episode,
                ..Still::default()
            })
            .collect();
        for (index, recorded, finished) in played {
            stills[*index].progress = Some(play(*index, *recorded, *finished));
        }
        stills
    }

    // The same plays as the store answers them, every one the audience's
    // own.
    fn plays(played: &[(usize, i64, bool)]) -> Vec<Resume> {
        played
            .iter()
            .map(|(index, recorded, finished)| Resume {
                progress: play(*index, *recorded, *finished),
                exact: true,
                ..Resume::default()
            })
            .collect()
    }

    // The still the wall opens on after these plays.
    fn opens_on(played: &[(usize, i64, bool)]) -> usize {
        next_episode(&stills(played), &plays(played))
    }

    #[test]
    fn a_series_no_play_names_opens_on_its_first_episode() {
        assert_eq!(opens_on(&[]), 0);
    }

    #[test]
    fn a_series_opens_on_the_episode_the_latest_play_stopped_in() {
        assert_eq!(opens_on(&[(0, 100, true), (1, 200, false)]), 1);
    }

    #[test]
    fn a_series_opens_on_the_episode_after_the_one_they_finished() {
        assert_eq!(opens_on(&[(0, 200, true), (1, 100, true)]), 1);
    }

    #[test]
    fn a_series_they_finished_to_the_end_opens_on_its_first_episode() {
        assert_eq!(opens_on(&[(3, 100, true)]), 0);
    }

    #[test]
    fn a_play_of_more_people_alone_opens_the_wall_on_its_first_episode() {
        let mut plays = plays(&[(1, 100, true)]);
        plays[0].exact = false;
        assert_eq!(next_episode(&stills(&[]), &plays), 0);
    }

    #[test]
    fn a_play_of_an_episode_the_wall_does_not_hold_is_not_in_the_walk() {
        let mut plays = plays(&[(0, 100, true), (1, 200, true)]);
        plays[1].progress.season = 9;
        assert_eq!(next_episode(&stills(&[]), &plays), 1);
    }

    #[test]
    fn a_finished_episode_draws_the_whole_bar() {
        let stills = stills(&[(0, 100, true), (1, 200, false)]);
        assert_eq!(share(stills[0].progress.as_ref()), Some(1.0));
        assert_eq!(
            share(stills[1].progress.as_ref()),
            Some(900.0 / 2_760.0_f32)
        );
        assert_eq!(share(stills[2].progress.as_ref()), None);
    }

    #[test]
    fn a_press_resumes_only_an_episode_they_are_in_the_middle_of() {
        let stills = stills(&[(0, 100, true), (1, 200, false)]);
        assert_eq!(start(&stills[0]), None);
        assert_eq!(start(&stills[1]), Some(900));
        assert_eq!(start(&stills[2]), None);
    }
}
