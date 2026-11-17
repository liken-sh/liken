//! Top owners as the nodes of the loop graph.  A pod, its `ReplicaSet`,
//! and its `Deployment` come up together, so they count as one part in a
//! cold start; the managed-by edges, which run from each owned object
//! straight to its top owner, fold each object into that owner.

use std::collections::{BTreeSet, HashMap};

use crate::edges::{Endpoint, Graph, Kind};

pub struct Owners<'a> {
    top: HashMap<&'a Endpoint, &'a Endpoint>,
    /// The namespaces of the objects folded into each top owner, and its
    /// own.  A cluster-scoped owner, such as a catalog that owns a pod,
    /// is in the namespaces its objects are in, so a loop through it
    /// shows on those namespaces.
    namespaces: HashMap<&'a Endpoint, BTreeSet<&'a str>>,
}

impl<'a> Owners<'a> {
    pub fn of(graph: &'a Graph) -> Self {
        let mut owners = Self {
            top: HashMap::new(),
            namespaces: HashMap::new(),
        };
        for edge in graph.of_kind(Kind::ManagedBy) {
            owners.top.insert(&edge.from, &edge.to);
            let namespaces = owners.namespaces.entry(&edge.to).or_default();
            namespaces.extend(namespace(&edge.from));
        }
        owners
    }

    /// The top owner of `end`, or `end` itself when it has no owner.
    pub fn top<'b>(&'b self, end: &'b Endpoint) -> &'b Endpoint {
        self.top.get(end).copied().unwrap_or(end)
    }

    /// The namespaces `owner` is in: its own, and those of the objects
    /// folded into it.
    pub fn namespaces<'b>(&'b self, owner: &'b Endpoint) -> impl Iterator<Item = &'b str> {
        let folded = self.namespaces.get(owner).into_iter().flatten().copied();
        namespace(owner).into_iter().chain(folded)
    }
}

fn namespace(end: &Endpoint) -> Option<&str> {
    match end {
        Endpoint::Object(object) => object.namespace.as_deref(),
        _ => None,
    }
}
