// How far the audience reached in a series, as the episode wall draws it:
// a bar under every still they played, and focus on the episode they watch
// next.

use super::{Focus, Series, Still};
use crate::catalog::{Progress, Source};

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
        page.focus = Focus::Still(next_episode(&page.stills));
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

/// The episode the wall opens on: the one the latest play of the series
/// reached while they are in the middle of it, the one after it where they
/// finished it, and the first episode where the series holds neither.
pub fn next_episode(stills: &[Still]) -> usize {
    let Some(index) = latest(stills) else {
        return 0;
    };
    let finished = stills[index]
        .progress
        .as_ref()
        .is_some_and(|progress| progress.finished);
    match (finished, index + 1 < stills.len()) {
        (true, true) => index + 1,
        (true, false) => 0,
        (false, _) => index,
    }
}

// The still of the latest play of the series, by the second the store last
// wrote its row, or nothing where no play names an episode the wall holds.
fn latest(stills: &[Still]) -> Option<usize> {
    stills
        .iter()
        .enumerate()
        .filter_map(|(index, still)| {
            still
                .progress
                .as_ref()
                .map(|progress| (index, progress.recorded))
        })
        .max_by_key(|(_, recorded)| *recorded)
        .map(|(index, _)| index)
}

#[cfg(test)]
mod tests {
    use super::*;

    // A wall of four episodes, with a play on each one the case names: the
    // second it was recorded, and whether it finished.
    fn stills(played: &[(usize, i64, bool)]) -> Vec<Still> {
        let mut stills: Vec<Still> = (1..=4)
            .map(|episode| Still {
                season: 1,
                episode,
                ..Still::default()
            })
            .collect();
        for (index, recorded, finished) in played {
            stills[*index].progress = Some(Progress {
                position: match finished {
                    true => 2_700,
                    false => 900,
                },
                duration: 2_760,
                finished: *finished,
                recorded: *recorded,
                ..Progress::default()
            });
        }
        stills
    }

    #[test]
    fn a_series_no_play_names_opens_on_its_first_episode() {
        assert_eq!(next_episode(&stills(&[])), 0);
    }

    #[test]
    fn a_series_opens_on_the_episode_the_latest_play_stopped_in() {
        assert_eq!(next_episode(&stills(&[(0, 100, true), (1, 200, false)])), 1);
    }

    #[test]
    fn a_series_opens_on_the_episode_after_the_one_they_finished() {
        assert_eq!(next_episode(&stills(&[(0, 200, true), (1, 100, true)])), 1);
    }

    #[test]
    fn a_series_they_finished_to_the_end_opens_on_its_first_episode() {
        assert_eq!(next_episode(&stills(&[(3, 100, true)])), 0);
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
