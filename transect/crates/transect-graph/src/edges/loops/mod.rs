//! Loops: sets of parts that each need another of the set to come up.
//! In steady state a loop is invisible, because every part is already
//! running; on a cold start, such as after a power cut, no start order
//! works unless a part of the loop can start on its own.
//!
//! A loop is found among objects, and only then shown by the namespaces
//! its objects are in.  Collapsing each edge to its namespaces first
//! would join two namespaces whenever one object of each needs some
//! object of the other, though no path of edges leads from an object of
//! either back to itself: one namespace's small helper that depends on
//! another's workload, and that workload applied from the first
//! namespace's applier, is a path each way between the namespaces and
//! no cycle among objects.
//!
//! The loops are the strongly connected components of the object graph
//! that hold objects of more than one namespace.  A component inside one
//! namespace is left out, because there the counted kinds form cycles
//! that every GitOps cluster has and that do not block a cold start,
//! such as a tool's own applier applying the source it reconciles from,
//! and a reader learns nothing from a tag on every namespace that holds
//! one.

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

/// The kinds a loop is made of, in the order a link names them.  Each
/// states that its near end needs its far end to come up.  Allowed to
/// call allows without requiring, runs on is a placement that the
/// scheduler can change, and a `Service`'s selects and an `Ingress`'s
/// routes-to stay inside one namespace, where depends-on already
/// follows them.  Holds counts, because a pod that holds a device
/// cannot start until the device's driver publishes it.  Authenticates
/// through counts, because an application whose users cannot sign in
/// is not up for them.  Owner references are not among them because
/// they define the nodes: each object is represented by its top owner,
/// the workload a reader knows it by.
const COUNTED: [Kind; 6] = [
    Kind::ReconcilesFrom,
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::ControlledBy,
    Kind::StoresOn,
    Kind::Holds,
];

/// One loop: the namespaces its objects are in, and every counted edge
/// between two of its objects.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Loop {
    /// The namespaces in the loop, sorted.
    pub namespaces: Vec<String>,
    /// Each pair of members one or more counted edges join, sorted by
    /// their names.
    pub links: Vec<Link>,
}

/// The counted edges from one member of a loop to another, by the names
/// a sentence gives them.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Link {
    pub from: String,
    pub to: String,
    /// The kinds of the edges, each once, in `COUNTED`'s order.
    pub kinds: Vec<Kind>,
}

impl Loop {
    pub fn holds(&self, namespace: &str) -> bool {
        self.namespaces.iter().any(|member| member == namespace)
    }

    /// The cycles through every member, for drawing the loop.
    pub fn cycles(&self) -> Vec<Cycle> {
        cycles::cycles(&self.links)
    }
}

/// The ranks in `COUNTED` of the edges from one node to another.
type Joined = BTreeMap<(Endpoint, Endpoint), BTreeSet<usize>>;

impl Graph {
    /// Every loop, ordered by its namespaces.
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

/// The loop that `members`, one component, make, with the links among
/// them read from `joined`.
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
