//! This module indexes the cluster's addresses and resolves each
//! address that a workload is configured with against the index.  So an
//! environment variable that names a database's `Service` becomes a
//! depends-on edge from the workload to the database.
//!
//! Only whole names match.  A `Service`'s name inside a sentence or
//! inside a longer host does not show that the workload calls it, and
//! a wrong edge on the map misleads a reader more than a missing one.

mod card;
mod depends;
pub mod guess;
mod index;
mod parse;
mod reach;
pub mod relations;
mod sources;
mod stated;

use std::collections::BTreeSet;

pub use card::Depends;
pub use depends::{Dependency, depends_on, of_workload};
pub use index::Addresses;
pub use parse::addresses;
pub use sources::sources_depend_on;
pub use stated::{Readers, Reading as StatedReading, Stated, Unread, attaches, readers};

use transect_graph::edges::{Endpoint, Graph, Kind};

/// The cluster's DNS domain when no flag sets another.
pub const DEFAULT_DOMAIN: &str = "cluster.local";

/// The depends-on edges that cross a namespace's boundary.
#[derive(Debug, Default, PartialEq)]
pub struct Across {
    /// The other namespaces its workloads depend on.
    pub on: BTreeSet<String>,
    /// The machines its workloads depend on by address.
    pub machines: BTreeSet<String>,
    /// The other namespaces whose workloads depend on it.
    pub by: BTreeSet<String>,
}

impl Across {
    /// Reads the depends-on edges in `graph` that touch `namespace`, and
    /// skips each edge between two of its own workloads.
    pub fn of(graph: &Graph, namespace: &str) -> Self {
        let mut across = Self::default();
        let edges = graph.touching_namespace(namespace);
        for edge in edges.filter(|edge| edge.kind == Kind::DependsOn) {
            let (from, to) = (namespace_of(&edge.from), namespace_of(&edge.to));
            match (&edge.to, from == Some(namespace)) {
                (Endpoint::Machine(machine), true) => {
                    across.machines.insert(machine.clone());
                }
                (_, true) if to != Some(namespace) => {
                    across.on.extend(to.map(Into::into));
                }
                (_, false) => {
                    across.by.extend(from.map(Into::into));
                }
                _ => {}
            }
        }
        across
    }
}

fn namespace_of(end: &Endpoint) -> Option<&str> {
    match end {
        Endpoint::Object(object) => object.namespace.as_deref(),
        _ => None,
    }
}

#[cfg(test)]
mod ingress_ports;
#[cfg(test)]
mod tests;
