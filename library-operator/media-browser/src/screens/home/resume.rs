// The continue-watching row: the leaves the audience's threads offer,
// one card per leaf with the reasons stacked on it, newest thread first.
// The row reads every play of the audience once, finds the containers
// those plays seed (the film, its set, the series, and every franchise the
// work belongs to), walks each one, and folds the offers into cards. A
// press on a card opens the page its first reason points at.

mod containers;
mod reason;

use std::collections::{HashMap, HashSet};

pub use reason::Reason;

use self::containers::{Container, MOVIES, Work};
use crate::catalog::progress::thread;
use crate::catalog::recency::SHOWN;
use crate::catalog::{Resume, Slot, Source};
use crate::screens::{InFranchise, Item, facts};

/// The heading over the row.
pub const HEADING: &str = "Continue watching";

// The kind word a play of a series carries, as the progress read writes
// it.
const SERIES: &str = "series";

// One card of the row: the leaf as a slot, with the position to resume
// at where a thread stands unfinished on it; the reasons in their order;
// and the recorded time of the newest play that moved a thread onto it,
// which the row sorts on.
#[derive(Debug, Clone, PartialEq)]
pub struct Card {
    pub slot: Slot,
    pub reasons: Vec<Reason>,
    pub recorded: i64,
}

// The row for these people: at most `SHOWN` cards, newest first.
pub fn cards(source: &mut dyn Source, people: &[String]) -> Vec<Card> {
    let plays = grouped(source.continue_watching(people));
    let mut cards: Vec<Card> = Vec::new();
    for container in seeded(source, &plays) {
        let leaves = containers::leaves(source, &container);
        let Some(offer) = thread::walk(leaves.len(), &containers::on_leaves(&leaves, &plays))
        else {
            continue;
        };
        let leaf = &leaves[offer.leaf];
        let reason = match &offer.progress {
            Some(_) => Reason::Resume {
                series: leaf
                    .slot
                    .episode
                    .as_ref()
                    .map(|place| place.name.clone())
                    .unwrap_or_default(),
            },
            None => match containers::title(source, &container) {
                Some(title) => next_in(&container, leaf.member, title),
                None => continue,
            },
        };
        let mut slot = leaf.slot.clone();
        slot.progress = offer.progress;
        fold(&mut cards, slot, reason, offer.recorded);
    }
    for card in &mut cards {
        reason::stacked(&mut card.reasons);
    }
    cards.sort_by_key(|card| std::cmp::Reverse(card.recorded));
    cards.truncate(SHOWN);
    cards
}

// The "next in" reason of one container, spelled with its title.
fn next_in(container: &Container, member: i64, title: String) -> Reason {
    match container {
        Container::Film(_) | Container::Series { .. } => Reason::Series(title),
        Container::Set { .. } => Reason::Set(title),
        Container::Franchise(membership) => Reason::Franchise {
            library: membership.library.clone(),
            id: membership.id.clone(),
            position: member,
            title,
        },
    }
}

// One offer onto the cards. The card of the same leaf takes the reason,
// the newer time, and the resume position where this thread resumes. A
// leaf no card holds becomes one.
fn fold(cards: &mut Vec<Card>, slot: Slot, reason: Reason, recorded: i64) {
    let same = |card: &Card| {
        card.slot.library == slot.library && card.slot.kind == slot.kind && card.slot.id == slot.id
    };
    match cards.iter_mut().find(|card| same(card)) {
        Some(card) => {
            card.reasons.push(reason);
            card.recorded = card.recorded.max(recorded);
            if slot.progress.is_some() {
                card.slot.progress = slot.progress;
            }
        }
        None => cards.push(Card {
            slot,
            reasons: vec![reason],
            recorded,
        }),
    }
}

// Every play of the audience, keyed by the work it names, in the order
// the read answered them.
fn grouped(plays: Vec<Resume>) -> HashMap<Work, Vec<Resume>> {
    let mut grouped: HashMap<Work, Vec<Resume>> = HashMap::new();
    for play in plays {
        grouped
            .entry((play.library.clone(), play.id.clone()))
            .or_default()
            .push(play);
    }
    grouped
}

// The containers the plays seed, each once, in the order of the newest
// play of each work. Only a work with a play that names exactly the
// audience seeds any, because a container with no such play has no
// thread. A film seeds itself, its set, and its franchises. A series seeds
// itself and its franchises.
fn seeded(source: &mut dyn Source, plays: &HashMap<Work, Vec<Resume>>) -> Vec<Container> {
    let mut works: Vec<&Resume> = plays
        .values()
        .filter_map(|plays| plays.iter().find(|play| play.exact))
        .collect();
    works.sort_by_key(|work| {
        (
            std::cmp::Reverse(work.progress.recorded),
            work.library.clone(),
            work.id.clone(),
        )
    });
    let mut seen: HashSet<(u8, String, String)> = HashSet::new();
    let mut containers = Vec::new();
    for work in works {
        for container in of_work(source, work) {
            if seen.insert(container.key()) {
                containers.push(container);
            }
        }
    }
    containers
}

// The containers one work seeds.
fn of_work(source: &mut dyn Source, work: &Resume) -> Vec<Container> {
    let mut containers = Vec::new();
    if work.kind == SERIES {
        containers.push(Container::Series {
            library: work.library.clone(),
            id: work.id.clone(),
            title: work.title.clone(),
        });
    } else {
        let Some(details) = source.movie(&work.library, &work.id) else {
            return Vec::new();
        };
        containers.push(Container::Film(Box::new(Slot {
            library: work.library.clone(),
            kind: MOVIES.to_string(),
            id: work.id.clone(),
            title: work.title.clone(),
            released: work.released.clone(),
            art: work.art.clone(),
            duration: details.duration,
            rating: details.rating,
            tagline: details.tagline,
            ..Slot::default()
        })));
        if !details.set_id.is_empty() {
            containers.push(Container::Set {
                library: work.library.clone(),
                id: details.set_id,
            });
        }
    }
    for membership in source.franchises_of(&work.library, &work.id) {
        containers.push(Container::Franchise(membership));
    }
    containers
}

// One card as the item the strip draws: the caption every slot of its
// kind takes, the reasons as the second line, and the member the first
// reason opens where that reason is a franchise.
pub fn item(card: Card) -> Item {
    let season = card
        .slot
        .episode
        .as_ref()
        .map(|place| format!("S{:02}", place.season))
        .unwrap_or_default();
    let under = facts::joined(&[&reason::line(&card.reasons), &season]);
    let franchise = match card.reasons.first() {
        Some(Reason::Franchise {
            library,
            id,
            position,
            ..
        }) => Some(InFranchise {
            library: library.clone(),
            id: id.clone(),
            position: *position,
        }),
        _ => None,
    };
    Item::resumed(card.slot, under, franchise)
}

#[cfg(test)]
mod tests;
