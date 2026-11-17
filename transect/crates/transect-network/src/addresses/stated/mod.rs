//! Edges from intent.  Each entry of a list such as `depends-on` is
//! resolved the way a configured address is.  So a dependency that
//! only a `Secret` or a `ConfigMap` names, and that no pod spec shows
//! for that reason, still draws the edge.  An address that names
//! nothing in the cluster becomes a box outside it, and an entry that
//! names nothing transect can read stays on the card as text.
//!
//! A list on a workload applies to that workload.  A list on a
//! `ConfigMap` or a `Secret` applies to each workload that reads it,
//! because that object contains the address, and so does a list on the
//! object that writes a `Secret`.  A list on anything else draws no
//! edge, because nothing calls out from a `Service` or a `Namespace`.
//! A list read for a `Secret`'s readers records in its provenance
//! which way transect read it, because transect never reads the
//! `Secret`'s data and reads its annotations only when it is
//! configured to.

mod entry;

use std::collections::{HashMap, HashSet};

use k8s_openapi::api::core::v1::PodSpec;

use super::index::Addresses;
use super::parse::{self, Address};
use super::reach::{behind, reach};
use entry::Entry;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance, Way};
use transect_graph::environment::{self, Environment};
use transect_resources::intent::Listed;

/// The workloads that read each `ConfigMap` and each `Secret`, and the
/// `Secret` each producer writes, so a list on a producer applies to
/// the workloads that read what it writes, as a list on a `ConfigMap`
/// does.
#[derive(Debug, Default)]
pub struct Readers {
    /// The workloads that read each `ConfigMap` or `Secret`.
    by_source: HashMap<Object, Vec<Object>>,
    /// The `Secret` each producer writes, by the producer.
    written: HashMap<Object, Object>,
}

impl Readers {
    /// These readers, with `written` naming the `Secret` each producer
    /// writes.
    pub fn with_written(self, written: HashMap<Object, Object>) -> Self {
        Self { written, ..self }
    }

    /// The workloads that read `source`, a `ConfigMap` or a `Secret`.
    fn of(&self, source: &Object) -> Vec<Object> {
        self.by_source.get(source).cloned().unwrap_or_default()
    }

    /// The `Secret` `producer` writes and the workloads that read it.
    fn of_producer(&self, producer: &Object) -> Option<(&Object, Vec<Object>)> {
        let secret = self.written.get(producer)?;
        Some((secret, self.of(secret)))
    }
}

/// What the entries resolve against.
pub struct Reading<'a> {
    pub addresses: &'a Addresses,
    pub graph: &'a Graph,
    pub ownership: &'a Ownership,
    pub plurals: &'a Plurals,
    /// Every object transect watches, which an object entry must name.
    pub known: &'a HashSet<Object>,
    pub readers: &'a Readers,
}

/// An entry that names nothing transect can read, and where it was
/// written.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Unread {
    /// The kind of edge its list draws, which selects the card section
    /// that shows it.
    pub kind: Kind,
    pub entry: String,
    pub provenance: Provenance,
}

/// The edges a list's entries draw, and the entries left unread, by the
/// workload whose card shows them.
#[derive(Debug, Default)]
pub struct Stated {
    pub edges: Vec<Edge>,
    pub unread: HashMap<Object, Vec<Unread>>,
}

