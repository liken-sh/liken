//! Maps each object to its top owner, because loop detection treats a
//! pod, its `ReplicaSet`, and its `Deployment` as one node: they come up
//! together in a cold start. Each `ManagedBy` edge already runs from an
//! owned object to its top owner, so one lookup folds the object into it.

use std::collections::{BTreeSet, HashMap};

use crate::edges::{Endpoint, Graph, Kind};

pub struct Owners<'a> {
    top: HashMap<&'a Endpoint, &'a Endpoint>,
    /// The namespaces of the objects folded into each top owner. A
    /// cluster-scoped owner, such as a catalog that owns a pod, has no
    /// namespace of its own, so a loop through it is reported on the
    /// namespaces of the objects it owns.
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

    /// The namespace of `owner`, if it has one, and the namespaces of the
    /// objects folded into it.
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
