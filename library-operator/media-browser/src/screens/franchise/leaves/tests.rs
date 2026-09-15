// The leaves of an order: what a film contributes, what a run of a
// series contributes, and what a gap contributes.

use super::*;
use crate::screens::franchise::tests::{AIRED, FIRST, Orders, SECOND, order, split};

// Every leaf of these entries as the work it names and the member it
// came from: a film by its id, an episode by its aired numbers.
fn drawn(entries: &[Entry]) -> Vec<(String, i64)> {
    let mut episodes = Episodes::default();
    of(&mut Orders::default(), &mut episodes, entries)
        .into_iter()
        .map(|leaf| {
            let named = match &leaf.slot.episode {
                Some(place) => format!("S{}E{}", place.season, place.episode),
                None => leaf.slot.id.clone(),
            };
            (named, leaf.member)
        })
        .collect()
}

#[test]
fn the_films_and_the_covered_episodes_stand_in_story_order() {
    let first: Vec<(String, i64)> = (1..=AIRED)
        .map(|episode| (format!("S1E{episode}"), 2))
        .collect();
    let second: Vec<(String, i64)> = (1..=AIRED)
        .map(|episode| (format!("S2E{episode}"), 4))
        .collect();
    let mut story = vec![(FIRST.to_string(), 1)];
    story.extend(first);
    story.push((SECOND.to_string(), 3));
    story.extend(second);

    assert_eq!(drawn(&split()), story);
}

#[test]
fn an_episode_outside_a_members_runs_is_no_leaf_of_it() {
    let leaves = drawn(&split());
    assert!(leaves.iter().all(|(named, member)| match member {
        2 => named.starts_with("S1E") || named == FIRST,
        4 => named.starts_with("S2E") || named == SECOND,
        _ => true,
    }));
    assert_eq!(leaves.iter().filter(|(_, member)| *member == 2).count(), 5);
}

#[test]
fn a_gap_contributes_no_leaf() {
    let leaves = drawn(&order());
    let gap = order()[3].clone();

    assert!(gap.held.is_none());
    assert!(leaves.iter().all(|(_, member)| *member != gap.position));
}

#[test]
fn a_member_whose_runs_name_a_season_the_catalog_holds_no_episode_of_is_empty() {
    let member = Entry {
        runs: vec![(9, 0)],
        ..split()[1].clone()
    };

    assert_eq!(drawn(&[member]), []);
}
