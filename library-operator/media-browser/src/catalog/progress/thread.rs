// The thread rule. A container is an ordered list of leaves. The thread
// stands at the latest play that names exactly the people at the screen.
// Every later play with more people counts only when its leaf is the one
// the thread expects next. Next up is the leaf the thread stands on while
// it is unfinished, the leaf after it once it is finished, and nothing
// after the last leaf. Every container kind (a series, a set, a franchise)
// and the series page call this one function, so a card and a page agree.

use super::{Played, Progress};

// One play on one leaf of a container: the leaf's index in the
// container's order, where the play reached, and whether the play names
// exactly the people at the screen.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Play {
    pub leaf: usize,
    pub progress: Progress,
    pub exact: bool,
}

// What a container offers the people at the screen: the leaf; the
// position to resume at while that leaf is unfinished, and nothing for a
// leaf they have not started; the recorded time of the play that moved the
// thread last, which the row sorts on; and that play itself, which a page
// draws where it resumes.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Offer {
    pub leaf: usize,
    pub progress: Option<Played>,
    pub recorded: i64,
    pub standing: Progress,
}

// The walk over one container of `leaves` leaves and the plays on them.
// A play on a leaf past the end is ignored. A tie in recorded time breaks
// on the play name, so the order is stable. A container with no play that
// names exactly the audience has no thread and offers nothing.
pub fn walk(leaves: usize, plays: &[Play]) -> Option<Offer> {
    let mut ordered: Vec<&Play> = plays.iter().filter(|play| play.leaf < leaves).collect();
    ordered.sort_by(|one, other| {
        (one.progress.recorded, &one.progress.play)
            .cmp(&(other.progress.recorded, &other.progress.play))
    });
    let own = ordered.iter().rposition(|play| play.exact)?;
    let mut standing = ordered[own];
    for play in &ordered[own + 1..] {
        if play.leaf == expected(standing) {
            standing = play;
        }
    }
    let recorded = standing.progress.recorded;
    match (standing.progress.finished, standing.leaf + 1 < leaves) {
        (false, _) => Some(Offer {
            leaf: standing.leaf,
            progress: Some(standing.progress.played()),
            recorded,
            standing: standing.progress.clone(),
        }),
        (true, true) => Some(Offer {
            leaf: standing.leaf + 1,
            progress: None,
            recorded,
            standing: standing.progress.clone(),
        }),
        (true, false) => None,
    }
}

// The leaf the thread expects the next play on: the same leaf while the
// standing play is unfinished, and the leaf after it once it is finished.
fn expected(standing: &Play) -> usize {
    standing.leaf + usize::from(standing.progress.finished)
}

#[cfg(test)]
mod tests {
    use super::*;

    // How long every leaf of these cases runs.
    const RUNTIME: i64 = 2_760;

