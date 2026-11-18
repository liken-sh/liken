//! A loop shown as cycles through its members, so a reader can follow
//! "A needs B needs C needs A" on one line instead of piecing the cycle
//! together from one sentence per edge.
//!
//! A loop is a strongly connected component, which can have more edges
//! than one cycle uses. For each member that no earlier cycle covers,
//! the shortest cycle through it is shown, so every member appears and
//! each line is as short as the loop allows.

use std::collections::{BTreeMap, BTreeSet, VecDeque};

use super::{Kind, Link};

/// One cycle, from its first member back to it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Cycle {
    pub steps: Vec<Step>,
}

/// A member of a cycle, and the kinds of the edges from it to the next
/// member.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Step {
    pub member: String,
    pub kinds: Vec<Kind>,
}

/// The cycles that cover every member joined by `links`. Each cycle
/// starts at the first member, in name order, that no earlier cycle
/// covers.
pub fn cycles(links: &[Link]) -> Vec<Cycle> {
    let mut next: BTreeMap<&str, BTreeMap<&str, &[Kind]>> = BTreeMap::new();
    for link in links {
        let leaving = next.entry(&link.from).or_default();
        leaving.insert(&link.to, &link.kinds);
    }
    let mut covered: BTreeSet<&str> = BTreeSet::new();
    let mut found = Vec::new();
    for &start in next.keys() {
        if covered.contains(start) {
            continue;
        }
        let Some(path) = shortest_back(start, &next) else {
            continue;
        };
        covered.extend(path.iter().copied());
        let steps = path
            .iter()
            .zip(path.iter().cycle().skip(1))
            .map(|(&from, &to)| Step {
                member: from.into(),
                kinds: next[from][to].to_vec(),
            })
            .collect();
        found.push(Cycle { steps });
    }
    found
}

/// The members of the shortest path from `start` back to it, `start`
/// first and not repeated at the end.
fn shortest_back<'a>(
    start: &'a str,
    next: &BTreeMap<&'a str, BTreeMap<&'a str, &[Kind]>>,
) -> Option<Vec<&'a str>> {
    let mut came_from: BTreeMap<&str, &str> = BTreeMap::new();
    let mut queue: VecDeque<&str> = VecDeque::from([start]);
    while let Some(at) = queue.pop_front() {
        for &to in next.get(at).into_iter().flat_map(BTreeMap::keys) {
            if to == start {
                let mut path = vec![at];
                while let Some(&before) = came_from.get(path[path.len() - 1]) {
                    path.push(before);
                }
                path.reverse();
                return Some(path);
            }
            if !came_from.contains_key(to) {
                came_from.insert(to, at);
                queue.push_back(to);
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::link;
    use super::*;

    fn step(member: &str, kinds: &[Kind]) -> Step {
        Step {
            member: member.into(),
            kinds: kinds.to_vec(),
        }
    }

    const R: Kind = Kind::ReconcilesFrom;
    const D: Kind = Kind::DependsOn;

    #[rstest]
    #[case::a_ring_of_three(
        vec![link("a", "b", &[R]), link("b", "c", &[R]), link("c", "a", &[D])],
        vec![Cycle { steps: vec![step("a", &[R]), step("b", &[R]), step("c", &[D])] }]
    )]
    #[case::two_kinds_on_one_arrow(
        vec![link("a", "b", &[R, D]), link("b", "a", &[D])],
        vec![Cycle { steps: vec![step("a", &[R, D]), step("b", &[D])] }]
    )]
    #[case::the_shortest_way_back(
        vec![
            link("a", "b", &[D]),
            link("b", "a", &[D]),
            link("b", "c", &[D]),
            link("c", "d", &[D]),
            link("d", "a", &[D]),
        ],
        vec![
            Cycle { steps: vec![step("a", &[D]), step("b", &[D])] },
            Cycle { steps: vec![step("c", &[D]), step("d", &[D]), step("a", &[D]), step("b", &[D])] },
        ]
    )]
    fn covers_every_member_with_the_shortest_cycles(
        #[case] links: Vec<Link>,
        #[case] expected: Vec<Cycle>,
    ) {
        assert_eq!(cycles(&links), expected);
    }
}