impl Stated {
    /// These edges and unread entries with `other`'s added.
    pub fn extend(&mut self, other: Self) {
        self.edges.extend(other.edges);
        for (workload, unread) in other.unread {
            self.unread.entry(workload).or_default().extend(unread);
        }
    }
    /// A `kind` edge from each workload a list applies to, to each end
    /// its entries reach, once per pair, with the list's provenance.  A
    /// workload's own objects are no dependency of it, as with an
    /// address its spec names.
    pub fn read<'a>(
        kind: Kind,
        reading: &Reading,
        lists: impl IntoIterator<Item = (&'a Object, &'a Listed)>,
    ) -> Self {
        let mut stated = Self::default();
        for (object, listed) in lists {
            let workloads = reading.applies_to(object);
            if workloads.is_empty() {
                continue;
            }
            for entry in &listed.entries {
                let reached = reading.resolve(entry, object.namespace.as_deref());
                for (workload, way) in &workloads {
                    let provenance = match way {
                        Some(way) => listed.provenance.clone().through(way.clone()),
                        None => listed.provenance.clone(),
                    };
                    let Some((ends, guessed)) = &reached else {
                        let unread = Unread {
                            kind,
                            entry: entry.clone(),
                            provenance,
                        };
                        stated
                            .unread
                            .entry(workload.clone())
                            .or_default()
                            .push(unread);
                        continue;
                    };
                    let from = Endpoint::Object(workload.clone());
                    for to in ends.iter().filter(|end| !reading.owns(workload, end)) {
                        let seen = stated.edges.iter().any(|e| e.from == from && &e.to == to);
                        if !seen {
                            stated.edges.push(Edge {
                                kind,
                                from: from.clone(),
                                to: to.clone(),
                                guessed: *guessed,
                                provenance: provenance.clone(),
                            });
                        }
                    }
                }
            }
        }
        stated
    }
}

/// Whether a list on `object` can draw edges: it is a workload, or a
/// `ConfigMap` or `Secret`, whose readers the list applies to.
pub fn attaches(object: &Object, ownership: &Ownership) -> bool {
    let namespace = object.namespace.as_deref().unwrap_or_default();
    read_by_workloads(object) || ownership.workloads(namespace).any(|w| w == object)
}

/// Whether `object` is a `ConfigMap` or a `Secret`, which workloads
/// read.
fn read_by_workloads(object: &Object) -> bool {
    object.group.is_empty() && matches!(object.kind.as_str(), "ConfigMap" | "Secret")
}

