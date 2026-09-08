// A container is an ordered list of leaves, and a leaf is an episode or
// a film. This module lists the leaves of each kind of container the row
// walks: a film alone, a series in aired order, a set in release order,
// and a franchise in story order with its series members expanded into
// their episodes and cut to their runs. It also maps the audience's plays
// onto those leaves for the walk.

use std::collections::HashMap;

use crate::catalog::progress::thread;
use crate::catalog::{Episode, InSeries, Membership, Resume, Slot, Source, franchise};

// The kind words the leaves carry. They are the catalog's own item tables,
// so a slot of this row reads as every other slot of that kind.
pub const MOVIES: &str = "movies";
pub const EPISODES: &str = "episodes";

// One container the row walks, with what its leaves are listed from. A
// film alone is a container of one leaf, so a film in no set and no
// franchise still resumes.
#[derive(Debug, Clone, PartialEq)]
pub enum Container {
    Film(Box<Slot>),
    Series {
        library: String,
        id: String,
        title: String,
    },
    Set {
        library: String,
        id: String,
    },
    Franchise(Membership),
}

impl Container {
    // What tells two containers apart, so a set two of its films seed is
    // walked once.
    pub fn key(&self) -> (u8, String, String) {
        match self {
            Self::Film(slot) => (0, slot.library.clone(), slot.id.clone()),
            Self::Series { library, id, .. } => (1, library.clone(), id.clone()),
            Self::Set { library, id } => (2, library.clone(), id.clone()),
            Self::Franchise(membership) => (3, membership.library.clone(), membership.id.clone()),
        }
    }
}

// One leaf of a container as the slot the row draws, and the position of
// the franchise member it came from, zero outside a franchise.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Leaf {
    pub slot: Slot,
    pub member: i64,
}

// The title the "next in" reason of a container names, read from the
// catalog where the container does not carry it. Nothing for a film alone,
// which offers nothing after itself.
pub fn title(source: &mut dyn Source, container: &Container) -> Option<String> {
    match container {
        Container::Film(_) => None,
        Container::Series { title, .. } => Some(title.clone()),
        Container::Set { library, id } => source.set(library, id).map(|set| set.title),
        Container::Franchise(membership) => Some(membership.title.clone()),
    }
}

// The leaves of one container in its order. A member no library holds
// and an episode outside a member's runs contribute none.
pub fn leaves(source: &mut dyn Source, container: &Container) -> Vec<Leaf> {
    match container {
        Container::Film(slot) => vec![leaf((**slot).clone(), 0)],
        Container::Series { library, id, title } => source
            .episodes(library, id)
            .into_iter()
            .map(|episode| leaf(still(library, id, title, episode), 0))
            .collect(),
        Container::Set { library, id } => source
            .set(library, id)
            .map(|set| set.members)
            .unwrap_or_default()
            .into_iter()
            .map(|member| leaf(Slot::of(library, MOVIES, member), 0))
            .collect(),
        Container::Franchise(membership) => membership
            .members
            .iter()
            .flat_map(|entry| member(source, entry))
            .collect(),
    }
}

fn leaf(slot: Slot, member: i64) -> Leaf {
    Leaf { slot, member }
}

// The leaves of one franchise member: the film, or the episodes of the
// series inside its runs.
fn member(source: &mut dyn Source, entry: &franchise::Entry) -> Vec<Leaf> {
    let Some(held) = &entry.held else {
        return Vec::new();
    };
    if held.kind != "series" {
        return vec![leaf(franchise::slot(held.clone()), entry.position)];
    }
    source
        .episodes(&held.library, &held.id)
        .into_iter()
        .filter(|episode| entry.covers(episode.season, episode.episode))
        .map(|episode| {
            leaf(
                still(&held.library, &held.id, &held.title, episode),
                entry.position,
            )
        })
        .collect()
}

// One episode of a series as the slot the row draws.
fn still(library: &str, series: &str, name: &str, episode: Episode) -> Slot {
    Slot {
        library: library.to_string(),
        kind: EPISODES.to_string(),
        id: episode.id,
        title: episode.title,
        released: episode.released,
        art: episode.art,
        duration: episode.duration,
        episode: Some(InSeries {
            series: series.to_string(),
            name: name.to_string(),
            season: episode.season,
            episode: episode.episode,
        }),
        ..Slot::default()
    }
}

// The library and the id a play names: the film's, or the series' for an
// episode.
pub type Work = (String, String);

// The plays of the audience on these leaves, each with the leaf's index,
// for the walk. `plays` is every play of the audience keyed by the work it
// names. An episode leaf takes the plays of its series whose season and
// episode numbers are its own.
pub fn on_leaves(leaves: &[Leaf], plays: &HashMap<Work, Vec<Resume>>) -> Vec<thread::Play> {
    leaves
        .iter()
        .enumerate()
        .flat_map(|(index, leaf)| {
            let slot = &leaf.slot;
            let work = match &slot.episode {
                Some(place) => (slot.library.clone(), place.series.clone()),
                None => (slot.library.clone(), slot.id.clone()),
            };
            plays
                .get(&work)
                .into_iter()
                .flatten()
                .filter(move |play| match &slot.episode {
                    Some(place) => {
                        (play.progress.season, play.progress.episode)
                            == (place.season, place.episode)
                    }
                    None => true,
                })
                .map(move |play| thread::Play {
                    leaf: index,
                    progress: play.progress.clone(),
                    exact: play.exact,
                })
        })
        .collect()
}
