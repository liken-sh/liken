//! Loops: sets of objects where each object needs another object of the
//! set to come up.  A loop does no harm while everything runs.  After a
//! cold start, such as a power cut, no start order brings the loop up
//! unless one of its objects can start without the others.
//!
//! A loop is a strongly connected component of the graph of objects
//! that has objects in more than one namespace.  The graph finds cycles
//! among objects first and reports their namespaces after.  Collapsing
//! the edges to pairs of namespaces first would report a loop whenever
//! each of two namespaces needs something in the other, even with no
//! cycle among objects.  For example, a helper in namespace `a` depends
//! on a workload in `b`, and an applier in `a` applies that workload.
//!
//! A component inside one namespace is left out, because every GitOps
//! cluster has one, such as an applier that applies the source it
//! reconciles from, and it does not block a cold start.

mod components;
mod cycles;
#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures;
mod owners;
mod sentence;
#[cfg(test)]
mod tests;

use std::collections::{BTreeMap, BTreeSet};

use super::{Endpoint, Graph, Kind};
pub use cycles::{Cycle, Step};
use owners::Owners;

/// The edge kinds that make a loop, in the order a link lists them.  An
/// edge of each kind means that its `from` end needs its `to` end to come
/// up.  The other kinds are left out for these reasons:
///
/// - `AllowedToCall` permits a call and does not require it.
/// - `RunsOn` is a placement that the scheduler can change.
/// - `Selects` and `RoutesTo` stay inside one namespace, where
///   `DependsOn` already follows them.
/// - `ManagedBy` is not an edge of the graph of objects: each object is
///   replaced by its top owner, the workload that a reader knows it by.
///
/// `Holds` counts because a pod cannot start until the driver publishes
/// the device that its claim needs.  `AuthenticatesThrough` counts because an
/// application is not up for its users while they cannot sign in.
const COUNTED: [Kind; 6] = [
    Kind::ReconcilesFrom,
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::ControlledBy,
    Kind::StoresOn,
    Kind::Holds,
];

/// One loop: the namespaces of its objects, and the counted edges
/// between those objects.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Loop {
    /// The namespaces in the loop, sorted.
    pub namespaces: Vec<String>,
    /// One link for each pair of members that has a counted edge from
    /// one to the other, sorted by name.
    pub links: Vec<Link>,
}

/// The counted edges from one member of a loop to another.  `from` and
/// `to` are the names that `sentence::name` writes.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Link {
    pub from: String,
    pub to: String,
    /// The kinds of the edges, each once, in the order of `COUNTED`.
    pub kinds: Vec<Kind>,
}

impl Loop {
    /// Whether one of the loop's objects is in `namespace`.
    pub fn holds(&self, namespace: &str) -> bool {
        self.namespaces.iter().any(|member| member == namespace)
    }

    /// The cycles that together pass through every member, for drawing
    /// the loop.
    pub fn cycles(&self) -> Vec<Cycle> {
        cycles::cycles(&self.links)
    }
}

/// For each pair of nodes, the positions in `COUNTED` of the edge kinds
/// that go from the first to the second.
type Joined = BTreeMap<(Endpoint, Endpoint), BTreeSet<usize>>;

impl Graph {
    /// Every loop, sorted by its namespaces.
    pub fn loops(&self) -> Vec<Loop> {
        let owners = Owners::of(self);
        let mut joined: Joined = BTreeMap::new();
        for edge in self.iter() {
            let Some(rank) = COUNTED.iter().position(|&kind| kind == edge.kind) else {
                continue;
            };
            let (from, to) = (owners.top(&edge.from), owners.top(&edge.to));
            if from != to {
                joined
                    .entry((from.clone(), to.clone()))
                    .or_default()
                    .insert(rank);
            }
        }
        let mut leaving: BTreeMap<Endpoint, BTreeSet<Endpoint>> = BTreeMap::new();
        for (from, to) in joined.keys() {
            leaving.entry(from.clone()).or_default().insert(to.clone());
        }
        let mut loops: Vec<Loop> = components::components(&leaving)
            .into_iter()
            .filter(|members| members.len() > 1)
            .map(|members| close(&members, &joined, &owners))
            .filter(|found| found.namespaces.len() > 1)
            .collect();
        loops.sort_by(|a, b| a.namespaces.cmp(&b.namespaces));
        loops
    }
}

/// The loop that the component `members` forms, with a link for each
/// pair of members that `joined` has an edge between.
fn close(members: &[Endpoint], joined: &Joined, owners: &Owners) -> Loop {
    let namespaces: BTreeSet<&str> = members
        .iter()
        .flat_map(|member| owners.namespaces(member))
        .collect();
    let inside: BTreeSet<&Endpoint> = members.iter().collect();
    let mut links: Vec<Link> = joined
        .iter()
        .filter(|((from, to), _)| inside.contains(from) && inside.contains(to))
        .map(|((from, to), ranks)| Link {
            from: sentence::name(from),
            to: sentence::name(to),
            kinds: ranks.iter().map(|&rank| COUNTED[rank]).collect(),
        })
        .collect();
    links.sort_by(|a, b| (&a.from, &a.to).cmp(&(&b.from, &b.to)));
    Loop {
        namespaces: namespaces.into_iter().map(Into::into).collect(),
        links,
    }
}