impl Reading<'_> {
    /// The workloads a list on `object` applies to, each with the way
    /// it was read when that was through a `Secret`: a `ConfigMap`'s or
    /// a `Secret`'s readers, a workload itself, and the readers of the
    /// `Secret` a producer writes.
    fn applies_to(&self, object: &Object) -> Vec<(Object, Option<Way>)> {
        if read_by_workloads(object) {
            let way = (object.kind == "Secret").then(|| Way::Annotations {
                secret: object.name.clone(),
            });
            let readers = self.readers.of(object).into_iter();
            return readers.map(|reader| (reader, way.clone())).collect();
        }
        let mut workloads = match attaches(object, self.ownership) {
            true => vec![(object.clone(), None)],
            false => Vec::new(),
        };
        if let Some((secret, readers)) = self.readers.of_producer(object) {
            let way = Way::Producer {
                secret: secret.name.clone(),
            };
            for reader in readers {
                if !workloads.iter().any(|(workload, _)| workload == &reader) {
                    workloads.push((reader, Some(way.clone())));
                }
            }
        }
        workloads
    }

    /// What `entry`, written in `namespace`, reaches, and whether that
    /// is a guess, or none when it names nothing transect can read.
    fn resolve(&self, entry: &str, namespace: Option<&str>) -> Option<(Vec<Endpoint>, bool)> {
        match entry::read(entry, |resource| self.kinds(resource).next().is_some()) {
            Entry::Address(address) => Some(self.reach(address, namespace)),
            Entry::Object {
                namespace: written,
                resource,
                name,
            } => {
                let namespace = written.as_deref().or(namespace);
                self.object(&resource, namespace, &name)
                    .map(|ends| (ends, false))
            }
            Entry::Unread => None,
        }
    }

    /// The group and kind of each resource the cluster serves as
    /// `resource`, which names it by its plural, or by its plural and
    /// group.
    fn kinds<'a>(&'a self, resource: &'a str) -> impl Iterator<Item = &'a (String, String)> {
        self.plurals.iter().filter_map(move |(kind, plural)| {
            let named = resource == plural || resource == format!("{plural}.{}", kind.0);
            named.then_some(kind)
        })
    }

    /// What the one object transect watches as `resource/name` stands
    /// for: a machine for a `Node`, the workloads a `Service` selects,
    /// or any other object's top owner.  A name of no watched object,
    /// or of several, names nothing.
    fn object(&self, resource: &str, namespace: Option<&str>, name: &str) -> Option<Vec<Endpoint>> {
        let found: Vec<Object> = self
            .kinds(resource)
            .flat_map(|(group, kind)| {
                [
                    Object::new(group, kind, namespace, name),
                    Object::new(group, kind, None, name),
                ]
            })
            .filter(|object| self.known.contains(object))
            .collect();
        let [object] = found.as_slice() else {
            return None;
        };
        Some(self.stands_for(object))
    }

    /// What `address`, configured in `namespace`, reaches, and whether
    /// that is a guess.  A `Service`'s DNS name of a `Service` the
    /// cluster does not have reaches that `Service`, which has no page,
    /// and is no box outside.  One box outside stands for its host whatever port
    /// or path is written, as the ground's boxes do.
    fn reach(&self, address: Address, namespace: Option<&str>) -> (Vec<Endpoint>, bool) {
        let found = reach(self.addresses, self.graph, &address, namespace);
        if !found.to.is_empty() {
            return (found.to, found.guessed);
        }
        let end = match self.addresses.service_named(&address.host) {
            Some(service) => Endpoint::Object(service),
            None => Endpoint::Outside(address.host),
        };
        (vec![end], false)
    }

    /// What the one address `text`, written in `namespace`, reaches, and
    /// whether that is a guess, or none when it is not one address.
    pub fn address(&self, text: &str, namespace: Option<&str>) -> Option<(Vec<Endpoint>, bool)> {
        match parse::addresses(text).as_slice() {
            [address] => Some(self.reach(address.clone(), namespace)),
            _ => None,
        }
    }

    /// What `object` stands for on the map: a machine for a `Node`, the
    /// workloads a `Service` selects, a watched object's top owner, and
    /// any other object itself, because a name an operator writes in
    /// status names a real object even of a kind transect does not
    /// watch.
    pub fn stands_for(&self, object: &Object) -> Vec<Endpoint> {
        let end = Endpoint::Object(object.clone());
        match (object.group.as_str(), object.kind.as_str()) {
            ("", "Node") => vec![Endpoint::Machine(object.name.clone())],
            ("", "Service") => behind(self.graph, &end),
            _ if self.known.contains(object) => {
                vec![Endpoint::Object(self.ownership.top_owner(object))]
            }
            _ => vec![end],
        }
    }

    /// Whether `end` is `workload` or one of its own objects.
    pub fn owns(&self, workload: &Object, end: &Endpoint) -> bool {
        match end {
            Endpoint::Object(object) => &self.ownership.top_owner(object) == workload,
            _ => false,
        }
    }
}

/// The workloads that read each `ConfigMap` and `Secret`, from each
/// workload's pod specs.
pub fn readers<'a>(workloads: impl IntoIterator<Item = (&'a Object, Vec<&'a PodSpec>)>) -> Readers {
    let mut readers = Readers::default();
    for (workload, templates) in workloads {
        let namespace = workload.namespace.as_deref();
        for spec in templates {
            for source in Environment::read(spec).sources {
                let kind = match source.kind {
                    environment::Kind::ConfigMap => "ConfigMap",
                    environment::Kind::Secret => "Secret",
                };
                let object = Object::new("", kind, namespace, &source.name);
                let found = readers.by_source.entry(object).or_default();
                if !found.contains(workload) {
                    found.push(workload.clone());
                }
            }
        }
    }
    readers
}

#[cfg(test)]
mod tests;