    // One night in the store: the leaf, the people on the play as one word
    // of initials, the second it was recorded, and whether it finished.
    type Night = (usize, &'static str, i64, bool);

    // Whether a play names every person of the audience, which is what the
    // store's read fetches, and inside that whether it names exactly them.
    fn names(people: &str, audience: &str) -> Option<bool> {
        let every = audience.chars().all(|person| people.contains(person));
        every.then_some(people.len() == audience.len())
    }

    // The plays the store answers this audience, out of these nights.
    fn plays(nights: &[Night], audience: &str) -> Vec<Play> {
        nights
            .iter()
            .filter_map(|(leaf, people, recorded, finished)| {
                let exact = names(people, audience)?;
                Some(Play {
                    leaf: *leaf,
                    progress: Progress {
                        play: format!("{people}-{recorded}"),
                        position: match finished {
                            true => RUNTIME,
                            false => 900,
                        },
                        duration: RUNTIME,
                        finished: *finished,
                        recorded: *recorded,
                        ..Progress::default()
                    },
                    exact,
                })
            })
            .collect()
    }

    // The leaf a series of six offers this audience, and nothing where the
    // walk offers nothing.
    fn offered(nights: &[Night], audience: &str) -> Option<usize> {
        walk(6, &plays(nights, audience)).map(|offer| offer.leaf)
    }

    const FIRST_TWO: [Night; 2] = [(0, "ABC", 1, true), (1, "ABC", 2, true)];

    #[test]
    fn night_one_the_three_who_watched_see_the_next_and_a_subset_sees_nothing() {
        assert_eq!(offered(&FIRST_TWO, "ABC"), Some(2));
        assert_eq!(offered(&FIRST_TWO, "AB"), None);
        assert_eq!(offered(&FIRST_TWO, "A"), None);
    }

    #[test]
    fn night_two_a_guest_night_that_continued_the_thread_moves_it() {
        let nights = [FIRST_TWO[0], FIRST_TWO[1], (2, "ABCD", 3, true)];
        assert_eq!(offered(&nights, "ABC"), Some(3));
        assert_eq!(offered(&nights, "ABCD"), Some(3));
        assert_eq!(offered(&nights, "AB"), None);
    }

    #[test]
    fn night_three_a_night_without_one_of_them_leaves_their_thread_where_it_was() {
        let nights = [FIRST_TWO[0], FIRST_TWO[1], (2, "AB", 3, true)];
        assert_eq!(offered(&nights, "ABC"), Some(2));
        assert_eq!(offered(&nights, "AB"), Some(3));
    }

    #[test]
    fn night_four_a_thread_started_alone_takes_the_night_a_second_person_joined() {
        let nights = [(0, "A", 1, true), (1, "AB", 2, true)];
        assert_eq!(offered(&nights, "A"), Some(2));
        assert_eq!(offered(&nights, "AB"), Some(2));
        assert_eq!(offered(&nights, "B"), None);
    }

    #[test]
    fn night_five_an_old_run_alone_does_not_adopt_a_new_groups_thread() {
        let nights = [
            (0, "A", 1, true),
            (1, "A", 2, true),
            (2, "A", 3, true),
            (3, "A", 4, true),
            (4, "A", 5, true),
            (5, "A", 6, true),
            (0, "ABC", 10, true),
        ];
        assert_eq!(offered(&nights, "A"), None);
        assert_eq!(offered(&nights, "ABC"), Some(1));
    }

    #[test]
    fn an_unfinished_leaf_resumes_where_the_play_reached() {
        let plays = plays(&[(1, "A", 1, false)], "A");
        let offer = walk(6, &plays).expect("the thread stands");
        assert_eq!(
            offer,
            Offer {
                leaf: 1,
                progress: Some(Played {
                    position: 900,
                    duration: RUNTIME
                }),
                recorded: 1,
                standing: plays[0].progress.clone(),
            }
        );
    }

    #[test]
    fn a_superset_play_of_the_unfinished_leaf_finishes_it() {
        let nights = [(1, "A", 1, false), (1, "AB", 2, true)];
        assert_eq!(offered(&nights, "A"), Some(2));
    }

    #[test]
    fn a_superset_play_off_the_thread_is_skipped_and_the_walk_goes_on() {
        let nights = [(0, "A", 1, true), (3, "AB", 2, true), (1, "AB", 3, true)];
        assert_eq!(offered(&nights, "A"), Some(2));
    }

    #[test]
    fn the_offer_carries_the_time_of_the_play_that_moved_the_thread_last() {
        let nights = [(0, "A", 1, true), (3, "AB", 2, true), (1, "AB", 3, true)];
        let offer = walk(6, &plays(&nights, "A")).expect("the thread stands");
        assert_eq!(offer.recorded, 3);
        assert_eq!(offer.progress, None);
        assert_eq!(offer.standing.play, "AB-3");
    }

    #[test]
    fn the_latest_own_play_stands_whatever_order_the_rows_came_in() {
        let nights = [(3, "A", 5, true), (0, "A", 1, true)];
        assert_eq!(offered(&nights, "A"), Some(4));
    }

    #[test]
    fn a_play_past_the_last_leaf_is_not_in_the_walk() {
        let nights = [(0, "A", 1, true), (9, "A", 2, true)];
        assert_eq!(offered(&nights, "A"), Some(1));
    }

    #[test]
    fn a_set_offers_the_film_after_the_one_they_finished_and_nothing_after_the_last() {
        let nights = [(0, "AB", 1, true)];
        assert_eq!(
            walk(3, &plays(&nights, "AB")).map(|offer| offer.leaf),
            Some(1)
        );
        let last = [(2, "AB", 1, true)];
        assert_eq!(walk(3, &plays(&last, "AB")), None);
    }

    #[test]
    fn a_franchise_offers_the_film_after_a_series_members_last_held_episode() {
        // A film, two episodes of a series, and a film after them.
        let nights = [(2, "AB", 1, true)];
        assert_eq!(
            walk(4, &plays(&nights, "AB")).map(|offer| offer.leaf),
            Some(3)
        );
    }

    #[test]
    fn a_leaf_the_audience_finished_before_is_offered_again() {
        let nights = [(1, "A", 1, true), (0, "A", 2, true)];
        assert_eq!(offered(&nights, "A"), Some(1));
    }

    #[test]
    fn an_empty_container_offers_nothing() {
        assert_eq!(walk(0, &plays(&[(0, "A", 1, true)], "A")), None);
        assert_eq!(walk(3, &[]), None);
    }
}
